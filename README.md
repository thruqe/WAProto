# WhatsApp Protocol Buffers

[![Build Status](https://github.com/thruqe/WAProto/actions/workflows/update-proto.yml/badge.svg)](https://github.com/Thruqe/wa-proto/actions)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Fetch and extract live WhatsApp Web protobuf definitions and generate both monolithic schemas (`WAProto.proto`) and modular package structures.

## Installation & Build

```bash
# Build binary
task build
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
wa-proto split -proto WAProto.proto -out ../whatsrook/wa-core/proto
```

#### 2. Fetch live JavaScript bundles and extract schema
```bash
wa-proto fetch -out WAProto.proto
```

#### 3. Compile `.proto` files to `.pb.go`
```bash
wa-proto compile -dir ../whatsrook/wa-core/proto
```

#### 4. Full end-to-end synchronization
```bash
wa-proto sync -proto WAProto.proto -out ../whatsrook/wa-core/proto -compile
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
