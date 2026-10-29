package main

import (
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

var deviceSetFoldStateCmd = newDeviceSetFoldStateCommand()

func newDeviceSetFoldStateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-fold-state <folded|unfolded>",
		Short: "Fold or unfold a running multi-display iOS device (currently iPhone Duo)",
		Example: `  revyl device set-fold-state folded -s 0
  revyl device set-fold-state unfolded --session-id <session-id> --json`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"folded", "unfolded"},
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := api.ParseDeviceFoldState(args[0])
			if err != nil {
				return err
			}
			mgr, err := getDeviceSessionMgr(cmd)
			if err != nil {
				return err
			}
			session, err := resolveSessionTarget(cmd, mgr)
			if err != nil {
				return err
			}
			if err := mgr.SetFoldStateOnSession(cmd.Context(), session, state); err != nil {
				return err
			}
			jsonOrPrint(cmd, struct {
				FoldState api.DeviceFoldState `json:"fold_state"`
				Status    string              `json:"status"`
			}{FoldState: state, Status: "set"}, fmt.Sprintf("Fold state set to %s", state))
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	registerSessionTargetFlags(cmd)
	return cmd
}

func init() {
	deviceCmd.AddCommand(deviceSetFoldStateCmd)
}
