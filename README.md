# WA-Proto (Golang)

[![Build Status](https://github.com/Thruqe/wa-proto/actions/workflows/update-proto.yml/badge.svg)](https://github.com/Thruqe/wa-proto/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/Thruqe/wa-proto)](https://goreportcard.com/report/github.com/Thruqe/wa-proto)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Pure Go Protocol Buffer (`.proto`) extractor and generator for WhatsApp Web (2.3000.x series).

This project enables Go developers to extract live WhatsApp Web protobuf definitions and generate both monolithic schemas (`WAProto.proto`) and modular package structures (for `whatsmeow` / `wa-core`).

## Features

- **100% Pure Go**: Zero Node.js, Puppeteer, or JavaScript dependencies.
- **Direct Web Extraction**: Connects to `web.whatsapp.com`, identifies bundles, and parses JavaScript AST directly using pure Go (`goja/parser`).
- **Dual Output Modes**:
  - **Monolithic (`proto3`)**: Emits unified `WAProto.proto` under `package waproto;`.
  - **Modular (`proto2`)**: Automatically partitions definitions into 57+ modular packages matching `wa-core/proto/*/*.proto` (e.g. `waE2E`, `waAdv`, `waAICommon`, `instamadilloAddMessage`, etc.) with automated cross-package import resolution.
- **Client Payload Synchronization**: Automatically updates WhatsApp Web client revision numbers in `wa-core/store/clientpayload.go`.
- **Integrated Compiler**: Can automatically invoke `protoc` + `protoc-gen-go` to produce compiled `.pb.go` bindings.
- **Automated GitHub Actions**: Native Go workflows for scheduled updates and releases.

## Installation & Build

```bash
# Build binary
task build
# or: go build -o bin/wa-proto .
```

## CLI Commands

```text
Usage:
  wa-proto <command> [flags]

Commands:
  split      Split monolithic WAProto.proto into modular wa-core/proto packages
  fetch      Fetch WhatsApp Web scripts and discover bundle URLs
  generate   Generate protobuf files (monolithic or modular wa-core structure)
  compile    Compile .proto files to .pb.go using protoc
  sync       Full end-to-end: split WAProto.proto -> update clientpayload -> compile .pb.go
  version    Show version info
```

### Examples

#### 1. Split monolithic `WAProto.proto` into `wa-core/proto`
```bash
./bin/wa-proto split -proto WAProto.proto -out ../whatsrook/wa-core/proto
```

#### 2. Fetch live JavaScript bundles and extract schema
```bash
./bin/wa-proto fetch -out WAProto.proto
```

#### 3. Compile `.proto` files to `.pb.go`
```bash
./bin/wa-proto compile -dir ../whatsrook/wa-core/proto
```

#### 4. Full end-to-end synchronization
```bash
./bin/wa-proto sync -proto WAProto.proto -out ../whatsrook/wa-core/proto -compile
```

## Package Layout

- `pkg/ast`: Strongly typed AST representing messages, enums, oneofs, nested types, and field rules.
- `pkg/parser`: High-performance recursive descent `.proto` parser.
- `pkg/extractor`: Pure-Go AST analyzer for WhatsApp Web JavaScript module specs (`internalSpec`).
- `pkg/catalog`: Catalog of all 57 standard modular packages in `wa-core/proto`.
- `pkg/fetcher`: HTTP client and script bundle discovery engine.
- `pkg/generator`: Monolithic and modular proto generator with automated import resolution.
- `pkg/compiler`: Protoc compilation orchestrator.

## License

Licensed under the Apache License, Version 2.0.
