package toolcatalog

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type Parameter struct {
	Type        string     `json:"type"`
	Description string     `json:"description"`
	Format      string     `json:"format,omitempty"`
	Items       *Parameter `json:"items,omitempty"`
	Minimum     *float64   `json:"minimum,omitempty"`
	Maximum     *float64   `json:"maximum,omitempty"`
	MaxLength   int        `json:"maxLength,omitempty"`
	MaxItems    int        `json:"maxItems,omitempty"`
	MinItems    int        `json:"minItems,omitempty"`
	Default     any        `json:"default,omitempty"`
	Enum        []string   `json:"enum,omitempty"`
}

type InputSchema struct {
	Type                 string               `json:"type"`
	Properties           map[string]Parameter `json:"properties"`
	Required             []string             `json:"required"`
	AdditionalProperties bool                 `json:"additionalProperties"`
}

type Definition struct {
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	ReadOnly       bool        `json:"read_only"`
	Command        []string    `json:"command"`
	InputSchema    InputSchema `json:"input_schema"`
	ViewRenderer   string      `json:"view_renderer,omitempty"`
	Positionals    []string    `json:"-"`
	FixedArguments []string    `json:"-"`
}

type Spec struct {
	Name, Command, Description   string
	Flags, Required, Positionals []string
	FixedArguments               []string
	Write                        bool
	Defaults                     map[string]any
}

func Build(root *cobra.Command, specs []Spec) ([]Definition, error) {
	definitions := make([]Definition, 0, len(specs))
	for _, spec := range specs {
		path := strings.Fields(spec.Command)
		command, remaining, err := root.Find(path)
		if err != nil || len(remaining) != 0 || command.RunE == nil {
			return nil, fmt.Errorf("tool %s has no executable command", spec.Name)
		}
		properties := map[string]Parameter{}
		for _, name := range spec.Flags {
			flag := command.Flags().Lookup(name)
			if flag == nil {
				flag = command.InheritedFlags().Lookup(name)
			}
			if flag == nil {
				return nil, fmt.Errorf("tool %s has unknown flag %s", spec.Name, name)
			}
			parameter := Parameter{Description: flag.Usage}
			switch flag.Value.Type() {
			case "string", "duration":
				parameter.Type = "string"
				parameter.MaxLength = 4000
			case "bool":
				parameter.Type = "boolean"
			case "int":
				parameter.Type = "integer"
			case "float64":
				parameter.Type = "number"
			case "stringSlice", "stringArray":
				parameter.Type = "array"
				parameter.Items = &Parameter{Type: "string", MaxLength: 500}
				parameter.MaxItems = 20
			default:
				return nil, fmt.Errorf("tool %s has unsupported flag %s", spec.Name, name)
			}
			key := strings.ReplaceAll(name, "-", "_")
			parameter.Default = spec.Defaults[key]
			switch name {
			case "platform":
				parameter.Enum = []string{"ios", "android"}
			case "direction":
				parameter.Enum = []string{"both", "in", "out"}
			case "surface-scope":
				parameter.Enum = []string{"all", "app", "app+system", "app+external"}
			}
			if name == "session-id" || name == "app" || name == "observation" || name == "report-id" || name == "test-id" || name == "client-request-id" || name == "base-build" || name == "head-build" {
				parameter.Format = "uuid"
			}
			if name == "reports" {
				parameter.MaxItems = 5
				parameter.MinItems = 1
				parameter.Items.Format = "uuid"
			}
			if parameter.Type == "integer" || parameter.Type == "number" {
				minimum, maximum := 0.0, 86400.0
				if name == "limit" || name == "tail" {
					minimum, maximum = 1, 100
				}
				if name == "page" {
					minimum, maximum = 1, 10000
				}
				if name == "offset" {
					maximum = 100000
				}
				if name == "explorers" {
					minimum, maximum = 1, 100
				}
				parameter.Minimum, parameter.Maximum = &minimum, &maximum
			}
			properties[key] = parameter
		}
		for _, key := range spec.Positionals {
			parameter := Parameter{Type: "string", Description: key, MaxLength: 500}
			if strings.HasSuffix(key, "_id") {
				parameter.Format = "uuid"
			}
			properties[key] = parameter
		}
		required := append([]string{}, spec.Positionals...)
		required = append(required, spec.Required...)
		definitions = append(definitions, Definition{Name: spec.Name, Description: spec.Description, ReadOnly: !spec.Write, Command: path, InputSchema: InputSchema{Type: "object", Properties: properties, Required: required}, ViewRenderer: viewRenderers[spec.Name], Positionals: spec.Positionals, FixedArguments: spec.FixedArguments})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions, nil
}

func (d Definition) Arguments(input map[string]any) ([]string, error) {
	for key := range input {
		if _, ok := d.InputSchema.Properties[key]; !ok {
			return nil, fmt.Errorf("unknown parameter %q; use tools describe %s", key, d.Name)
		}
	}
	values := map[string]any{}
	for key, parameter := range d.InputSchema.Properties {
		value, present := input[key]
		if !present {
			value = parameter.Default
		}
		if value != nil {
			if err := validate(parameter, value); err != nil {
				return nil, fmt.Errorf("parameter %s: %w", key, err)
			}
			values[key] = value
		} else if present {
			return nil, fmt.Errorf("parameter %s cannot be null", key)
		}
	}
	for _, key := range d.InputSchema.Required {
		value, ok := values[key]
		if !ok {
			return nil, fmt.Errorf("required parameter %s is missing", key)
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("required parameter %s is empty", key)
		}
	}
	args := append([]string{}, d.Command...)
	args = append(args, "--json")
	args = append(args, d.FixedArguments...)
	positionals := map[string]bool{}
	for _, key := range d.Positionals {
		positionals[key] = true
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if !positionals[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		var encoded string
		switch v := value.(type) {
		case string:
			encoded = v
		case bool:
			encoded = strconv.FormatBool(v)
		case json.Number:
			encoded = v.String()
		case float64:
			encoded = strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			encoded = strconv.Itoa(v)
		case []any:
			parts := make([]string, len(v))
			for i, item := range v {
				parts[i] = item.(string)
			}
			var buffer bytes.Buffer
			writer := csv.NewWriter(&buffer)
			_ = writer.Write(parts)
			writer.Flush()
			encoded = strings.TrimSuffix(buffer.String(), "\n")
		default:
			return nil, fmt.Errorf("unsupported parameter %s", key)
		}
		args = append(args, "--"+strings.ReplaceAll(key, "_", "-")+"="+encoded)
	}
	if len(d.Positionals) > 0 {
		args = append(args, "--")
	}
	for _, key := range d.Positionals {
		args = append(args, values[key].(string))
	}
	return args, nil
}

func validate(parameter Parameter, value any) error {
	switch parameter.Type {
	case "string":
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected string")
		}
		if len(v) > parameter.MaxLength || strings.ContainsRune(v, 0) {
			return fmt.Errorf("string is too long or contains NUL")
		}
		if len(parameter.Enum) > 0 {
			found := false
			for _, choice := range parameter.Enum {
				if v == choice {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("expected one of %s", strings.Join(parameter.Enum, ", "))
			}
		}
		if parameter.Format == "uuid" {
			if id, err := uuid.Parse(v); err != nil || id.String() != v {
				return fmt.Errorf("expected canonical UUID from a previous result")
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	case "integer", "number":
		var n float64
		switch v := value.(type) {
		case json.Number:
			var err error
			n, err = v.Float64()
			if err != nil {
				return fmt.Errorf("expected number")
			}
		case int:
			n = float64(v)
		case float64:
			n = v
		default:
			return fmt.Errorf("expected number")
		}
		if parameter.Type == "integer" && n != float64(int64(n)) {
			return fmt.Errorf("expected integer")
		}
		if parameter.Minimum != nil && n < *parameter.Minimum {
			return fmt.Errorf("number must be at least %g", *parameter.Minimum)
		}
		if parameter.Maximum != nil && n > *parameter.Maximum {
			return fmt.Errorf("number must be at most %g", *parameter.Maximum)
		}
	case "array":
		items, ok := value.([]any)
		if !ok || len(items) > parameter.MaxItems || len(items) < parameter.MinItems {
			return fmt.Errorf("expected bounded array")
		}
		for _, item := range items {
			if err := validate(*parameter.Items, item); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported schema type")
	}
	return nil
}
