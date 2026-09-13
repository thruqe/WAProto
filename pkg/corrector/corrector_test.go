package corrector

import (
	"strings"
	"testing"

	"github.com/Thruqe/wa-proto/pkg/ast"
)

func TestFixProto2_Message(t *testing.T) {
	schema := &ast.ProtoSchema{
		Syntax: "proto2",
		Messages: []*ast.MessageDef{
			{
				Name: "TestMessage",
				Fields: []*ast.FieldDef{
					{Name: "id", ID: 1, Type: "string", Rule: ""},
					{Name: "count", ID: 2, Type: "int32", Rule: "optional"},
					{Name: "requiredField", ID: 3, Type: "bool", Rule: "required"},
				},
			},
		},
	}

	FixProto2(schema)

	if schema.Messages[0].Fields[0].Rule != "optional" {
		t.Errorf("expected empty rule to default to optional, got %s", schema.Messages[0].Fields[0].Rule)
	}
	if schema.Messages[0].Fields[2].Rule != "required" {
		t.Errorf("expected required rule to be preserved in proto2, got %s", schema.Messages[0].Fields[2].Rule)
	}
}

func TestFixProto2Content_String(t *testing.T) {
	raw := `syntax = "proto3";
package waproto;
`
	fixed := FixProto2Content(raw)
	if !strings.Contains(fixed, `syntax = "proto2";`) {
		t.Errorf("expected syntax = \"proto2\";, got:\n%s", fixed)
	}
}
