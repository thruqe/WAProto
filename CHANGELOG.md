# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-09-04

### Added
- Pure Go implementation of WhatsApp Web protobuf extractor and generator.
- Multi-format output:
  - Monolithic `WAProto.proto` (`proto3` syntax with strict proto3 compliance).
  - Modular `wa-core/proto` package layout (57 modular packages with automated cross-package import resolution).
- Pure Go AST extractor using ECMAScript parser for live JavaScript bundles.
- Proto3 syntax corrector:
  - Automatic replacement of `required` fields with `optional`.
  - Automatic enforcement of zero-value (`0`) first enum constants.
  - Package-scoped enum collision disambiguation.
- Synchronizer for `wa-core/store/clientpayload.go` version containers.
- GitHub Actions CI/CD workflows powered by latest stable Go.
