package parser

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/Thruqe/wa-proto/pkg/ast"
)

var (
	versionRe = regexp.MustCompile(`WhatsApp Version:\s*([0-9.]+)`)
	pkgRe     = regexp.MustCompile(`^package\s+([A-Za-z0-9_.]+);`)
	syntaxRe  = regexp.MustCompile(`^syntax\s*=\s*"([^"]+)";`)
	enumRe    = regexp.MustCompile(`^enum\s+([A-Za-z0-9_]+)\s*\{?`)
	msgRe     = regexp.MustCompile(`^message\s+([A-Za-z0-9_]+)\s*\{?`)
	oneofRe   = regexp.MustCompile(`^oneof\s+([A-Za-z0-9_]+)\s*\{?`)
	mapFieldRe = regexp.MustCompile(`^map\s*<\s*([A-Za-z0-9_.]+)\s*,\s*([A-Za-z0-9_.]+)\s*>\s+([A-Za-z0-9_]+)\s*=\s*(\d+)\s*;`)
	fieldRe   = regexp.MustCompile(`^(optional|repeated|required)?\s*([A-Za-z0-9_.]+)\s+([A-Za-z0-9_]+)\s*=\s*(\d+)(?:\s*\[([^\]]+)\])?\s*;`)
	enumValRe = regexp.MustCompile(`^([A-Za-z0-9_]+)\s*=\s*(-?\d+)\s*;`)
)

// ParseProto parses a .proto file into an ast.ProtoSchema.
func ParseProto(r io.Reader) (*ast.ProtoSchema, error) {
	scanner := bufio.NewScanner(r)
	// Support large buffers if needed
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	schema := &ast.ProtoSchema{
		Syntax: "proto2",
	}

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading proto: %w", err)
	}

	idx := 0
	inBlockComment := false
	for idx < len(lines) {
		line := strings.TrimSpace(lines[idx])
		if inBlockComment {
			if strings.Contains(line, "*/") {
				inBlockComment = false
			}
			idx++
			continue
		}
		if strings.HasPrefix(line, "/*") {
			if !strings.Contains(line, "*/") {
				inBlockComment = true
			}
			idx++
			continue
		}

		if line == "" || strings.HasPrefix(line, "//") {
			if m := versionRe.FindStringSubmatch(line); len(m) > 1 {
				schema.Version = m[1]
			}
			idx++
			continue
		}

		if m := syntaxRe.FindStringSubmatch(line); len(m) > 1 {
			schema.Syntax = m[1]
			idx++
			continue
		}

		if m := pkgRe.FindStringSubmatch(line); len(m) > 1 {
			schema.Package = m[1]
			idx++
			continue
		}

		if m := enumRe.FindStringSubmatch(line); len(m) > 1 {
			enumDef, nextIdx, err := parseEnum(lines, idx)
			if err != nil {
				return nil, err
			}
			if len(enumDef.Values) > 0 {
				schema.Enums = append(schema.Enums, enumDef)
			}
			idx = nextIdx
			continue
		}

		if m := msgRe.FindStringSubmatch(line); len(m) > 1 {
			msgDef, nextIdx, err := parseMessage(lines, idx)
			if err != nil {
				return nil, err
			}
			schema.Messages = append(schema.Messages, msgDef)
			idx = nextIdx
			continue
		}

		idx++
	}

	return schema, nil
}

func parseEnum(lines []string, start int) (*ast.EnumDef, int, error) {
	line := strings.TrimSpace(lines[start])
	m := enumRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return nil, start, fmt.Errorf("line %d: invalid enum header %q", start+1, line)
	}

	enumDef := &ast.EnumDef{
		Name: m[1],
	}

	idx := start + 1
	for idx < len(lines) {
		l := strings.TrimSpace(lines[idx])
		if l == "" || strings.HasPrefix(l, "//") {
			idx++
			continue
		}
		if l == "}" || strings.HasPrefix(l, "};") {
			idx++
			break
		}

		if vm := enumValRe.FindStringSubmatch(l); len(vm) > 2 {
			id, _ := strconv.Atoi(vm[2])
			enumDef.Values = append(enumDef.Values, &ast.EnumValueDef{
				Name: vm[1],
				ID:   id,
			})
		}
		idx++
	}

	return enumDef, idx, nil
}

func parseMessage(lines []string, start int) (*ast.MessageDef, int, error) {
	line := strings.TrimSpace(lines[start])
	m := msgRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return nil, start, fmt.Errorf("line %d: invalid message header %q", start+1, line)
	}

	msgDef := &ast.MessageDef{
		Name: m[1],
	}

	idx := start + 1
	for idx < len(lines) {
		l := strings.TrimSpace(lines[idx])
		if l == "" || strings.HasPrefix(l, "//") {
			idx++
			continue
		}
		if l == "}" || strings.HasPrefix(l, "};") {
			idx++
			break
		}

		// Nested enum
		if em := enumRe.FindStringSubmatch(l); len(em) > 1 {
			nestedEnum, nextIdx, err := parseEnum(lines, idx)
			if err != nil {
				return nil, idx, err
			}
			msgDef.NestedEnums = append(msgDef.NestedEnums, nestedEnum)
			idx = nextIdx
			continue
		}

		// Nested message
		if nm := msgRe.FindStringSubmatch(l); len(nm) > 1 {
			nestedMsg, nextIdx, err := parseMessage(lines, idx)
			if err != nil {
				return nil, idx, err
			}
			msgDef.NestedMessages = append(msgDef.NestedMessages, nestedMsg)
			idx = nextIdx
			continue
		}

		// Oneof
		if om := oneofRe.FindStringSubmatch(l); len(om) > 1 {
			oneofDef, nextIdx, err := parseOneof(lines, idx)
			if err != nil {
				return nil, idx, err
			}
			msgDef.Oneofs = append(msgDef.Oneofs, oneofDef)
			idx = nextIdx
			continue
		}

		// Map field
		if mm := mapFieldRe.FindStringSubmatch(l); len(mm) > 4 {
			id, _ := strconv.Atoi(mm[4])
			msgDef.Fields = append(msgDef.Fields, &ast.FieldDef{
				IsMap:    true,
				MapKey:   mm[1],
				MapValue: mm[2],
				Name:     mm[3],
				ID:       id,
				Rule:     "repeated",
			})
			idx++
			continue
		}

		// Regular field
		if fm := fieldRe.FindStringSubmatch(l); len(fm) > 4 {
			rule := fm[1]
			fieldType := fm[2]
			fieldName := fm[3]
			id, _ := strconv.Atoi(fm[4])
			opts := ""
			if len(fm) > 5 {
				opts = fm[5]
			}
			packed := strings.Contains(opts, "packed=true")

			msgDef.Fields = append(msgDef.Fields, &ast.FieldDef{
				Rule:   rule,
				Type:   fieldType,
				Name:   fieldName,
				ID:     id,
				Packed: packed,
			})
			idx++
			continue
		}

		idx++
	}

	return msgDef, idx, nil
}

func parseOneof(lines []string, start int) (*ast.OneofDef, int, error) {
	line := strings.TrimSpace(lines[start])
	m := oneofRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return nil, start, fmt.Errorf("line %d: invalid oneof header %q", start+1, line)
	}

	oneofDef := &ast.OneofDef{
		Name: m[1],
	}

	idx := start + 1
	for idx < len(lines) {
		l := strings.TrimSpace(lines[idx])
		if l == "" || strings.HasPrefix(l, "//") {
			idx++
			continue
		}
		if l == "}" || strings.HasPrefix(l, "};") {
			idx++
			break
		}

		if fm := fieldRe.FindStringSubmatch(l); len(fm) > 4 {
			fieldType := fm[2]
			fieldName := fm[3]
			id, _ := strconv.Atoi(fm[4])
			opts := ""
			if len(fm) > 5 {
				opts = fm[5]
			}
			packed := strings.Contains(opts, "packed=true")

			oneofDef.Fields = append(oneofDef.Fields, &ast.FieldDef{
				Type:   fieldType,
				Name:   fieldName,
				ID:     id,
				Packed: packed,
			})
		}
		idx++
	}

	return oneofDef, idx, nil
}
