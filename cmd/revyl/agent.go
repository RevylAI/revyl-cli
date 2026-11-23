package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const agentRunKindProof = "proof"

// newAgentCommand builds `revyl agent`. The names `run` and `start` are
// reserved for a future general agent and must not be registered here.
func newAgentCommand() *cobra.Command {
	agent := &cobra.Command{
		Use:   "agent",
		Short: "Run Revyl agents on your app from the terminal or CI",
		Long: `Run Revyl agents on your app from the terminal or CI.

proof starts a proof run and prints its run ID. status reads a proof run by
its run ID, including a proof that a pull request started. cancel stops a
proof run that 'revyl agent proof' started.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	status := &cobra.Command{
		Use:   "status <run-id>",
		Short: "Read a proof run's state or result",
		Long: `Read a proof run's current state or result by its run ID.

It reads any proof run in your organization, whether 'revyl agent proof' or a
pull request started it; a pull request proof's run ID is in the address of
its page in Revyl, under Cloud Agents > Proof of Changes.

Prints the proof message on stdout once the run has finished; the status, run
ID and run URL go to stderr. The API key needs the Member role or higher to
read a proof run. Exits 0 when the proof passed or is still queued or running,
2 when it was inconclusive, and 1 when it ended without passing otherwise
(failed, cancelled, or a status this CLI does not know). For a pull request
proof the message is that run's own result, without the review findings, other
runs and re-run links of the pull request comment, and a finished run returns
to queued under the same ID when someone re-runs it.

With --json, stdout is the JSON object 'revyl agent proof --json' prints.
success is true only when status is passed, and verification_score is set
only when status is passed, failed or inconclusive.

` + agentProofFailureKindHelp,
		Example: `  revyl agent status <run-id>
  revyl agent status <run-id> --json`,
		Args: cobra.ExactArgs(1),
		RunE: runAgentStatus,
	}

	cancel := &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancel a proof run",
		Long: `Cancel a queued or running proof run that 'revyl agent proof' started.

Revyl stops the run's agent and frees its devices; the run's status becomes
cancelled. A run that already finished is left as it is. Either way the command
prints the run as 'revyl agent status' does and exits 0, so it is safe to run
from a CI job's cleanup step whether or not the proof is still going.

A pull request's proofs can't be cancelled this way, since a new commit or build
replaces them; for one, and for an ID no proof run has, the command exits 1.`,
		Example: `  revyl agent cancel <run-id>
  revyl agent cancel <run-id> --json`,
		Args: cobra.ExactArgs(1),
		RunE: runAgentCancel,
	}

	agent.AddCommand(newAgentProofCommand(), status, cancel)
	return agent
}

func isConflictResponse(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict
}

func isNotFoundResponse(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func parseAgentRunID(value string) (string, error) {
	runID, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", usageErrorf("invalid run ID: expected a run ID (a UUID), such as the run_id that 'revyl agent proof' printed")
	}
	return runID.String(), nil
}

func runAgentStatus(cmd *cobra.Command, args []string) error {
	runID, err := parseAgentRunID(args[0])
	if err != nil {
		return err
	}
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	run, err := agentProofNewClient(apiKey, devMode).GetProofRun(cmd.Context(), runID)
	if isNotFoundResponse(err) {
		return usageErrorf("no proof run has that ID. Check the run ID, and that your API key belongs to the organization that started the run")
	}
	if err != nil {
		return analytics.WithSafeDiagnostic(fmt.Errorf("get proof run: %w", err), "proof run read failed")
	}
	return finishAgentProof(cmd, run, proofRunOutcome(run), nil, jsonOutput, false)
}

func runAgentCancel(cmd *cobra.Command, args []string) error {
	runID, err := parseAgentRunID(args[0])
	if err != nil {
		return err
	}
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	run, err := agentProofNewClient(apiKey, devMode).CancelProofRun(cmd.Context(), runID)
	switch {
	case isNotFoundResponse(err):
		return usageErrorf("no proof run has that ID. Check the run ID, and that your API key belongs to the organization that started the run")
	case isConflictResponse(err):
		return usageErrorf("proof run %s belongs to a pull request and can't be cancelled; a new commit or build replaces it", runID)
	case err != nil:
		return analytics.WithSafeDiagnostic(fmt.Errorf("cancel proof run: %w", err), "proof run cancel failed")
	}
	outcome := proofRunOutcome(run)
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: agentProofAnalyticsDomain, DomainStatus: outcome})
	return writeAgentProofOutput(cmd, agentProofOutputFromRun(run), outcome, jsonOutput)
}
