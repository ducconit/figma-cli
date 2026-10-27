# Agent Instructions

Open standard developer and AI coding agent guide for `figma-cli` (`fm`), compatible with OpenCode, OpenAI Codex, Google Antigravity, Claude Code, Cursor, Windsurf, Copilot, and Cline.

## Overview

`figma-cli` (`fm`) is a high-performance, zero-dependency, cross-platform CLI tool and universal AI Agent Skill written in Go (1.23+). It communicates with the Figma REST API to fetch and simplify design trees into token-optimized formats (Tree DSL, YAML, JSON), converting complex Figma layouts (Flexbox, CSS Grid, typography, rich text, alpha-blended fills, gradients, components) into clean web semantics, and downloads/crops SVG, PNG, and GIF assets via a streaming pure-Go pipeline.

## Build & Verification Commands

All build, test, and vet operations are managed via the standard Go toolchain and `Makefile`:

- **Install dependencies:** `go mod download`
- **Compile & build binary:** `make build` (compiles to `./fm` and mirrors to `bin/fm` with `CGO_ENABLED=0`)
- **Run dev / check CLI flags:** `go run ./cmd/fm --help` or `./fm --help`
- **Lint & static analysis:** `make vet` (runs `go vet ./...`)
- **Run all tests:** `make test` (runs `go test -v -race -count=1 ./...`)
- **Run focused / single test (Mandatory for targeted changes):**
  - Run a specific test function in a package:
    ```bash
    go test -v -race -count=1 -run ^TestParseFigmaURLAndResolveTarget$ ./internal/figma
    ```
  - Run all tests within a specific package:
    ```bash
    go test -v ./internal/extractors
    ```
  - Run a specific table-driven subtest:
    ```bash
    go test -v -run "TestVersionAndHelpCommands/version_flag" ./internal/cli
    ```
- **Install skill & script binaries:** `make install-skill` (installs `skills/figma-cli` and cross-compiles `scripts/fm` for Linux, `fm-darwin-arm64`/`amd64` for macOS, and `fm.exe` for Windows)
- **Cross-compile all platforms:** `make cross-build` (builds Linux, macOS, and Windows binaries into `dist/`)
- **Clean build artifacts:** `make clean`

## Architecture & Module Layout

```
cmd/
  └── fm/main.go                   # Binary entrypoint; invokes internal/cli.Execute()
internal/
  ├── cli/                         # Cobra CLI commands (fetch, download, install, set-token, version)
  ├── config/                      # 4-tier auth resolution, .env loading & ~/.figma/.env persistence
  ├── extractors/                  # Single-pass tree walking, AST simplification & finalizeDesign
  ├── figma/                       # HTTP client with connection pooling, rate limit backoff & redaction
  ├── images/                      # 2-stage streaming download/crop pipeline (pure-Go affine transform)
  ├── serialize/                   # Serializers (Tree DSL, YAML, JSON) & noise name stripper
  ├── skill/                       # Agent skill installer & multi-agent symlink creator
  ├── transformers/                # Figma-to-CSS mapping (Flexbox, Grid, gradients, rich text, fills)
  └── version/                     # Build metadata (Version, Commit, BuildDate) injected via ldflags
skills/
  └── figma-cli/                   # Embedded agent skill, documentation, references & scripts/fm
docs/                              # Detailed engineering specs & algorithmic deep-dives (01 to 06)
```

### Authentication Priority Chain (`internal/config`)

Authentication credentials resolve in strict priority:
1. CLI flags (`--token` or `--oauth-token`)
2. Current project `.env` (automatically discovers upwards to project root)
3. User global `~/.figma/.env` (saved via `fm set-token <token>`)
4. OS environment variables (`FIGMA_API_KEY` for PAT or `FIGMA_OAUTH_TOKEN` for OAuth Bearer)

## Code Conventions & Core Invariants

- **Zero External Runtime Dependencies**: Pure Go with standard library. External third-party libraries are strictly limited to CLI flags (`spf13/cobra`), env parsing (`joho/godotenv`), and YAML serialization (`gopkg.in/yaml.v3`).
- **Secret & Token Redaction**: Figma Personal Access Tokens and OAuth Bearer tokens MUST be redacted (`[REDACTED]`) from all error messages, HTTP status bodies (401/403/429), and logging outputs.
- **Path Security & Boundary Check**: All file outputs and download paths must be cleaned and guarded against path traversal vulnerabilities (`filepath.Clean`, `os.DirFS` checks) before disk operations.
- **Atomic File Downloads**: Asset downloads must write to temporary `.part` files first and perform an atomic rename upon completion to prevent corrupted or partial file states.
- **Strict `CGO_ENABLED=0`**: All builds and scripts must compile without CGO to preserve universal static linking and cross-platform compatibility across Linux, Darwin, and Windows.
- **Single-Pass Extraction & Determinism**: AST tree transformations in `internal/extractors` and `internal/transformers` must maintain deterministic output ordering (e.g. `finalizeDesign` deduplication and `EL-xxxxxxxx` identifiers).
- **Error Handling**: Return explicit typed errors wrapped with actionable context; avoid bare panics in library packages.
- **Testing**: Maintain high test coverage using Go standard table-driven test patterns and `httptest.Server` for mocking Figma REST API responses.

## Environment & Prerequisites

- **Go toolchain**: Go 1.23 or newer.
- **Figma API Credentials**:
  - Personal Access Token (PAT) set via `FIGMA_API_KEY` or `fm set-token <token>`.
  - Alternatively, OAuth Bearer token set via `FIGMA_OAUTH_TOKEN`.
- **Local Services**: No external database or daemon required; tests and builds are completely self-contained.
