package generator

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Thruqe/wa-proto/pkg/ast"
	"github.com/Thruqe/wa-proto/pkg/catalog"
	"github.com/Thruqe/wa-proto/pkg/corrector"
	"github.com/Thruqe/wa-proto/pkg/parser"
)

// Report contains statistics about the generation process.
type Report struct {
	TotalPackages  int
	TotalMessages  int
	TotalEnums     int
	GeneratedFiles []string
}

// GenerateMonolithic generates a single monolithic WAProto.proto matching wppconnect wa-proto in proto2 syntax.
func GenerateMonolithic(schema *ast.ProtoSchema, w io.Writer) error {
	// Sort entities before output so disambiguation is completely deterministic
	sort.Slice(schema.Enums, func(i, j int) bool {
		return schema.Enums[i].Name < schema.Enums[j].Name
	})
	sort.Slice(schema.Messages, func(i, j int) bool {
		return schema.Messages[i].Name < schema.Messages[j].Name
	})

	// Ensure proto2 compliance (rules default to optional)
	corrector.FixProto2(schema)

	var b strings.Builder
	b.WriteString("syntax = \"proto2\";\n")
	b.WriteString("package waproto;\n\n")

	if schema.Version != "" {
		b.WriteString(fmt.Sprintf("/// WhatsApp Version: %s\n\n", schema.Version))
	}

	// Sort entities alphabetically for deterministic output
	type entity struct {
		name    string
		content string
	}
	var entities []entity

	for _, e := range schema.Enums {
		entities = append(entities, entity{name: e.Name, content: e.FormatProto2("")})
	}
	for _, m := range schema.Messages {
		entities = append(entities, entity{name: m.Name, content: m.FormatProto2("")})
	}

	sort.Slice(entities, func(i, j int) bool {
		return entities[i].name < entities[j].name
	})

	for _, ent := range entities {
		b.WriteString(ent.content)
		b.WriteString("\n\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// GenerateModular partitions the schema into the modular wa-core/proto packages.
func GenerateModular(schema *ast.ProtoSchema, outDir string) (*Report, error) {
	pkgMessages := make(map[string][]*ast.MessageDef)
	pkgEnums := make(map[string][]*ast.EnumDef)

	// Group enums into packages
	for _, e := range schema.Enums {
		pkg := catalog.LookupPackageForType(e.Name)
		pkgEnums[pkg] = append(pkgEnums[pkg], e)
	}

	// Group messages into packages
	for _, m := range schema.Messages {
		pkg := catalog.LookupPackageForType(m.Name)
		pkgMessages[pkg] = append(pkgMessages[pkg], m)
	}

	// Also ensure all registered catalog packages exist
	allPkgs := make(map[string]bool)
	for p := range catalog.Packages {
		allPkgs[p] = true
	}
	for p := range pkgMessages {
		allPkgs[p] = true
	}
	for p := range pkgEnums {
		allPkgs[p] = true
	}

	var pkgNames []string
	for p := range allPkgs {
		pkgNames = append(pkgNames, p)
	}
	sort.Strings(pkgNames)

	report := &Report{
		TotalPackages: len(pkgNames),
	}

	for _, pkgDir := range pkgNames {
		if pkgDir == "" {
			continue
		}
		meta, exists := catalog.Packages[pkgDir]
		if !exists {
			meta = catalog.PackageMeta{
				Dir:       pkgDir,
				File:      pkgDir + ".proto",
				Package:   pkgDir,
				GoPackage: "go.mau.fi/whatsmeow/proto/" + pkgDir,
			}
		}

		msgs := pkgMessages[pkgDir]
		enums := pkgEnums[pkgDir]

		// Skip empty packages if they have no messages or enums
		if len(msgs) == 0 && len(enums) == 0 {
			continue
		}

		targetDirPath := filepath.Join(outDir, pkgDir)
		if err := os.MkdirAll(targetDirPath, 0755); err != nil {
			return nil, fmt.Errorf("failed creating package dir %s: %w", targetDirPath, err)
		}

		targetFilePath := filepath.Join(targetDirPath, meta.File)

		// Calculate required imports based on baseline and referenced external types
		neededImports := make(map[string]bool)
		for _, imp := range meta.Imports {
			neededImports[imp] = true
		}

		// Merge with existing declarations if target file already exists on disk
		mergeWithExistingFile(targetFilePath, &msgs, &enums, neededImports, pkgDir)

		// Collect ONLY top-level types defined in this package
		topLevelTypes := make(map[string]bool)
		for _, m := range msgs {
			topLevelTypes[m.Name] = true
		}
		for _, e := range enums {
			topLevelTypes[e.Name] = true
		}

		// Qualify external types in all messages BEFORE calculating imports
		for _, m := range msgs {
			qualifyExternalTypes(m, pkgDir, topLevelTypes)
		}

		// Collect referenced types after qualification to compute needed imports
		referencedTypes := make(map[string]bool)
		for _, m := range msgs {
			collectMessageTypes(m, referencedTypes)
		}

		for t := range referencedTypes {
			cleanType := strings.TrimPrefix(t, ".")
			parts := strings.Split(cleanType, ".")
			rootType := parts[0]
			if catalog.IsScalarType(cleanType) || topLevelTypes[cleanType] || topLevelTypes[rootType] {
				continue
			}

			// Check if rootType is a known package name (e.g. "WACommon" from WACommon.MessageKey)
			var refPkg string
			for _, p := range catalog.Packages {
				if p.Package == rootType {
					refPkg = p.Dir
					break
				}
			}
			if refPkg == "" {
				refPkg = catalog.LookupPackageForType(rootType)
			}

			if refPkg != "" && refPkg != pkgDir {
				if refPkg == "waE2E" && pkgDir != "waHistorySync" && pkgDir != "waWeb" && pkgDir != "waGroupHistory" {
					continue
				}
				if pkgDir == "waE2E" && (refPkg == "waHistorySync" || refPkg == "waWeb" || refPkg == "waGroupHistory") {
					continue
				}
				if pkgDir == "waCompanionReg" && refPkg == "waHistorySync" {
					continue
				}
				if refMeta, ok := catalog.Packages[refPkg]; ok {
					impPath := refMeta.Dir + "/" + refMeta.File
					neededImports[impPath] = true
				}
			}
		}

		// Format the file
		var b strings.Builder
		b.WriteString("syntax = \"proto2\";\n")
		b.WriteString(fmt.Sprintf("package %s;\n", meta.Package))
		b.WriteString(fmt.Sprintf("option go_package = %q;\n", meta.GoPackage))

		if len(neededImports) > 0 {
			b.WriteString("\n")
			var imps []string
			for imp := range neededImports {
				imps = append(imps, imp)
			}
			sort.Strings(imps)
			for _, imp := range imps {
				b.WriteString(fmt.Sprintf("import %q;\n", imp))
			}
		}

		b.WriteString("\n")

		// Sort and write Enums
		sort.Slice(enums, func(i, j int) bool {
			return enums[i].Name < enums[j].Name
		})
		for _, e := range enums {
			b.WriteString(e.FormatProto2(""))
			b.WriteString("\n\n")
			report.TotalEnums++
		}

		// Sort and write Messages
		sort.Slice(msgs, func(i, j int) bool {
			return msgs[i].Name < msgs[j].Name
		})
		for _, m := range msgs {
			b.WriteString(m.FormatProto2(""))
			b.WriteString("\n\n")
			report.TotalMessages++
		}

		if err := os.WriteFile(targetFilePath, []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0644); err != nil {
			return nil, fmt.Errorf("failed writing %s: %w", targetFilePath, err)
		}

		report.GeneratedFiles = append(report.GeneratedFiles, targetFilePath)
	}

	return report, nil
}

func collectMessageTypes(m *ast.MessageDef, acc map[string]bool) {
	for _, f := range m.Fields {
		cleanType := strings.TrimPrefix(f.Type, ".")
		if !isScalarType(cleanType) {
			acc[cleanType] = true
		}
		if f.IsMap {
			if !isScalarType(f.MapKey) {
				acc[f.MapKey] = true
			}
			if !isScalarType(f.MapValue) {
				acc[f.MapValue] = true
			}
		}
	}
	for _, o := range m.Oneofs {
		for _, f := range o.Fields {
			cleanType := strings.TrimPrefix(f.Type, ".")
			if !isScalarType(cleanType) {
				acc[cleanType] = true
			}
		}
	}
	for _, nm := range m.NestedMessages {
		collectMessageTypes(nm, acc)
	}
}

func isScalarType(t string) bool {
	return catalog.IsScalarType(t)
}

func qualifyExternalTypes(m *ast.MessageDef, currentPkg string, scopeTypes map[string]bool) {
	currentScope := make(map[string]bool)
	for k, v := range scopeTypes {
		currentScope[k] = v
	}
	for _, ne := range m.NestedEnums {
		currentScope[ne.Name] = true
	}
	for _, nm := range m.NestedMessages {
		currentScope[nm.Name] = true
	}

	for _, f := range m.Fields {
		qualifyField(f, currentPkg, currentScope)
	}
	for _, o := range m.Oneofs {
		for _, f := range o.Fields {
			qualifyField(f, currentPkg, currentScope)
		}
	}
	for _, nm := range m.NestedMessages {
		qualifyExternalTypes(nm, currentPkg, currentScope)
	}
}

func qualifyField(f *ast.FieldDef, currentPkg string, localTypes map[string]bool) {
	if f.IsMap {
		f.MapKey = qualifySingleType(f.MapKey, currentPkg, localTypes)
		f.MapValue = qualifySingleType(f.MapValue, currentPkg, localTypes)
		return
	}
	f.Type = qualifySingleType(f.Type, currentPkg, localTypes)
}

func qualifySingleType(t string, currentPkg string, localTypes map[string]bool) string {
	cleanType := strings.TrimPrefix(t, ".")
	if catalog.IsScalarType(cleanType) || localTypes[cleanType] {
		return t
	}

	parts := strings.Split(cleanType, ".")
	rootType := parts[0]

	// Check if rootType is already a known package name
	for _, pkg := range catalog.Packages {
		if pkg.Package == rootType {
			return t
		}
	}

	// Check if rootType is defined locally in this scope
	if localTypes[rootType] {
		return t
	}

	refPkg := catalog.LookupPackageForType(rootType)
	if refPkg != "" && refPkg != currentPkg {
		if currentPkg == "waE2E" && (refPkg == "waHistorySync" || refPkg == "waWeb" || refPkg == "waGroupHistory") {
			return t
		}
		if currentPkg == "waCompanionReg" && refPkg == "waHistorySync" {
			return t
		}
		if refMeta, ok := catalog.Packages[refPkg]; ok {
			return refMeta.Package + "." + cleanType
		}
	}
	return t
}

var impRegex = regexp.MustCompile(`^import\s+"([^"]+)";`)

func mergeWithExistingFile(filePath string, msgs *[]*ast.MessageDef, enums *[]*ast.EnumDef, neededImports map[string]bool, currentPkg string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := impRegex.FindStringSubmatch(line); len(m) > 1 {
			neededImports[m[1]] = true
		}
	}

	existingSchema, err := parser.ParseProto(bytes.NewReader(data))
	if err != nil {
		return
	}

	// 1. Merge enums: preserve existing enum constant names, append new values
	existingEnumMap := make(map[string]*ast.EnumDef)
	for _, ee := range existingSchema.Enums {
		existingEnumMap[ee.Name] = ee
	}
	var mergedEnums []*ast.EnumDef
	for _, ee := range existingSchema.Enums {
		mergedEnums = append(mergedEnums, ee)
	}
	for _, ie := range *enums {
		if exE, exists := existingEnumMap[ie.Name]; exists {
			mergeEnumValues(exE, ie)
		} else {
			mergedEnums = append(mergedEnums, ie)
			existingEnumMap[ie.Name] = ie
		}
	}
	*enums = mergedEnums

	// 2. Merge messages: preserve existing field names and casing, append new fields
	existingMsgMap := make(map[string]*ast.MessageDef)
	for _, em := range existingSchema.Messages {
		existingMsgMap[em.Name] = em
	}
	var mergedMsgs []*ast.MessageDef
	for _, em := range existingSchema.Messages {
		mergedMsgs = append(mergedMsgs, em)
	}
	for _, im := range *msgs {
		if exM, exists := existingMsgMap[im.Name]; exists {
			mergeMessage(exM, im, existingMsgMap, currentPkg)
		} else {
			mergedMsgs = append(mergedMsgs, im)
			existingMsgMap[im.Name] = im
		}
	}
	*msgs = mergedMsgs
}

func mergeMessage(existing *ast.MessageDef, incoming *ast.MessageDef, topLevelMsgs map[string]*ast.MessageDef, currentPkg string) {
	// Track all field IDs used in existing message (both normal fields and oneof fields)
	usedFieldIDs := make(map[int]bool)
	for _, f := range existing.Fields {
		usedFieldIDs[f.ID] = true
	}
	for _, o := range existing.Oneofs {
		for _, f := range o.Fields {
			usedFieldIDs[f.ID] = true
		}
	}

	// 1. Merge regular fields
	for _, inf := range incoming.Fields {
		if !usedFieldIDs[inf.ID] {
			existing.Fields = append(existing.Fields, inf)
			usedFieldIDs[inf.ID] = true
		}
	}

	// 2. Merge Oneofs (with case-insensitive name matching and field ID deduplication)
	for _, ino := range incoming.Oneofs {
		var matchedOneof *ast.OneofDef
		for _, exO := range existing.Oneofs {
			if strings.EqualFold(exO.Name, ino.Name) {
				matchedOneof = exO
				break
			}
		}

		if matchedOneof != nil {
			for _, inF := range ino.Fields {
				if !usedFieldIDs[inF.ID] {
					matchedOneof.Fields = append(matchedOneof.Fields, inF)
					usedFieldIDs[inF.ID] = true
				}
			}
		} else {
			var newFields []*ast.FieldDef
			for _, inF := range ino.Fields {
				if !usedFieldIDs[inF.ID] {
					newFields = append(newFields, inF)
				}
			}
			if len(newFields) > 0 {
				ino.Fields = newFields
				for _, inF := range newFields {
					usedFieldIDs[inF.ID] = true
				}
				existing.Oneofs = append(existing.Oneofs, ino)
			}
		}
	}

	// 3. Nested Enums
	enumByName := make(map[string]*ast.EnumDef)
	existingEnumValues := make(map[string]bool)
	for _, e := range existing.NestedEnums {
		enumByName[e.Name] = e
		for _, v := range e.Values {
			existingEnumValues[v.Name] = true
		}
	}
	for _, ine := range incoming.NestedEnums {
		if exE, exists := enumByName[ine.Name]; exists {
			mergeEnumValues(exE, ine)
		} else {
			clash := false
			for _, v := range ine.Values {
				if existingEnumValues[v.Name] {
					clash = true
					break
				}
			}
			if !clash {
				existing.NestedEnums = append(existing.NestedEnums, ine)
				enumByName[ine.Name] = ine
				for _, v := range ine.Values {
					existingEnumValues[v.Name] = true
				}
			}
		}
	}

	// 4. Nested Messages
	nestedMsgByName := make(map[string]*ast.MessageDef)
	for _, nm := range existing.NestedMessages {
		nestedMsgByName[nm.Name] = nm
	}
	for _, inm := range incoming.NestedMessages {
		// If inm is already a top-level message in this package or catalog, merge into top-level and don't nest!
		if topLevelM, exists := topLevelMsgs[inm.Name]; exists {
			mergeMessage(topLevelM, inm, topLevelMsgs, currentPkg)
			continue
		}
		if catalog.LookupPackageForType(inm.Name) == currentPkg {
			continue
		}
		if exNm, exists := nestedMsgByName[inm.Name]; exists {
			mergeMessage(exNm, inm, topLevelMsgs, currentPkg)
		} else {
			existing.NestedMessages = append(existing.NestedMessages, inm)
			nestedMsgByName[inm.Name] = inm
		}
	}
}

func mergeEnumValues(existing *ast.EnumDef, incoming *ast.EnumDef) {
	valByID := make(map[int]bool)
	for _, v := range existing.Values {
		valByID[v.ID] = true
	}
	for _, inv := range incoming.Values {
		if !valByID[inv.ID] {
			existing.Values = append(existing.Values, inv)
			valByID[inv.ID] = true
		}
	}
}

// UpdateClientPayloadVersion updates the waVersion variable in clientpayload.go.
func UpdateClientPayloadVersion(filePath string, fullVersion string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	parts := strings.Split(fullVersion, ".")
	if len(parts) < 3 {
		return fmt.Errorf("invalid version string format: %s (expected e.g. 2.3000.1046738589)", fullVersion)
	}

	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	build, _ := strconv.Atoi(parts[2])

	re := regexp.MustCompile(`var waVersion = WAVersionContainer\{\d+,\s*\d+,\s*\d+\}`)
	newDeclaration := fmt.Sprintf("var waVersion = WAVersionContainer{%d, %d, %d}", major, minor, build)

	if !re.Match(content) {
		return fmt.Errorf("waVersion declaration not found in %s", filePath)
	}

	updated := re.ReplaceAll(content, []byte(newDeclaration))
	return os.WriteFile(filePath, updated, 0644)
}
