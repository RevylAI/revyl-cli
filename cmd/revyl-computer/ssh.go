package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/session-manager-plugin/src/datachannel"
	smlog "github.com/aws/session-manager-plugin/src/log"
	"github.com/aws/session-manager-plugin/src/message"
	"github.com/aws/session-manager-plugin/src/sdkutil"
	smsession "github.com/aws/session-manager-plugin/src/sessionmanagerplugin/session"
	"github.com/aws/session-manager-plugin/src/sessionmanagerplugin/session/sessionutil"
	_ "github.com/aws/session-manager-plugin/src/sessionmanagerplugin/session/shellsession"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/ui"
)

var sshCmd = &cobra.Command{
	Use:   "ssh",
	Args:  cobra.NoArgs,
	Short: "Open a shell on your organization's machine",
	Long: `Open an interactive shell on the machine assigned to your organization.

The connection is brokered by Revyl, so no SSH client, AWS plugin, or cloud
credentials are required. Use REVYL_API_KEY or sign in with 'revyl auth login'.
Type 'exit' to end the session.

EXAMPLES:
  revyl-computer ssh`,
	RunE: func(cmd *cobra.Command, args []string) error {
		devMode, _ := cmd.Flags().GetBool("dev")

		mgr := auth.NewManager()
		token, err := mgr.GetActiveToken()
		if err != nil || token == "" {
			ui.PrintWarning("Not authenticated")
			ui.PrintInfo("Set REVYL_API_KEY or run 'revyl auth login', then 'revyl-computer ssh'")
			return errors.New("not authenticated")
		}

		client := api.NewClientWithDevMode(token, devMode)
		return runBrokeredShellSession(cmd.Context(), client, startBrokeredShell)
	},
}

func runBrokeredShellSession(ctx context.Context, client *api.Client, startShell func(context.Context, *api.MacShellSession, func()) error) (err error) {
	startupCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	startupCtx, cancelStartup := context.WithTimeout(startupCtx, 30*time.Second)
	defer cancelStartup()

	session, err := client.OpenMacShellSession(startupCtx)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancelCleanup()
		cleanupErr := client.TerminateMacShellSession(cleanupCtx, session.SessionId)
		if cleanupErr == nil {
			return
		}
		err = errors.Join(err, errors.New("could not confirm remote shell termination; the session may remain until its idle timeout"))
	}()

	return startShell(startupCtx, session, func() {
		stopSignals()
		cancelStartup()
	})
}

func printShellWelcome() {
	ui.PrintBox("Revyl Computer", `You are on the machine assigned to your organization. Anything you
install or change here persists for your whole team.

Type 'exit' or press Ctrl-D to end the session.

Docs      https://docs.revyl.ai
Support   support@revyl.ai`)
}

// silenceSessionClosedBanner takes over the websocket handler the data channel
// installed. The library prints its own "Exiting session with sessionId" line
// while handling a channel-closed message, and that handler is a plain method
// with no override hook, so the message has to be caught before it reaches it.
// The plugin's own stop restores the terminal, which Session.Stop does not.
func silenceSessionClosedBanner(log smlog.T, shell *smsession.Session, handler smsession.ISessionPlugin) {
	shell.DataChannel.GetWsChannel().SetOnMessage(func(input []byte) {
		var incoming message.ClientMessage
		if err := incoming.DeserializeClientMessage(log, input); err == nil &&
			incoming.MessageType == message.ChannelClosedMessage {
			handler.Stop()
			return
		}
		shell.DataChannel.OutputMessageHandler(log, shell.Stop, shell.SessionId, input)
	})
}

func startBrokeredShell(ctx context.Context, session *api.MacShellSession, onReady func()) error {
	log := smlog.Logger(true, "revyl-computer")
	sdkutil.SetRegionAndProfile(session.Region, "")

	shell := &smsession.Session{
		SessionId:   session.SessionId,
		StreamUrl:   session.StreamUrl,
		TokenValue:  session.TokenValue,
		Endpoint:    fmt.Sprintf("ssm.%s.amazonaws.com", session.Region),
		ClientId:    uuid.NewString(),
		TargetId:    session.InstanceId,
		Region:      session.Region,
		DataChannel: &datachannel.DataChannel{},
		DisplayMode: sessionutil.NewDisplayMode(log),
	}
	return runBrokeredShell(ctx, log, shell, onReady)
}

func runBrokeredShell(ctx context.Context, log smlog.T, shell *smsession.Session, onReady func()) error {
	if err := awaitBrokeredShell(ctx, log, shell); err != nil {
		return err
	}
	defer closeBrokeredShellChannel(log, shell)

	handler, ok := smsession.SessionRegistry[shell.SessionType]
	if !ok {
		return fmt.Errorf("unsupported session type %q", shell.SessionType)
	}
	handler.Initialize(log, shell)
	silenceSessionClosedBanner(log, shell, handler)
	onReady()
	printShellWelcome()

	// Ending the session by closing stdin (Ctrl-D) surfaces as io.EOF here,
	// which is a normal exit rather than a failure.
	if err := handler.SetSessionHandlers(log); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func awaitBrokeredShell(ctx context.Context, log smlog.T, shell *smsession.Session) (err error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("shell startup canceled before connecting: %w", err)
	}
	opened := make(chan error)
	go func() {
		openErr := shell.OpenDataChannel(log)
		select {
		case opened <- openErr:
		case <-ctx.Done():
			closeBrokeredShellChannel(log, shell)
		}
	}()
	select {
	case <-ctx.Done():
		return fmt.Errorf("could not connect to the machine before the startup deadline: %w", ctx.Err())
	case openErr := <-opened:
		defer func() {
			if err != nil {
				closeBrokeredShellChannel(log, shell)
			}
		}()
		if openErr != nil {
			return errors.New("could not connect to the machine; try again")
		}
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("the machine did not complete the shell handshake before the startup deadline: %w", ctx.Err())
	case ready := <-shell.DataChannel.IsSessionTypeSet():
		if !ready {
			return errors.New("the machine did not start a shell")
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("shell startup canceled before the shell was ready: %w", err)
	}
	shell.SessionType = shell.DataChannel.GetSessionType()
	shell.SessionProperties = shell.DataChannel.GetSessionProperties()
	return nil
}

func closeBrokeredShellChannel(log smlog.T, shell *smsession.Session) {
	if err := shell.DataChannel.Close(log); err != nil {
		ui.PrintWarning("Could not close the shell connection cleanly")
	}
}
