# Commitlint Documentation

Complete guide for using hyperfleet-hooks commitlint in local development and Prow CI.

## Table of Contents

- [Overview](#overview)
- [Local Development](#local-development)
- [Prow CI Setup](#prow-ci-setup)
- [Cross-Component Usage](#cross-component-usage)
- [CLI Reference](#cli-reference)
- [Commit Message Standard](#commit-message-standard)
- [Troubleshooting](#troubleshooting)

## Overview

`hyperfleet-hooks commitlint` validates commit messages against the HyperFleet Commit Standard, based on [Conventional Commits](https://www.conventionalcommits.org/).

**Key Features**:
- ✅ Validates commit message format locally (pre-commit)
- ✅ Validates the PR title in Prow CI (the title becomes the squashed commit)
- ✅ Optional per-commit validation in CI with `--commits`
- ✅ Zero configuration for other components

**What is checked where**: CI validates the PR title, which becomes the squashed commit on `main`. Commits inside a PR need only the `<type>: <subject>` shape, enforced locally by the `commit-msg` hook. Push review fixes as new commits instead of amending.

## Local Development

### Installation

1. Install pre-commit framework:
   ```bash
   pip install pre-commit
   ```

2. Add to `.pre-commit-config.yaml`:
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

**Note**: Pre-commit will automatically clone the repository and build the binary. No manual installation of `hyperfleet-hooks` is needed.

### Manual Testing

```bash
# From file
hyperfleet-hooks commitlint .git/COMMIT_EDITMSG

# From stdin
echo "feat: add new feature" | hyperfleet-hooks commitlint
```

## Prow CI Setup

### Overview

`hyperfleet-hooks commitlint --pr` automatically:
1. Reads the PR title from the `JOB_SPEC` environment variable that Prow sets on every job
2. Falls back to the GitHub API (`REPO_OWNER`, `REPO_NAME`, `PULL_NUMBER`) when `JOB_SPEC` has no pull request
3. Fails the job when no title can be obtained from either source
4. Validates the PR title and reports the result

With `--pr --commits` it also detects the commit range from the environment and validates every commit in the PR before the title.

### PR Title Requirements

PR titles must include JIRA ticket: `<JIRA_PROJECT_ID>-<TICKET_NUM> - <type>: <subject>`

*Why?* PRs are squashed on merge, so the PR title becomes the commit message on `main` and carries the traceability to JIRA. Individual commits inside the PR do not reach `main`, which is why CI does not check them.

Examples:
- ✅ `HYPERFLEET-123 - feat: add cluster validation`
- ✅ `SENTINEL-42 - fix: resolve timeout`
- ❌ `feat: add cluster validation` (missing JIRA)

### Configuration

Add a presubmit job to your component's Prow configuration:

```yaml
# ci-operator/jobs/openshift-hyperfleet/<component>/<component>-main-presubmits.yaml

presubmits:
  openshift-hyperfleet/<component>:
  - name: pull-ci-openshift-hyperfleet-<component>-main-validate-commits
    cluster: build05
    always_run: true
    decorate: true
    spec:
      containers:
      - name: validate
        image: quay.io/openshift-hyperfleet/hyperfleet-git-hooks:latest
        command:
        - hyperfleet-hooks
        - commitlint
        - --pr
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
```

### Environment Variables

Prow automatically provides these variables (no configuration needed):

| Variable | Example | Description |
|----------|---------|-------------|
| `JOB_SPEC` | `{"refs":{"pulls":[{"number":123,"title":"..."}]}}` | Prow job spec JSON, the primary source of the PR title and author |
| `PULL_NUMBER` | `123` | PR number (GitHub API fallback for the title) |
| `REPO_OWNER` | `openshift-hyperfleet` | Repository owner (GitHub API fallback) |
| `REPO_NAME` | `hyperfleet-api` | Repository name (GitHub API fallback) |
| `GITHUB_TOKEN` | `ghp_...` | GitHub API token (optional, only used by the fallback) |
| `PULL_REFS` | `main:abc123,456:def789` | Base and PR SHAs for `--commits` (most accurate) |
| `PULL_BASE_SHA` | `abc123...` | Base branch SHA for `--commits` (fallback) |
| `PULL_PULL_SHA` | `def789...` | PR head SHA for `--commits` (fallback) |
| `PULL_BASE_REF` | `main` | Target branch name for `--commits` |

> **Note**: If `GITHUB_TOKEN` is not set, the GitHub API fallback is unauthenticated (60 req/hr rate limit) and prints a warning to stderr. In Prow the title normally comes from `JOB_SPEC`, so the API is only reached when that spec has no pull request.

**PR title source priority**:
1. `JOB_SPEC` (`refs.pulls[0].title`, set by Prow on every decorated job)
2. GitHub API via `REPO_OWNER`, `REPO_NAME`, `PULL_NUMBER`
3. Neither available: the job fails

**Commit range detection priority** (`--commits` only):
1. `PULL_REFS` (most accurate)
2. `PULL_BASE_SHA` + `PULL_PULL_SHA`
3. `PULL_BASE_REF` + `HEAD` (local fallback)

### Trigger in PR

```bash
/test validate-commits    # Run validation job
/test all                 # Run all tests
/retest                   # Rerun failed tests
```

## Cross-Component Usage

All HyperFleet components use the same container image. **No installation required in component repositories.**

### Architecture

```text
┌─────────────────────────────────┐
│  hyperfleet-hooks Repository    │
│                                 │
│  1. Build Go binary             │
│  2. Build container image       │
│  3. Push to quay.io             │
└─────────────────────────────────┘
              │
              ▼
   quay.io/openshift-hyperfleet/hyperfleet-git-hooks:latest
              │
    ┌─────────┼─────────┬─────────┐
    ▼         ▼         ▼         ▼
┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐
│  API   │ │Sentinel│ │Adapter │ │ Broker │
└────────┘ └────────┘ └────────┘ └────────┘
  Use pre-built image, no dependencies!
```

Components just reference the image in Prow configuration. No dependencies to install.

### Updating to New Version

```bash
# In openshift/release repository
cd ci-operator/jobs/openshift-hyperfleet/
sed -i 's|hyperfleet-git-hooks:v1.0.0|hyperfleet-git-hooks:v1.1.0|g' */*-presubmits.yaml
git commit -m "ci: update hyperfleet-git-hooks to v1.1.0"
```

## CLI Reference

### Commands

```bash
# Validate single commit message
hyperfleet-hooks commitlint [file]

# Validate the PR title (CI mode)
hyperfleet-hooks commitlint --pr

# Validate the PR title and every commit in the PR
hyperfleet-hooks commitlint --pr --commits

# Show version
hyperfleet-hooks version
```

### Flags

| Flag | Description |
|------|-------------|
| `--pr` | Validate the PR title (CI mode, title read from `JOB_SPEC` or the GitHub API) |
| `--commits` | With `--pr`, also validate every commit in the PR range |
| `-h, --help` | Show help |

### Exit Codes

- `0`: All validations passed
- `1`: Validation failed

## Commit Message Standard

### Format

```text
[<JIRA_PROJECT_ID>-<TICKET_NUM> - ]<type>: <subject>

[body]

[footer]
```

### Type (required)

- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation
- `style`: Code style (formatting)
- `refactor`: Code refactoring
- `perf`: Performance improvement
- `test`: Add/update tests
- `build`: Build system changes
- `ci`: CI configuration changes
- `chore`: Maintenance tasks
- `revert`: Revert previous commit

### Rules

- **JIRA ID**: Optional, format `<JIRA_PROJECT_ID>-<TICKET_NUM> - `
- **Type**: Required, one of the valid types (see above)
- **Subject**: Required, imperative mood, no period
- **Header Length**: `<type>: <subject>` must not exceed 72 characters (excluding JIRA prefix)
- **Body**: Optional, separated by blank line
- **Footer**: Optional, e.g., `BREAKING CHANGE:`, `Fixes #123`

### Examples

**Valid**:
```text
✅ feat: add user authentication
✅ HYPERFLEET-123 - fix: resolve memory leak
✅ SENTINEL-42 - docs: update API documentation
✅ refactor: simplify error handling
```

**Invalid**:
```text
❌ added new feature           # Missing type
❌ feat add feature             # Missing colon
❌ Feat: add feature            # Type must be lowercase
❌ feature: add feature         # Invalid type
```

## Troubleshooting

Common errors:
- `header must match format` → Add type: `feat:`, `fix:`, etc.
- `type must be one of` → Use valid type (feat, fix, docs, etc.)
- `header must not exceed 72 characters` → `<type>: <subject>` must be ≤ 72 chars (excluding JIRA prefix)
- `pr-title-requires-jira` → PR titles must start with `<JIRA_PROJECT_ID>-<TICKET_NUM> - `
- `could not get PR title` → neither `JOB_SPEC` nor the GitHub API env (`REPO_OWNER`, `REPO_NAME`, `PULL_NUMBER`) yielded a title; check the Prow job is decorated

Hook issues:
- Reinstall: `pre-commit install --hook-type commit-msg`
- Update version: `pre-commit autoupdate`

See [HyperFleet Commit Standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/commit-standard.md) for details.
