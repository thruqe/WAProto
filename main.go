package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Thruqe/wa-proto/pkg/ast"
	"github.com/Thruqe/wa-proto/pkg/compiler"
	"github.com/Thruqe/wa-proto/pkg/corrector"
	"github.com/Thruqe/wa-proto/pkg/extractor"
	"github.com/Thruqe/wa-proto/pkg/fetcher"
	"github.com/Thruqe/wa-proto/pkg/generator"
	"github.com/Thruqe/wa-proto/pkg/parser"
)

const version = "1.0.0"

func printUsage() {
	fmt.Printf(`WhatsApp Web Protobuf Generator & Extractor (Golang) v%s

Usage:
  wa-proto <command> [flags]

Commands:
  split      Split monolithic WAProto.proto into modular wa-core/proto packages
  fetch      Fetch WhatsApp Web scripts and discover bundle URLs
  generate   Generate protobuf files (monolithic or modular wa-core structure)
  fix        Ensure proto2 compliance (rules default to optional)
  compile    Compile .proto files to .pb.go using protoc
  sync       Full end-to-end: split WAProto.proto -> update clientpayload -> compile .pb.go
  version    Show version info

Run 'wa-proto <command> -h' for command-specific flags.
`, version)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "split":
		cmdSplit(os.Args[2:])
	case "fetch":
		cmdFetch(os.Args[2:])
	case "generate":
		cmdGenerate(os.Args[2:])
	case "fix":
		cmdFix(os.Args[2:])
	case "compile":
		cmdCompile(os.Args[2:])
	case "sync":
		cmdSync(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("wa-proto v%s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func cmdSplit(args []string) {
	fs := flag.NewFlagSet("split", flag.ExitOnError)
	protoPath := fs.String("proto", "WAProto.proto", "Path to input monolithic WAProto.proto")
	outDir := fs.String("out", "../whatsrook/wa-core/proto", "Target directory for modular wa-core protos")
	clientPayload := fs.String("clientpayload", "../whatsrook/wa-core/store/clientpayload.go", "Path to clientpayload.go to update version")
	_ = fs.Parse(args)

	f, err := os.Open(*protoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to open %s: %v\n", *protoPath, err)
		os.Exit(1)
	}
	defer f.Close()

	fmt.Printf("📖 Parsing %s...\n", *protoPath)
	schema, err := parser.ParseProto(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed parsing proto: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Parsed %d messages and %d enums (WhatsApp Version: %s)\n",
		len(schema.Messages), len(schema.Enums), schema.Version)

	fmt.Printf("🔨 Generating modular wa-core protos in %s...\n", *outDir)
	report, err := generator.GenerateModular(schema, *outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Generation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Generated %d proto packages (%d messages, %d enums) across %d files\n",
		report.TotalPackages, report.TotalMessages, report.TotalEnums, len(report.GeneratedFiles))

	if *clientPayload != "" && schema.Version != "" {
		if _, err := os.Stat(*clientPayload); err == nil {
			if err := generator.UpdateClientPayloadVersion(*clientPayload, schema.Version); err != nil {
				fmt.Printf("⚠️ Warning updating clientpayload: %v\n", err)
			} else {
				fmt.Printf("✓ Updated %s with version %s\n", *clientPayload, schema.Version)
			}
		}
	}
}

func cmdFetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	timeout := fs.Duration("timeout", 2*time.Minute, "HTTP fetch timeout")
	outProto := fs.String("out", "WAProto.proto", "Output path for extracted WAProto.proto")
	modularOut := fs.String("modular-out", "", "Optional directory to output modular wa-core protos")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Println("🌐 Connecting to WhatsApp Web to discover bundles...")
	res, err := fetcher.FetchBundles(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Fetch failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Downloaded %d bundles (WhatsApp Web Version: %s)\n", len(res.Sources), res.Version)
	fmt.Println("🔍 Extracting protobuf definitions from JavaScript bundles...")
	ext := extractor.NewExtractor(res.Version)
	for i, src := range res.Sources {
		_ = ext.ProcessSource(src)
		if (i+1)%50 == 0 || i+1 == len(res.Sources) {
			fmt.Printf("  Parsed %d/%d bundles\n", i+1, len(res.Sources))
		}
	}

	// Merge and deduplicate all modules into a unified ProtoSchema
	var modNames []string
	for name := range ext.Modules {
		modNames = append(modNames, name)
	}
	sort.Strings(modNames)

	var rawMessages []*ast.MessageDef
	var rawEnums []*ast.EnumDef
	for _, name := range modNames {
		mod := ext.Modules[name]
		rawMessages = append(rawMessages, mod.Messages...)
		rawEnums = append(rawEnums, mod.Enums...)
	}

	msgMap := make(map[string]*ast.MessageDef)
	for _, m := range rawMessages {
		if _, ok := msgMap[m.Name]; !ok {
			msgMap[m.Name] = m
		}
	}
	var dedupMessages []*ast.MessageDef
	for _, m := range msgMap {
		dedupMessages = append(dedupMessages, m)
	}
	sort.Slice(dedupMessages, func(i, j int) bool {
		return dedupMessages[i].Name < dedupMessages[j].Name
	})

	enumMap := make(map[string]*ast.EnumDef)
	for _, e := range rawEnums {
		if _, ok := enumMap[e.Name]; !ok {
			enumMap[e.Name] = e
		}
	}
	var dedupEnums []*ast.EnumDef
	for _, e := range enumMap {
		dedupEnums = append(dedupEnums, e)
	}
	sort.Slice(dedupEnums, func(i, j int) bool {
		return dedupEnums[i].Name < dedupEnums[j].Name
	})

	topMessages, topEnums := extractor.OrganizeHierarchy(dedupMessages, dedupEnums)

	schema := &ast.ProtoSchema{
		Syntax:   "proto2",
		Package:  "waproto",
		Version:  res.Version,
		Messages: topMessages,
		Enums:    topEnums,
	}

	if len(schema.Messages) >= 50 {
		fmt.Printf("✓ Extracted %d messages and %d enums from %d modules\n",
			len(schema.Messages), len(schema.Enums), len(ext.Modules))

		if *outProto != "" {
			f, err := os.Create(*outProto)
			if err == nil {
				_ = generator.GenerateMonolithic(schema, f)
				f.Close()
				fmt.Printf("✓ Updated %s\n", *outProto)
			}
		}

		if *modularOut != "" {
			rep, err := generator.GenerateModular(schema, *modularOut)
			if err == nil {
				fmt.Printf("✓ Generated %d modular packages in %s\n", rep.TotalPackages, *modularOut)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "⚠️ Warning: Live bundle extraction yielded only %d messages and %d enums (threshold >= 50). Existing schema preserved.\n", len(schema.Messages), len(schema.Enums))
		os.Exit(1)
	}
}

func cmdGenerate(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	input := fs.String("input", "WAProto.proto", "Input WAProto.proto file")
	out := fs.String("out", "WAProto.proto", "Output path")
	format := fs.String("format", "modular", "Output format: 'modular' (wa-core) or 'monolithic' (wppconnect)")
	_ = fs.Parse(args)

	f, err := os.Open(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to open %s: %v\n", *input, err)
		os.Exit(1)
	}
	defer f.Close()

	schema, err := parser.ParseProto(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed parsing proto: %v\n", err)
		os.Exit(1)
	}

	if *format == "monolithic" {
		outF, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed creating output file: %v\n", err)
			os.Exit(1)
		}
		defer outF.Close()

		if err := generator.GenerateMonolithic(schema, outF); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error generating monolithic proto: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Generated monolithic proto at %s\n", *out)
	} else {
		report, err := generator.GenerateModular(schema, *out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error generating modular protos: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Generated %d modular packages in %s\n", report.TotalPackages, *out)
	}
}

func cmdCompile(args []string) {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	dir := fs.String("dir", "../whatsrook/wa-core/proto", "Directory containing .proto files")
	filter := fs.String("filter", "", "Optional filter for package name")
	_ = fs.Parse(args)

	fmt.Printf("⚙️ Compiling .proto files in %s...\n", *dir)
	report, err := compiler.CompileProtos(*dir, *filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Compilation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Successfully compiled %d protobuf files to .pb.go\n", report.TotalCompiled)
	if len(report.FailedFiles) > 0 {
		fmt.Printf("⚠️ %d file(s) failed compilation: %v\n", len(report.FailedFiles), report.FailedFiles)
	}
}

func cmdSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	protoPath := fs.String("proto", "WAProto.proto", "Path to input WAProto.proto")
	outDir := fs.String("out", "../whatsrook/wa-core/proto", "Target directory for wa-core protos")
	clientPayload := fs.String("clientpayload", "../whatsrook/wa-core/store/clientpayload.go", "Path to clientpayload.go")
	compile := fs.Bool("compile", true, "Automatically run protoc to build .pb.go files")
	_ = fs.Parse(args)

	// Step 1: Parse and Split
	cmdSplit([]string{
		"-proto", *protoPath,
		"-out", *outDir,
		"-clientpayload", *clientPayload,
	})

	// Step 2: Compile
	if *compile {
		absOut, _ := filepath.Abs(*outDir)
		cmdCompile([]string{"-dir", absOut})
	}

	fmt.Println("🎉 Protobuf synchronization complete!")
}

func cmdFix(args []string) {
	fs := flag.NewFlagSet("fix", flag.ExitOnError)
	protoPath := fs.String("proto", "WAProto.proto", "Path to .proto file to correct for proto2 compliance")
	_ = fs.Parse(args)

	content, err := os.ReadFile(*protoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed reading %s: %v\n", *protoPath, err)
		os.Exit(1)
	}

	fmt.Printf("🔧 Ensuring proto2 compliance in %s...\n", *protoPath)
	fixed := corrector.FixProto2Content(string(content))

	if err := os.WriteFile(*protoPath, []byte(fixed), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed writing %s: %v\n", *protoPath, err)
		os.Exit(1)
	}

	fmt.Printf("✓ %s verified for proto2 compliance!\n", *protoPath)
}
