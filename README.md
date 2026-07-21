# HyperFleet Hooks

Shared validation tools for all HyperFleet projects. This repository is not meant to be cloned or built manually — it is consumed automatically by the [pre-commit](https://pre-commit.com/) framework and by Prow CI via a container image.

## How It Works

### For Developers (pre-commit hook)

Any HyperFleet repository can enforce commit message validation by adding a `.pre-commit-config.yaml` file. The `pre-commit` framework will automatically clone this repository, build the Go binary, cache it, and run it on every commit. No changes to the consuming repository's Makefile or build system are needed.

1. Install the pre-commit framework (one-time):

   ```bash
   pip install pre-commit
   ```

2. Add a `.pre-commit-config.yaml` to your repository:

   ```yaml
   repos:
     - repo: https://github.com/openshift-hyperfleet/hyperfleet-hooks
       rev: v0.1.0  # pin to a specific tag
       hooks:
         - id: hyperfleet-commitlint
   ```

3. Install the hook:

   ```bash
   pre-commit install --hook-type commit-msg
   ```

From this point on, every `git commit` in that repository will validate the commit message automatically. The developer does not need to install Go, clone this repo, or run `make build`.

### For CI (Prow)

In Prow, a pre-built container image (`quay.io/openshift-hyperfleet/hyperfleet-git-hooks`) is used to validate all commits and the PR title. See the [commitlint documentation](docs/commitlint.md) for Prow configuration details.

## Available Hooks

| Hook ID | Stage | Description |
| --- | --- | --- |
| `hyperfleet-commitlint` | `commit-msg` | Validates commit messages against the [HyperFleet Commit Standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/commit-standard.md) |
| `hyperfleet-golangci-lint` | `pre-commit` | Runs `make lint` - uses golangci-lint pinned in tools/go.mod |
| `hyperfleet-gofmt` | `pre-commit` | Runs `make gofmt` — checks Go file formatting |
| `hyperfleet-go-vet` | `pre-commit` | Runs `make go-vet` — finds suspicious constructs in Go code |

### Commitlint

Validates commit messages and PR titles against the [HyperFleet Commit Standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/commit-standard.md).

**[→ Full Documentation](docs/commitlint.md)**

### Go Tooling Hooks

The Go tooling hooks use `language: system` and delegate to the consuming repo's existing Make targets (`make lint`, `make gofmt`, `make go-vet`). This uses the repo's tool versions pinned in `tools/go.mod`. See the [dependency pinning standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/dependency-pinning.md) for details.

**[→ Documentation](docs/go-tooling.md)**

## Installation

### Prerequisites

- [pre-commit](https://pre-commit.com/#install) installed
- Go 1.26+ available (for the `commitlint` hook — built automatically by pre-commit)
- `make` targets (`lint`, `gofmt`, `go-vet`) in the consuming repo (for Go tooling hooks)

### Adding to your repository

Add this to your `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/openshift-hyperfleet/hyperfleet-hooks
    rev: main # Use latest release tag
    hooks:
      - id: hyperfleet-commitlint
      - id: hyperfleet-golangci-lint
      - id: hyperfleet-gofmt
      - id: hyperfleet-go-vet
```

Then install the hooks:

```bash
pre-commit install --hook-type commit-msg
pre-commit install --hook-type pre-commit
```

**Note**: The `commitlint` hook is built automatically by pre-commit (`language: golang`). The Go tooling hooks (`golangci-lint`, `gofmt`, `go-vet`) use `language: system` and require the consuming repo to have the corresponding Make targets.

See [commitlint documentation](docs/commitlint.md) for Prow CI setup and [go-tooling documentation](docs/go-tooling.md) for tool configuration.

## Development

These instructions are only needed if you are contributing to this repository.

```bash
make build    # Build binary
make test     # Run tests
make lint     # Run linters (golangci-lint, pinned in tools/go.mod)
make image    # Build container image
```

### Releasing a New Version

1. Create and push a release tag:

   ```bash
   git tag v0.1.0
   git push upstream v0.1.0
   ```

2. Build the linux/amd64 binary and create a GitHub release with it:

   ```bash
   GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o /tmp/hyperfleet-hooks-linux-amd64 ./cmd/hyperfleet-hooks
   gh release create v0.1.0 --repo openshift-hyperfleet/hyperfleet-hooks \
     --title "v0.1.0" --notes "Release notes here" \
     /tmp/hyperfleet-hooks-linux-amd64
   ```

   The binary is consumed by the Prow CI step (`hyperfleet-commitlint`) which downloads it directly from the GitHub release instead of compiling from source.

3. Build and push the container image to quay.io:

   ```bash
   make image IMAGE_TAG=v0.1.0
   make image-push IMAGE_TAG=v0.1.0
   ```

4. Verify the image is pullable:

   ```bash
   podman pull quay.io/openshift-hyperfleet/hyperfleet-git-hooks:v0.1.0
   podman pull quay.io/openshift-hyperfleet/hyperfleet-git-hooks:latest
   ```

## License

Apache 2.0
