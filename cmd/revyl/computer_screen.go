package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

func addComputerScreenCommands(parent *cobra.Command) {
	for _, action := range []string{"screenshot", "click", "move", "drag", "scroll", "type", "key"} {
		command := &cobra.Command{Use: action + " C1", Short: "Perform computer screen " + action}
		command.Flags().String("out", "", "Save the returned PNG to this local file")
		switch action {
		case "screenshot":
			command.Flags().String("zoom", "", "Crop a screenshot region: x0,y0,x1,y1")
		case "click", "move", "scroll":
			command.Flags().Int("x", 0, "X coordinate in screenshot pixels (required)")
			command.Flags().Int("y", 0, "Y coordinate in screenshot pixels (required)")
			if action == "click" {
				command.Flags().String("button", "left", "Mouse button: left, right, or middle")
				command.Flags().Int("count", 1, "Click count (1-3)")
				command.Flags().StringSlice("mods", nil, "Modifiers: shift, ctrl, alt (Option), cmd (comma separated)")
			}
			if action == "scroll" {
				command.Flags().String("direction", "", "Scroll direction: up, down, left, or right (required)")
				command.Flags().Int("amount", 3, "Scroll amount (1-20)")
				command.Flags().StringSlice("mods", nil, "Modifiers: shift, ctrl, alt (Option), cmd (comma separated)")
			}
		case "drag":
			for _, name := range []string{"start-x", "start-y", "end-x", "end-y"} {
				command.Flags().Int(name, 0, "Coordinate in screenshot pixels (required)")
			}
		case "type":
			command.Flags().String("text", "", "Text to type: 1-500 printable ASCII characters, newline, or tab")
			command.Flags().String("text-env", "", "Local environment variable containing text to type (not printed)")
		case "key":
			command.Flags().String("key", "", "Key chords separated by spaces, such as cmd+space (required)")
			command.Flags().Float64("hold", 0, "Seconds to hold the key (0-5); only with a single chord")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
				if len(args) != 1 {
					return nil, errors.New("computer screen command requires exactly one computer UUID")
				}
				id, err := parseComputerUUID(args[0])
				if err != nil {
					return nil, err
				}
				invocation.ComputerSessionID = id.String()
				out, _ := cmd.Flags().GetString("out")
				if cmd.Flags().Changed("out") && out == "" {
					return nil, errors.New("--out requires a local file path")
				}
				var screenAction api.ComputerScreenAction
				var region []int
				if action == "screenshot" {
					region, err = computerScreenshotRegion(cmd)
				} else {
					screenAction, err = computerScreenAction(cmd, action)
				}
				if err != nil {
					return nil, err
				}
				client, err := computerClient(cmd)
				if err != nil {
					return nil, err
				}
				if action == "screenshot" {
					return client.GetComputerScreenshot(cmd.Context(), id, region, "cli")
				}
				return client.PerformComputerScreenAction(cmd.Context(), id, api.ComputerScreenActionRequest{
					Action: screenAction, Screenshot: out != "", Via: "cli",
				})
			})
		}
		parent.AddCommand(command)
	}
}

func computerScreenshotRegion(cmd *cobra.Command) ([]int, error) {
	if !cmd.Flags().Changed("zoom") {
		return nil, nil
	}
	text, _ := cmd.Flags().GetString("zoom")
	parts := strings.Split(text, ",")
	if len(parts) != 4 {
		return nil, errors.New("--zoom requires four integers: x0,y0,x1,y1")
	}
	region := make([]int, 4)
	for i, part := range parts {
		coordinate, err := strconv.Atoi(part)
		if err != nil || coordinate < 0 || coordinate > 4095 {
			return nil, errors.New("--zoom requires coordinates from 0 to 4095")
		}
		region[i] = coordinate
	}
	if region[0] >= region[2] || region[1] >= region[3] {
		return nil, errors.New("--zoom requires positive width and height")
	}
	return region, nil
}

func computerScreenAction(cmd *cobra.Command, kind string) (api.ComputerScreenAction, error) {
	action := api.ComputerScreenAction{Action: kind}
	coordinate := func(name string) (*int, error) {
		if !cmd.Flags().Changed(name) {
			return nil, fmt.Errorf("--%s is required", name)
		}
		value, _ := cmd.Flags().GetInt(name)
		return &value, nil
	}
	var err error
	switch kind {
	case "click", "move", "scroll":
		action.X, err = coordinate("x")
		if err != nil {
			return action, err
		}
		action.Y, err = coordinate("y")
		if err != nil {
			return action, err
		}
		if kind == "click" {
			action.Button, _ = cmd.Flags().GetString("button")
			count, _ := cmd.Flags().GetInt("count")
			action.Count = &count
			action.Modifiers, _ = cmd.Flags().GetStringSlice("mods")
		}
		if kind == "scroll" {
			action.Direction, _ = cmd.Flags().GetString("direction")
			amount, _ := cmd.Flags().GetInt("amount")
			action.Amount = &amount
			action.Modifiers, _ = cmd.Flags().GetStringSlice("mods")
		}
	case "drag":
		for _, field := range []struct {
			name  string
			value **int
		}{{"start-x", &action.StartX}, {"start-y", &action.StartY}, {"end-x", &action.EndX}, {"end-y", &action.EndY}} {
			*field.value, err = coordinate(field.name)
			if err != nil {
				return action, err
			}
		}
	case "type":
		if cmd.Flags().Changed("text") == cmd.Flags().Changed("text-env") {
			return action, errors.New("provide exactly one of --text or --text-env")
		}
		action.Text, _ = cmd.Flags().GetString("text")
		if cmd.Flags().Changed("text-env") {
			name, _ := cmd.Flags().GetString("text-env")
			var ok bool
			action.Text, ok = os.LookupEnv(name)
			if !ok || name == "" {
				return action, errors.New("--text-env must name a set local environment variable")
			}
		}
	case "key":
		action.Keys, _ = cmd.Flags().GetString("key")
		action.HoldSeconds, _ = cmd.Flags().GetFloat64("hold")
	}
	return action, api.ValidateComputerScreenAction(action)
}

func writeComputerScreenOutput(cmd *cobra.Command, value any, jsonOutput bool) error {
	var screenshot *api.ComputerScreenshot
	var cursor *api.ComputerScreenPoint
	switch result := value.(type) {
	case *api.ComputerScreenshot:
		copy := *result
		screenshot, cursor, value = &copy, copy.Cursor, &copy
	case *api.ComputerScreenActionResult:
		copy := *result
		cursor = copy.Cursor
		if copy.Screenshot != nil {
			image := *copy.Screenshot
			copy.Screenshot, screenshot = &image, &image
		}
		value = &copy
	}
	if screenshot != nil {
		data, err := screenshot.DecodePNG()
		if err != nil {
			return err
		}
		out, _ := cmd.Flags().GetString("out")
		if out != "" {
			if os.WriteFile(out, data, 0600) != nil {
				return errors.New("computer PNG could not be saved; take another screenshot to save it")
			}
			if !jsonOutput {
				fmt.Fprintf(cmd.ErrOrStderr(), "Saved %s (%dx%d)\n", computerPrintable(out), screenshot.Width, screenshot.Height)
			}
		} else if !jsonOutput {
			fmt.Fprintf(cmd.ErrOrStderr(), "Screenshot %dx%d; use --out to save the PNG\n", screenshot.Width, screenshot.Height)
		}
		screenshot.PNG = api.EncodeComputerBytes(data)
	}
	if jsonOutput {
		return writeComputerJSON(cmd.OutOrStdout(), value, api.ComputerScreenMaxResponseJSON)
	}
	if cursor != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Cursor (%d, %d)\n", cursor.X, cursor.Y)
	} else {
		fmt.Fprintln(cmd.ErrOrStderr(), "Cursor unknown")
	}
	return nil
}
