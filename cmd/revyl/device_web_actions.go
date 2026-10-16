package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

type webActionResult struct {
	Action    string      `json:"action"`
	Success   bool        `json:"success"`
	LatencyMs json.Number `json:"latency_ms,omitempty"`
	Direction string      `json:"direction,omitempty"`
	Amount    int         `json:"amount,omitempty"`
}

var deviceScrollCmd = &cobra.Command{
	Use:   "scroll [direction]",
	Short: "Scroll a Web page by pixels, optionally at an element or coordinate",
	Example: `  revyl device scroll down --amount 600
  revyl device scroll up --amount 300 --target "Results list"
  revyl device scroll right --amount 200 --x 400 --y 300`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		direction, _ := cmd.Flags().GetString("direction")
		if len(args) > 0 {
			if cmd.Flags().Changed("direction") {
				return fmt.Errorf("provide direction as an argument or --direction, not both")
			}
			direction = args[0]
		}
		amount, _ := cmd.Flags().GetInt("amount")
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionTarget(cmd, mgr)
		if err != nil {
			return err
		}
		target, x, y := webActionTarget(cmd)
		response, err := mgr.ScrollOnSession(cmd.Context(), session, direction, amount, target, x, y, "")
		if err != nil {
			return err
		}
		result := buildActionResult("scroll", 0, 0, "", response)
		jsonOrPrint(cmd, webActionResult{Action: "scroll", Success: result.Success, LatencyMs: result.LatencyMs, Direction: direction, Amount: amount}, "Scrolled Web page")
		return nil
	},
}

var deviceSelectCmd = &cobra.Command{
	Use:   "select <option-label>",
	Short: "Choose an option by its exact label in a native Web select element",
	Example: `  revyl device select "Canada" --target "Country"
  revyl device select "Newest" --x 400 --y 300 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionTarget(cmd, mgr)
		if err != nil {
			return err
		}
		target, x, y := webActionTarget(cmd)
		response, err := mgr.SelectOnSession(cmd.Context(), session, args[0], target, x, y, "")
		if err != nil {
			return err
		}
		result := buildActionResult("select", 0, 0, "", response)
		jsonOrPrint(cmd, webActionResult{Action: "select", Success: result.Success, LatencyMs: result.LatencyMs}, "Selected Web option")
		return nil
	},
}

func webActionTarget(cmd *cobra.Command) (string, *int, *int) {
	target, _ := cmd.Flags().GetString("target")
	var x, y *int
	if cmd.Flags().Changed("x") {
		value, _ := cmd.Flags().GetInt("x")
		x = &value
	}
	if cmd.Flags().Changed("y") {
		value, _ := cmd.Flags().GetInt("y")
		y = &value
	}
	return target, x, y
}

func init() {
	for _, cmd := range []*cobra.Command{deviceScrollCmd, deviceSelectCmd} {
		cmd.Flags().String("target", "", "Element description (grounded); mutually exclusive with --x/--y")
		cmd.Flags().Int("x", 0, "X coordinate in pixels (requires --y)")
		cmd.Flags().Int("y", 0, "Y coordinate in pixels (requires --x)")
		cmd.Flags().Bool("json", false, "Output as JSON")
		registerSessionTargetFlags(cmd)
		deviceCmd.AddCommand(cmd)
	}
	deviceScrollCmd.Flags().String("direction", "", "Scroll direction: up, down, left, right")
	deviceScrollCmd.Flags().Int("amount", 600, "Scroll distance in pixels (1–16384)")
}
