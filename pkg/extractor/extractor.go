package extractor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	protoAst "github.com/Thruqe/wa-proto/pkg/ast"
	"github.com/Thruqe/wa-proto/pkg/catalog"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
)

// ModuleSchema represents the extracted protobuf definitions of a single module.
type ModuleSchema struct {
	Name      string
	Version   string
	Messages  []*protoAst.MessageDef
	Enums     []*protoAst.EnumDef
	Imports   []string
	CrossRefs []string
}

// Extractor extracts protobuf schemas from WhatsApp Web JavaScript bundle sources.
type Extractor struct {
	Version string
	Modules map[string]*ModuleSchema
}

// NewExtractor creates a new extractor instance.
func NewExtractor(version string) *Extractor {
	return &Extractor{
		Version: version,
		Modules: make(map[string]*ModuleSchema),
	}
}

// ProcessSource parses a JavaScript bundle source and extracts all protobuf modules.
func (e *Extractor) ProcessSource(src string) error {
	// Patch known Webpack split idiosyncrasies
	patched := strings.ReplaceAll(src, "LimitSharing$Trigger", "LimitSharing$TriggerType")

	prog, err := parser.ParseFile(nil, "", patched, 0)
	if err != nil {
		// Some composite bundles separated by FB_PKG_DELIM may fail whole-file parse.
		// Try splitting and parsing each part.
		parts := strings.Split(patched, "/*FB_PKG_DELIM*/")
		if len(parts) > 1 {
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if !strings.Contains(part, "Spec") && !strings.Contains(part, "internalSpec") {
					continue
				}
				subProg, subErr := parser.ParseFile(nil, "", part, 0)
				if subErr == nil {
					for _, stmt := range subProg.Body {
						e.inspectStatement(stmt)
					}
				}
			}
			return nil
		}
		return fmt.Errorf("parsing JS source: %w", err)
	}

	for _, stmt := range prog.Body {
		e.inspectStatement(stmt)
	}

	return nil
}

func (e *Extractor) inspectStatement(stmt ast.Statement) {
	callExpr, ok := getCallExpr(stmt)
	if !ok || len(callExpr.ArgumentList) < 3 {
		return
	}

	modNameLit, ok := callExpr.ArgumentList[0].(*ast.StringLiteral)
	if !ok {
		return
	}
	moduleName := modNameLit.Value.String()

	fn, ok := callExpr.ArgumentList[2].(*ast.FunctionLiteral)
	if !ok || fn.Body == nil {
		return
	}

	// First pass: collect local enums and track intermediate spec containers.
	enums := make(map[string]*protoAst.EnumDef)
	specContainers := make(map[string]bool)

	for _, s := range fn.Body.List {
		switch node := s.(type) {
		case *ast.VariableStatement:
			for _, decl := range node.List {
				ident, ok := decl.Target.(*ast.Identifier)
				if !ok {
					continue
				}
				varName := ident.Name.String()
				if decl.Initializer == nil {
					continue
				}

				switch init := decl.Initializer.(type) {
				case *ast.ObjectLiteral:
					if len(init.Value) == 0 {
						specContainers[varName] = true
					} else if enumDef := parseEnumObject(varName, init); enumDef != nil {
						enums[varName] = enumDef
					}
				case *ast.CallExpression:
					if enumDef := parseInternalEnumCall(varName, init); enumDef != nil {
						enums[varName] = enumDef
					}
				}
			}
		case *ast.LexicalDeclaration:
			for _, decl := range node.List {
				ident, ok := decl.Target.(*ast.Identifier)
				if !ok {
					continue
				}
				varName := ident.Name.String()
				if decl.Initializer == nil {
					continue
				}

				switch init := decl.Initializer.(type) {
				case *ast.ObjectLiteral:
					if len(init.Value) == 0 {
						specContainers[varName] = true
					} else if enumDef := parseEnumObject(varName, init); enumDef != nil {
						enums[varName] = enumDef
					}
				case *ast.CallExpression:
					if enumDef := parseInternalEnumCall(varName, init); enumDef != nil {
						enums[varName] = enumDef
					}
				}
			}
		}
	}

	// Second pass: collect assignments (including minified SequenceExpressions).
	containerNames := make(map[string]string)
	containerSpecs := make(map[string]*ast.ObjectLiteral)

	var messages []*protoAst.MessageDef
	var exportedEnums []*protoAst.EnumDef

	for _, s := range fn.Body.List {
		exprStmt, ok := s.(*ast.ExpressionStatement)
		if !ok {
			continue
		}

		assigns := collectAssignments(exprStmt.Expression)
		for _, assign := range assigns {
			mem, ok := assign.Left.(*ast.DotExpression)
			if !ok {
				continue
			}

			propName := mem.Identifier.Name.String()

			// Check left-hand side container properties: h.name = "..." or h.internalSpec = { ... }
			if lhsIdent, ok := mem.Left.(*ast.Identifier); ok {
				varName := lhsIdent.Name.String()
				switch propName {
				case "name":
					if strLit, ok := assign.Right.(*ast.StringLiteral); ok {
						containerNames[varName] = strLit.Value.String()
						specContainers[varName] = true
					}
					continue
				case "internalSpec":
					if obj, ok := assign.Right.(*ast.ObjectLiteral); ok {
						containerSpecs[varName] = obj
						specContainers[varName] = true
					}
					continue
				}
			}

			// Left side is module export (e.g. l.XxxSpec = ... or f.XxxSpec = ...)
			switch rhs := assign.Right.(type) {
			case *ast.Identifier:
				rhsName := rhs.Name.String()

				// NEW format: l.MessageNameSpec = h
				if strings.HasSuffix(propName, "Spec") && specContainers[rhsName] {
					msgName := containerNames[rhsName]
					if msgName == "" {
						msgName = strings.TrimSuffix(propName, "Spec")
					}
					if specObj, ok := containerSpecs[rhsName]; ok {
						msgDef := parseMessageSpecObject(msgName, specObj, enums, containerNames)
						if msgDef != nil {
							messages = append(messages, msgDef)
						}
					}
					continue
				}

				// Enum export: f.EnumName = localVar or l.EnumName = localVar
				if enumDef, ok := enums[rhsName]; ok {
					enumDef.Name = propName
					exportedEnums = append(exportedEnums, enumDef)
					continue
				}

			case *ast.ObjectLiteral:
				// OLD format: f.MessageNameSpec = { ... }
				if strings.HasSuffix(propName, "Spec") {
					msgName := strings.TrimSuffix(propName, "Spec")
					msgDef := parseMessageSpecObject(msgName, rhs, enums, containerNames)
					if msgDef != nil {
						messages = append(messages, msgDef)
					}
					continue
				}

				// OLD format enum: f.EnumName = { ... } (inline enum)
				if !strings.HasSuffix(propName, "Spec") {
					if enumDef := parseEnumObject(propName, rhs); enumDef != nil {
						exportedEnums = append(exportedEnums, enumDef)
						continue
					}
				}
			}
		}
	}

	// Capture any containerSpecs with names that were not explicitly exported on module object
	for varName, specObj := range containerSpecs {
		msgName := containerNames[varName]
		if msgName == "" {
			continue
		}
		found := false
		for _, m := range messages {
			if m.Name == msgName {
				found = true
				break
			}
		}
		if !found {
			msgDef := parseMessageSpecObject(msgName, specObj, enums, containerNames)
			if msgDef != nil {
				messages = append(messages, msgDef)
			}
		}
	}

	if len(messages) > 0 || len(exportedEnums) > 0 {
		mod := e.Modules[moduleName]
		if mod == nil {
			mod = &ModuleSchema{
				Name:    moduleName,
				Version: e.Version,
			}
			e.Modules[moduleName] = mod
		}
		mod.Messages = append(mod.Messages, messages...)
		mod.Enums = append(mod.Enums, exportedEnums...)
	}
}

// collectAssignments recursively gathers all AssignExpression nodes from an expression,
// properly unpacking SequenceExpressions (comma operator) and chained assignments.
func collectAssignments(expr ast.Expression) []*ast.AssignExpression {
	var list []*ast.AssignExpression
	var walk func(e ast.Expression)
	walk = func(e ast.Expression) {
		if e == nil {
			return
		}
		switch node := e.(type) {
		case *ast.AssignExpression:
			list = append(list, node)
			walk(node.Right)
			walk(node.Left)
		case *ast.SequenceExpression:
			for _, elem := range node.Sequence {
				walk(elem)
			}
		case *ast.CallExpression:
			walk(node.Callee)
			for _, arg := range node.ArgumentList {
				walk(arg)
			}
		}
	}
	walk(expr)
	return list
}

// parseInternalEnumCall parses enum definitions from function calls like $InternalEnum({KEY: 0, ...}).
func parseInternalEnumCall(name string, call *ast.CallExpression) *protoAst.EnumDef {
	if len(call.ArgumentList) != 1 {
		return nil
	}
	objArg, ok := call.ArgumentList[0].(*ast.ObjectLiteral)
	if !ok {
		return nil
	}
	return parseEnumObject(name, objArg)
}

var (
	validIdentifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	validProtoTypeRe  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`)
)

func parseEnumObject(name string, obj *ast.ObjectLiteral) *protoAst.EnumDef {
	if name == "" {
		return nil
	}
	var values []*protoAst.EnumValueDef
	seenKeys := make(map[string]bool)
	for _, prop := range obj.Value {
		if pk, ok := prop.(*ast.PropertyKeyed); ok {
			key := getPropKey(pk.Key)
			if !validIdentifierRe.MatchString(key) || seenKeys[key] {
				return nil
			}
			num, ok := getNumber(pk.Value)
			if !ok || num < -2147483648 || num > 2147483647 {
				return nil
			}
			seenKeys[key] = true
			values = append(values, &protoAst.EnumValueDef{
				Name: key,
				ID:   num,
			})
		} else {
			return nil
		}
	}
	if len(values) == 0 {
		return nil
	}
	return &protoAst.EnumDef{
		Name:   name,
		Values: values,
	}
}

func parseMessageSpecObject(msgName string, obj *ast.ObjectLiteral, localEnums map[string]*protoAst.EnumDef, containerNames map[string]string) *protoAst.MessageDef {
	msgDef := &protoAst.MessageDef{
		Name: msgName,
	}

	oneofsMap := make(map[string][]string)

	for _, prop := range obj.Value {
		pk, ok := prop.(*ast.PropertyKeyed)
		if !ok {
			continue
		}
		key := getPropKey(pk.Key)

		// Parse __oneofs__ constraint
		if key == "__oneofs__" {
			if oObj, ok := pk.Value.(*ast.ObjectLiteral); ok {
				for _, op := range oObj.Value {
					if opk, ok := op.(*ast.PropertyKeyed); ok {
						oneofName := getPropKey(opk.Key)
						if arr, ok := opk.Value.(*ast.ArrayLiteral); ok {
							for _, el := range arr.Value {
								if sLit, ok := el.(*ast.StringLiteral); ok {
									oneofsMap[oneofName] = append(oneofsMap[oneofName], sLit.Value.String())
								}
							}
						}
					}
				}
			}
			continue
		}

		if strings.HasPrefix(key, "__") {
			continue
		}

		arr, ok := pk.Value.(*ast.ArrayLiteral)
		if !ok || len(arr.Value) < 2 {
			continue
		}

		id, ok := getNumber(arr.Value[0])
		if !ok {
			continue
		}

		fieldType, rule, packed, isMap, mapKey, mapVal := parseTypeAndFlags(arr.Value[1], arr.Value, localEnums, containerNames)

		msgDef.Fields = append(msgDef.Fields, &protoAst.FieldDef{
			Name:     key,
			ID:       id,
			Type:     fieldType,
			Rule:     rule,
			Packed:   packed,
			IsMap:    isMap,
			MapKey:   mapKey,
			MapValue: mapVal,
		})
	}

	// Assemble oneofs
	if len(oneofsMap) > 0 {
		fieldMap := make(map[string]*protoAst.FieldDef)
		for _, f := range msgDef.Fields {
			fieldMap[f.Name] = f
		}

		var remainingFields []*protoAst.FieldDef
		inOneof := make(map[string]bool)

		for oName, fNames := range oneofsMap {
			oDef := &protoAst.OneofDef{Name: oName}
			for _, fn := range fNames {
				if f, ok := fieldMap[fn]; ok {
					oDef.Fields = append(oDef.Fields, f)
					inOneof[fn] = true
				}
			}
			msgDef.Oneofs = append(msgDef.Oneofs, oDef)
		}

		for _, f := range msgDef.Fields {
			if !inOneof[f.Name] {
				remainingFields = append(remainingFields, f)
			}
		}
		msgDef.Fields = remainingFields
	}

	return msgDef
}

func parseTypeAndFlags(typeExpr ast.Expression, allElements []ast.Expression, localEnums map[string]*protoAst.EnumDef, containerNames map[string]string) (fieldType, rule string, packed, isMap bool, mapKey, mapVal string) {
	rule = "optional"
	var parts []ast.Expression
	unwrapBinaryOr(typeExpr, &parts)

	var detectedType string
	for _, p := range parts {
		if dot, ok := p.(*ast.DotExpression); ok {
			memName := dot.Identifier.Name.String()
			if memObj, ok := dot.Left.(*ast.DotExpression); ok {
				category := memObj.Identifier.Name.String()
				if category == "FLAGS" {
					switch strings.ToUpper(memName) {
					case "REPEATED":
						rule = "repeated"
					case "PACKED":
						packed = true
					}
				} else if category == "TYPES" {
					detectedType = strings.ToLower(memName)
				}
			} else {
				if strings.Contains(strings.ToUpper(memName), "PACKED") {
					packed = true
				}
				if strings.Contains(strings.ToUpper(memName), "REPEATED") {
					rule = "repeated"
				}
			}
		}
	}

	switch detectedType {
	case "map":
		isMap = true
		if len(allElements) > 2 {
			if arr, ok := allElements[2].(*ast.ArrayLiteral); ok && len(arr.Value) >= 2 {
				mapKey = resolveTypeName(arr.Value[0], localEnums, containerNames)
				mapVal = resolveTypeName(arr.Value[1], localEnums, containerNames)
			}
		}
		fieldType = fmt.Sprintf("map<%s, %s>", mapKey, mapVal)
		return

	case "enum", "message":
		if len(allElements) > 2 {
			target := resolveTypeName(allElements[2], localEnums, containerNames)
			if target != "" {
				fieldType = target
				return
			}
		}
		fieldType = "bytes"
		return

	case "":
		fieldType = "string"
		return

	default:
		fieldType = detectedType
		return
	}
}

func resolveTypeName(expr ast.Expression, localEnums map[string]*protoAst.EnumDef, containerNames map[string]string) string {
	switch e := expr.(type) {
	case *ast.Identifier:
		name := e.Name.String()
		if cName, ok := containerNames[name]; ok && cName != "" {
			return cleanTypeName(cName)
		}
		if enumDef, ok := localEnums[name]; ok && enumDef.Name != "" {
			return cleanTypeName(enumDef.Name)
		}
		return cleanTypeName(name)
	case *ast.DotExpression:
		name := e.Identifier.Name.String()
		return cleanTypeName(name)
	case *ast.StringLiteral:
		return cleanTypeName(e.Value.String())
	default:
		return ""
	}
}

func cleanTypeName(name string) string {
	name = strings.TrimSuffix(name, "Spec")
	name = strings.ReplaceAll(name, "$", ".")
	if catalog.IsScalarType(name) {
		return strings.ToLower(name)
	}
	if !validProtoTypeRe.MatchString(name) {
		return ""
	}
	return name
}

func unnestName(name string) string {
	idx := strings.LastIndex(name, "$")
	if idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func unwrapBinaryOr(expr ast.Expression, acc *[]ast.Expression) {
	if bin, ok := expr.(*ast.BinaryExpression); ok && bin.Operator == token.OR {
		unwrapBinaryOr(bin.Left, acc)
		unwrapBinaryOr(bin.Right, acc)
	} else if expr != nil {
		*acc = append(*acc, expr)
	}
}

func getCallExpr(stmt ast.Statement) (*ast.CallExpression, bool) {
	if exprStmt, ok := stmt.(*ast.ExpressionStatement); ok {
		if call, ok := exprStmt.Expression.(*ast.CallExpression); ok {
			return call, true
		}
	}
	return nil, false
}

func getNumber(expr ast.Expression) (int, bool) {
	switch e := expr.(type) {
	case *ast.NumberLiteral:
		switch v := e.Value.(type) {
		case int:
			return v, true
		case int64:
			return int(v), true
		case float64:
			return int(v), true
		}
	case *ast.UnaryExpression:
		if e.Operator == token.MINUS {
			if n, ok := getNumber(e.Operand); ok {
				return -n, true
			}
		}
	}
	return 0, false
}

func getPropKey(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.Identifier:
		return e.Name.String()
	case *ast.StringLiteral:
		return e.Value.String()
	default:
		return ""
	}
}

// OrganizeHierarchy groups extracted messages and enums that have '$' in their names
// into a proper nested hierarchy under their respective parent messages.
func OrganizeHierarchy(messages []*protoAst.MessageDef, enums []*protoAst.EnumDef) ([]*protoAst.MessageDef, []*protoAst.EnumDef) {
	msgMap := make(map[string]*protoAst.MessageDef)
	for _, m := range messages {
		msgMap[m.Name] = m
	}

	// 1. Nest enums with $ into their parent message
	var topEnums []*protoAst.EnumDef
	for _, e := range enums {
		idx := strings.LastIndex(e.Name, "$")
		if idx >= 0 {
			parentPath := e.Name[:idx]
			childName := e.Name[idx+1:]
			if parentMsg, ok := msgMap[parentPath]; ok {
				e.Name = childName
				parentMsg.NestedEnums = append(parentMsg.NestedEnums, e)
				continue
			}
		}
		topEnums = append(topEnums, e)
	}

	// 2. Nest messages with $ into their parent message
	type msgWithDepth struct {
		m        *protoAst.MessageDef
		fullName string
		depth    int
	}
	var msgList []msgWithDepth
	for _, m := range messages {
		msgList = append(msgList, msgWithDepth{
			m:        m,
			fullName: m.Name,
			depth:    strings.Count(m.Name, "$"),
		})
	}
	sort.Slice(msgList, func(i, j int) bool {
		return msgList[i].depth > msgList[j].depth
	})

	nestedSet := make(map[string]bool)
	for _, item := range msgList {
		if item.depth == 0 {
			continue
		}
		idx := strings.LastIndex(item.fullName, "$")
		parentPath := item.fullName[:idx]
		childName := item.fullName[idx+1:]

		if parentMsg, ok := msgMap[parentPath]; ok {
			item.m.Name = childName
			parentMsg.NestedMessages = append(parentMsg.NestedMessages, item.m)
			nestedSet[item.fullName] = true
		}
	}

	for _, m := range messages {
		sort.Slice(m.NestedEnums, func(i, j int) bool {
			return m.NestedEnums[i].Name < m.NestedEnums[j].Name
		})
		sort.Slice(m.NestedMessages, func(i, j int) bool {
			return m.NestedMessages[i].Name < m.NestedMessages[j].Name
		})
	}

	var topMessages []*protoAst.MessageDef
	for _, item := range msgList {
		if !nestedSet[item.fullName] {
			topMessages = append(topMessages, item.m)
		}
	}

	sort.Slice(topMessages, func(i, j int) bool {
		return topMessages[i].Name < topMessages[j].Name
	})
	sort.Slice(topEnums, func(i, j int) bool {
		return topEnums[i].Name < topEnums[j].Name
	})

	return topMessages, topEnums
}
