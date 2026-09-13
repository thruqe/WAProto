package ast

import (
	"fmt"
	"strings"
)

// ProtoSchema represents a parsed protobuf schema.
type ProtoSchema struct {
	Syntax   string
	Package  string
	Version  string
	Messages []*MessageDef
	Enums    []*EnumDef
}

// EnumValueDef represents a single enum constant.
type EnumValueDef struct {
	Name    string
	ID      int
	Comment string
}

// EnumDef represents a protobuf enum definition.
type EnumDef struct {
	Name    string
	Values  []*EnumValueDef
	Comment string
}

// FieldDef represents a protobuf message field.
type FieldDef struct {
	Name     string
	ID       int
	Type     string
	Rule     string // "optional", "repeated", "required", or ""
	Packed   bool
	IsMap    bool
	MapKey   string
	MapValue string
	Comment  string
}

// OneofDef represents a protobuf oneof block.
type OneofDef struct {
	Name   string
	Fields []*FieldDef
}

// MessageDef represents a protobuf message definition.
type MessageDef struct {
	Name           string
	Fields         []*FieldDef
	Oneofs         []*OneofDef
	NestedMessages []*MessageDef
	NestedEnums    []*EnumDef
	Comment        string
}

// FindMessage finds a top-level message by name.
func (s *ProtoSchema) FindMessage(name string) *MessageDef {
	for _, m := range s.Messages {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// FindEnum finds a top-level enum by name.
func (s *ProtoSchema) FindEnum(name string) *EnumDef {
	for _, e := range s.Enums {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// AllTypeNames returns a set of all top-level message and enum names.
func (s *ProtoSchema) AllTypeNames() map[string]bool {
	types := make(map[string]bool)
	for _, m := range s.Messages {
		types[m.Name] = true
	}
	for _, e := range s.Enums {
		types[e.Name] = true
	}
	return types
}

// FormatProto2 formats a message definition in proto2 syntax.
func (m *MessageDef) FormatProto2(indent string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%smessage %s {\n", indent, m.Name))
	innerIndent := indent + "\t"

	for _, e := range m.NestedEnums {
		b.WriteString(e.FormatProto2(innerIndent))
		b.WriteString("\n\n")
	}

	for _, nm := range m.NestedMessages {
		b.WriteString(nm.FormatProto2(innerIndent))
		b.WriteString("\n\n")
	}

	for _, f := range m.Fields {
		b.WriteString(f.FormatProto2(innerIndent))
		b.WriteString("\n")
	}

	for _, o := range m.Oneofs {
		b.WriteString(fmt.Sprintf("%soneof %s {\n", innerIndent, o.Name))
		oneofIndent := innerIndent + "\t"
		for _, of := range o.Fields {
			packedStr := ""
			if of.Packed {
				packedStr = " [packed=true]"
			}
			b.WriteString(fmt.Sprintf("%s%s %s = %d%s;\n", oneofIndent, of.Type, of.Name, of.ID, packedStr))
		}
		b.WriteString(fmt.Sprintf("%s}\n", innerIndent))
	}

	b.WriteString(fmt.Sprintf("%s}", indent))
	return b.String()
}

// FormatProto2 formats an enum definition.
func (e *EnumDef) FormatProto2(indent string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%senum %s {\n", indent, e.Name))
	innerIndent := indent + "\t"
	for _, v := range e.Values {
		b.WriteString(fmt.Sprintf("%s%s = %d;\n", innerIndent, v.Name, v.ID))
	}
	b.WriteString(fmt.Sprintf("%s}", indent))
	return b.String()
}

// FormatProto2 formats a field definition in proto2 syntax.
func (f *FieldDef) FormatProto2(indent string) string {
	if f.IsMap {
		return fmt.Sprintf("%smap<%s, %s> %s = %d;", indent, f.MapKey, f.MapValue, f.Name, f.ID)
	}

	rule := f.Rule
	if rule == "" {
		rule = "optional"
	}

	packedStr := ""
	if f.Packed {
		packedStr = " [packed=true]"
	}

	return fmt.Sprintf("%s%s %s %s = %d%s;", indent, rule, f.Type, f.Name, f.ID, packedStr)
}
