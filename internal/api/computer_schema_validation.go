package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

//go:embed computer_schema_generated.json
var computerValidationSchema []byte

var resolvedComputerSchemas = sync.OnceValues(func() (map[string]*jsonschema.Resolved, error) {
	result := make(map[string]*jsonschema.Resolved)
	for _, name := range []string{"ComputerReport", "ComputerReportDetail", "ComputerCommandResponse", "ComputerATIFTrajectory", "ComputerRunListResponse", "ComputerPerformanceReport"} {
		var schema jsonschema.Schema
		if err := json.Unmarshal(computerValidationSchema, &schema); err != nil {
			return nil, err
		}
		schema.Ref = "#/$defs/" + name
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return nil, err
		}
		result[name] = resolved
	}
	return result, nil
})

func validateComputerResponseSchema(body []byte, name string) error {
	if !utf8.Valid(body) {
		return errComputerResponse
	}
	schemas, err := resolvedComputerSchemas()
	if err != nil {
		return errors.New("computer response schema could not be loaded")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&value) != nil {
		return errComputerResponse
	}
	schema, ok := schemas[name]
	if !ok || schema.Validate(value) != nil {
		return errComputerResponse
	}
	return nil
}
