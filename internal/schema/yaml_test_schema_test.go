package schema

import (
	"strings"
	"testing"
)

func TestYAMLTestSchemaDocumentsDeviceTarget(t *testing.T) {
	text := GetYAMLTestSchema()
	for _, want := range []string{"  device:", "os_version:", "orientation:", "revyl device targets"} {
		if !strings.Contains(text, want) {
			t.Fatalf("YAML test schema does not document %q", want)
		}
	}

	device, ok := YAMLTestSchemaJSON()["device"].(map[string]interface{})
	if !ok {
		t.Fatal("YAML test schema JSON has no device section")
	}
	fields, _ := device["fields"].(map[string]string)
	for _, field := range []string{"model", "os_version", "orientation"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("device section has no %q field", field)
		}
	}
}
