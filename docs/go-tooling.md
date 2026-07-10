# Go Tooling Hooks Documentation

System-level Go tooling hooks that delegate to existing Make targets in HyperFleet repositories.

## Table of Contents

- [Overview](#overview)
- [Hooks](#hooks)
- [Configuration](#configuration)
- [Prerequisites](#prerequisites)
- [Troubleshooting](#troubleshooting)

## Overview

HyperFleet Go repositories pin development tools in `tools/go.mod` using Go 1.24+ [tool directives](https://go.dev/doc/modules/managing-dependencies#tools) (see [dependency pinning standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/dependency-pinning.md)). These hooks use `language: system` and delegate to the consuming repo's existing Make targets, which invoke tools via `go tool -modfile=tools/go.mod <name>`.

## Hooks

### hyperfleet-golangci-lint

Runs `make lint` in the consuming repository.

- **Stage**: `pre-commit`
- **Entry**: `make lint`
- **Language**: `system`
- **File filtering**: Triggered by Go file changes (does not pass individual filenames)

### hyperfleet-gofmt

Runs `make gofmt` in the consuming repository.

- **Stage**: `pre-commit`
- **Entry**: `make gofmt`
- **Language**: `system`
- **File filtering**: Triggered by Go file changes (does not pass individual filenames)

### hyperfleet-go-vet

Runs `make go-vet` in the consuming repository.

- **Stage**: `pre-commit`
- **Entry**: `make go-vet`
- **Language**: `system`
- **File filtering**: Triggered by Go file changes (does not pass individual filenames)

## Configuration

### Basic Setup

Add to your `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/openshift-hyperfleet/hyperfleet-hooks
    rev: main # Use latest release tag
    hooks:
      - id: hyperfleet-golangci-lint
      - id: hyperfleet-gofmt
      - id: hyperfleet-go-vet
```

Install the hooks:

```bash
pre-commit install --hook-type pre-commit
```

### Selective Hooks

You don't need to enable all hooks. Pick the ones you need:

```yaml
hooks:
  # Just formatting and linting
  - id: hyperfleet-gofmt
  - id: hyperfleet-golangci-lint
```

## Prerequisites

Since these hooks use `language: system`, the consuming repository **must** have the corresponding Make targets:

- `make lint` - runs golangci-lint (via tools/go.mod)
- `make gofmt` — checks Go file formatting
- `make go-vet` — runs `go vet ./...`

These targets are already standard in HyperFleet repositories. Ensure tools are available:

```bash
make tools
```

## Troubleshooting

### make: *** No rule to make target 'lint'

The consuming repository is missing the required Make target. Ensure your Makefile includes the `lint`, `gofmt`, and `go-vet` targets.

### golangci-lint binary not found

If `make lint` fails because golangci-lint is not installed, tidy the tools module:

```bash
make tools
```

### Different results from direct make lint

These hooks run the exact same Make targets, so results should be identical. If they differ, check:

1. The tool module is tidy: `make verify-tools`
2. The `.golangci.yml` config is present in your repo root
3. Pre-commit environment is not interfering (check `pre-commit run --verbose`)

### go vet fails but code compiles

`go vet` catches issues that compile successfully but are likely bugs (e.g., unreachable code, incorrect format strings). Fix the reported issues rather than skipping the hook.
