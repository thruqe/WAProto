package parser

import (
	"os"
	"testing"
)

func TestParseProto_WAProto(t *testing.T) {
	f, err := os.Open("../../WAProto.proto")
	if err != nil {
		t.Skip("WAProto.proto not found, skipping integration test")
		return
	}
	defer f.Close()

	schema, err := ParseProto(f)
	if err != nil {
		t.Fatalf("ParseProto failed: %v", err)
	}

	if len(schema.Messages) == 0 {
		t.Errorf("expected messages, got 0")
	}
	if len(schema.Enums) == 0 {
		t.Errorf("expected enums, got 0")
	}

	t.Logf("Parsed %d messages and %d enums. Version: %s", len(schema.Messages), len(schema.Enums), schema.Version)
}

