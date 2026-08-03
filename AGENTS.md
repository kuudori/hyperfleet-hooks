# HyperFleet Hooks

Shared validation CLI and pre-commit hooks for all HyperFleet repositories. Validates commit messages and PR titles against HyperFleet Commit Standard (Conventional Commits with optional JIRA prefix). Consumed as a pre-commit hook and as a container image in Prow CI.

Go 1.26.0 · Cobra CLI · go-git · go-github · stretchr/testify · golangci-lint (pinned in tools/go.mod)

## Verification

| Target | What it runs |
|--------|-------------|
| `make build` | Build binary → `bin/hyperfleet-hooks` |
| `make build-all` | Cross-platform binaries (linux/darwin, amd64/arm64) |
| `make lint` | golangci-lint (pinned in tools/go.mod, config: `.golangci.yml`) |
| `make test` | Unit tests with race detection + coverage profile |
| `make test-coverage` | `make test` + opens HTML coverage report |
| `make validate-commits` | Build + `hyperfleet-hooks commitlint --pr` (CI mode) |
| `make image` | Build container image (podman/docker auto-detected) |

Pre-push order: `make lint` → `make test` → `make build`.

## CLI

```bash
hyperfleet-hooks commitlint <file>              # Validate single commit message from file
echo "feat: ..." | hyperfleet-hooks commitlint  # Validate from stdin
hyperfleet-hooks commitlint --pr                # CI mode: validate all PR commits + PR title
hyperfleet-hooks version                        # Print version info
```

## Source of Truth

| Topic | Location |
|-------|----------|
| Commitlint usage + Prow setup | `docs/commitlint.md` |
| Go tooling hooks (lint, fmt, vet) | `docs/go-tooling.md` |
| Pre-commit hook definitions | `.pre-commit-hooks.yaml` |
| Validation logic | `pkg/commitlint/validator.go` |
| GitHub API client | `pkg/github/client.go` |
| CLI command wiring | `cmd/hyperfleet-hooks/commitlint/commitlint.go` |
| Linter config | `.golangci.yml` |
| Container image | `Dockerfile` |
| Repo metadata | `.hyperfleet.yaml` |

## Architecture Context

Four pre-commit hooks defined in `.pre-commit-hooks.yaml`:
- `hyperfleet-commitlint` — validates commit messages (Go binary, `language: golang`)
- `hyperfleet-golangci-lint`, `hyperfleet-gofmt`, `hyperfleet-go-vet` — delegate to consuming repo's Make targets (`language: system`)

CI mode (`--pr`) commit range detection priority:
1. `PULL_REFS` env var (Prow standard, most accurate)
2. `PULL_BASE_SHA` + `PULL_PULL_SHA`
3. `PULL_BASE_REF` + `HEAD` (local fallback)

Falls back to GitHub API if local git history unavailable.

### Bot Author Whitelist

Whitelisted authors skip validation. Two mechanisms:
- Exact match: `whitelistedAuthors` map in `pkg/commitlint/validator.go`
- Pattern match: `whitelistedPatterns` — suffix + contains check (e.g., `@users.noreply.github.com` containing `[bot]`)

`IsWhitelistedAuthor()` accepts variadic identifiers (email, name, login) — any match = skip.

## Code Conventions

### Commit Messages

Format: `HYPERFLEET-### - type: description`

### Testing

- Table-driven tests, unit tests live alongside code (`foo_test.go` next to `foo.go`)
- `pkg/` tests use plain `if`/`t.Errorf` assertions — no testify
- `cmd/` tests use `testify/require` for setup-heavy git operations
- Run single test: `go test -run TestValidator_Validate ./pkg/commitlint/...`

### Validation Rules

Commit format: `[<JIRA_PROJECT_ID>-<TICKET_NUM> - ]<type>: <subject>`
- JIRA prefix: optional for commits, **required** for PR titles
- Type: one of `feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert`
- Header length: max 72 chars **excluding** JIRA prefix
- JIRA pattern: uppercase letters followed by digits (`[A-Z][A-Z0-9_]+-\d+`)

## Boundaries

### DO

- Keep validation rules in `pkg/commitlint/` — CLI layer in `cmd/` only handles I/O
- Update container image version across all component Prow configs when releasing

### DON'T

- Add scoped commits (e.g., `feat(api):`) — not supported by the standard
- Put business logic here — this repo is strictly for shared dev tooling

## Gotchas

- **Container image is the primary distribution mechanism** — components don't install the binary, they reference `quay.io/openshift-hyperfleet/hyperfleet-git-hooks:latest` in Prow job specs
- **Go tooling hooks (`language: system`) require Make targets in consuming repos** — `make lint`, `make gofmt`, `make go-vet` must exist
- **`GITHUB_TOKEN` optional but recommended in CI** — without it, GitHub API rate limit is 60 req/hr (unauthenticated)
- **JIRA prefix pattern accepts any project key** — not limited to `HYPERFLEET-`, any `[A-Z][A-Z0-9_]+-\d+` pattern works (OCM, MGDAPI, RHCLOUD, etc.)
- **PR title validation is stricter than commit validation** — PR titles always require JIRA prefix

