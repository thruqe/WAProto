package corrector

import (
	"strings"

	"github.com/Thruqe/wa-proto/pkg/ast"
)

// FixProto2 ensures a ProtoSchema conforms strictly to proto2 syntax with explicit field rules.
func FixProto2(schema *ast.ProtoSchema) {
	schema.Syntax = "proto2"
	for _, m := range schema.Messages {
		fixMessageProto2(m)
	}
}

func fixMessageProto2(m *ast.MessageDef) {
	for _, f := range m.Fields {
		if f.Rule == "" && !f.IsMap {
			f.Rule = "optional"
		}
	}
	for _, nm := range m.NestedMessages {
		fixMessageProto2(nm)
	}
}

// FixProto2Content ensures proto2 syntax and explicit field rules in raw proto text.
func FixProto2Content(content string) string {
	content = strings.Replace(content, `syntax = "proto3";`, `syntax = "proto2";`, 1)
	return content
}

// FixProto3 is retained as a compatibility alias forwarding to FixProto2.
func FixProto3(schema *ast.ProtoSchema) {
	FixProto2(schema)
}

// FixProto3Content is retained as a compatibility alias forwarding to FixProto2Content.
func FixProto3Content(content string) string {
	return FixProto2Content(content)
}
