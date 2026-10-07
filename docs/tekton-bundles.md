# Tekton Bundle Verification

`hyperfleet-hooks verify-tekton-bundles` checks that every digest-pinned Konflux task bundle reference in a repository's `.tekton/` pipelines is a Tekton bundle that the Tekton bundle resolver will accept. It exists to stop a pin like the SARIF artifacts of HYPERFLEET-1744 (a valid-looking `quay.io/konflux-ci/tekton-catalog/...@sha256:...` that is not a Tekton bundle) from merging, before it breaks every build on `main`.

## Table of Contents

- [Usage](#usage)
- [Installation](#installation)
- [What Is Checked](#what-is-checked)
- [Output](#output)
- [Failure Categories](#failure-categories)
- [Registry Behavior](#registry-behavior)
- [Scope Limits](#scope-limits)
- [Consuming the Release in Prow](#consuming-the-release-in-prow)

## Usage

```bash
hyperfleet-hooks verify-tekton-bundles                      # checks ./.tekton
hyperfleet-hooks verify-tekton-bundles --directory path/to/.tekton
```

| Flag | Default | Description |
| --- | --- | --- |
| `--directory` | `.tekton` | Directory holding the pipeline `*.yaml` files. A directory, not a shell glob. |

The command takes no author, PR or bot input: it behaves identically on a local checkout and on the candidate tree Prow checks out.

Exit status:

- `0`: every digest-pinned catalog reference is a valid bundle, or the directory has no such references (reported explicitly, never silently skipped).
- non-zero: an invalid pin, an invalid bundle manifest, a manifest that could not be fetched after retries, an unreadable or malformed YAML file, or a missing directory. Every problem found is printed before the command exits, not only the first.

## Installation

The command is part of the `hyperfleet-hooks` binary. Install a released version rather than building from a branch:

```bash
go install github.com/openshift-hyperfleet/hyperfleet-hooks/cmd/hyperfleet-hooks@<release>
```

The first release containing this command is proposed as `v0.3.0` and **does not exist yet**; `v0.2.1` has no `verify-tekton-bundles` command. Until it is released, build the binary from this repository with `make build` (output: `bin/hyperfleet-hooks`).

`go install` puts the binary in `$(go env GOBIN)` (default `$(go env GOPATH)/bin`). It does not touch the `go.mod` of the repository you run it from.

## What Is Checked

### Discovery

- All immediate `*.yaml` files in the directory, in sorted order. Every YAML document in each file is read, so push and tag pipelines and `finally` tasks are all covered.
- YAML scalar values are inspected, not raw text, so comments and descriptive prose that mention a catalog URL are ignored. A value counts only when the whole scalar is a catalog reference beginning with `quay.io/konflux-ci/tekton-catalog/`. A value behind a YAML alias is counted once, at its anchor.
- A reference containing `@` is parsed as a digest pin; any `:tag` is stripped and only the repository and digest are fetched. A malformed attempted pin (for example `@sha256:abc`) fails with its file, line and original value.
- References are deduplicated by repository plus digest. A pin used in four pipelines is fetched once and reported with all four locations. The same hash in two different repositories is two pins.
- References without a digest (tag-only) are counted and reported but never fetched: this check validates pinned digests, it does not impose a tag-pinning policy. A tag is never resolved as a fallback for a failed digest.

### Compliance rules (parity with the Tekton resolver)

The check is a port of `checkImageCompliance` from the Tekton Pipelines bundle resolver ([`pkg/resolution/resolver/bundle/bundle.go`, v1.7.0](https://github.com/tektoncd/pipeline/blob/v1.7.0/pkg/resolution/resolver/bundle/bundle.go), Apache-2.0). The function and `MaximumBundleObjects` are identical in every version compared (v0.59.0, v0.68.0, v1.0.0, v1.3.0, v1.7.0 and `main` as of October 2026). A pin fails when:

- the manifest has more than 20 layers (`MaximumBundleObjects`);
- any layer lacks the `dev.tekton.image.apiVersion`, `dev.tekton.image.name` or `dev.tekton.image.kind` annotation (key presence, as the resolver checks it);
- any layer's `dev.tekton.image.kind` is not lowercase and singular;
- the manifest has no layers, or is malformed or wrongly typed JSON.

It accepts what the resolver accepts: `v1` and `v1beta1` `apiVersion` values, Docker or OCI manifests, an absent `artifactType`, a non-task object in layer 0 (a test pipeline in layer 0 with the task in layer 1 is fine), and repeated objects.

An OCI image index or Docker manifest list is followed to its `linux/amd64` child, as the resolver's `remote.Image` does. It fails if there is no such child. Schema 1 and any other media type fail. Layer blobs are never downloaded: only manifests are read.

The first failing check is reported in the resolver's own wording, so it matches what engineers see in Konflux failures, for example `the layer 0 does not contain a dev.tekton.image.apiVersion annotation`. When a layer is missing several annotations, the others are listed after it.

## Output

A failing pin prints the catalog task, the full digest, every source location with the original reference, the reason and the `artifactType`. This is a real run on hyperfleet-applier's pipelines with the HYPERFLEET-1744 SARIF digest pinned for `task-rpms-signature-scan`:

```text
FAIL task-rpms-signature-scan@sha256:12239e624c5bca759fc594bdefd3d4411a62c69bb4c29c5ad733b1e0d88fb8e9
  .tekton/hyperfleet-applier-push.yaml:531 quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan:0.2.2@sha256:12239e624c5bca759fc594bdefd3d4411a62c69bb4c29c5ad733b1e0d88fb8e9
  .tekton/hyperfleet-applier-tag.yaml:559 quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan:0.2.2@sha256:12239e624c5bca759fc594bdefd3d4411a62c69bb4c29c5ad733b1e0d88fb8e9
  invalid bundle manifest: the layer 0 does not contain a dev.tekton.image.apiVersion annotation
    (layer 0 also missing dev.tekton.image.name, dev.tekton.image.kind)
  artifactType=application/sarif+json

verify-tekton-bundles: FAILED
  files scanned:   4
  unique pins:     18
  tag-only refs:   0 (not fetched)
  validated:       17 of 18
Error: 1 of 18 pinned Tekton bundle references failed verification
```

`artifactType=<absent>` is printed when the manifest has none. The artifact type is diagnostic only: validity is decided by the compliance rules, not by an artifact-type whitelist.

On success the summary shows files scanned, unique pins, tag-only references and the validated count.

## Failure Categories

| Category | Meaning | Typical cause |
| --- | --- | --- |
| `invalid pinned reference` | The value has an `@` but is not a valid catalog digest pin. | A typo or a truncated digest. |
| `invalid bundle manifest` | The registry returned a manifest, and it is not a Tekton bundle. | The digest denotes a non-bundle artifact such as a SARIF referrer, an index with no `linux/amd64` child, a schema 1 manifest. |
| `unable to fetch pinned manifest` | No manifest was obtained. | The digest was deleted (`MANIFEST_UNKNOWN`), a registry or network outage that outlasted the retries, or a timeout. |

The two manifest categories are deliberately separate: a registry outage is not evidence that a digest denotes a bad artifact, and a fetch failure is never treated as a pass. A deleted digest is a genuine failure, because pipelines using it are broken too.

## Registry Behavior

- Anonymous access to the public catalog only. There is no credential lookup, keychain search or TLS bypass.
- Fetches are sequential and deduplicated, with no cross-run cache.
- Each reference is fetched with three attempts in total (go-containerregistry's native retry, 1s base, factor 2, 10% jitter). It retries HTTP 408, 429, 5xx and transport errors. It does not retry `404 MANIFEST_UNKNOWN` or `401`.
- Each reference has a 30 second budget (covering an index and its child) and the whole command has a 4 minute budget. When the budget runs out the remaining pins are reported as not fetched and the command fails.

## Scope Limits

A pin that passes is a digest the resolver would accept. The check does not catch:

- runtime problems in a valid bundle (for example a task image missing `yq`);
- task parameter incompatibilities or task versions;
- a valid bundle of the wrong task: the resolver's later step that matches the `taskRef` name and kind against the layers is not checked;
- layer content decode errors (layer tarballs are not downloaded);
- tag-only references and registries other than `quay.io/konflux-ci/tekton-catalog/`;
- Enterprise Contract policy, or Tekton versions newer than the compliance rules ported above. If Konflux moves to a new major Tekton line, re-diff `checkImageCompliance` and `MaximumBundleObjects`.

## Consuming the Release in Prow

Prow steps download the `linux/amd64` binary from the GitHub release, the same way the commitlint step does, rather than pulling the container image:

```bash
HOOKS_VERSION="v0.3.0"   # pin once, in the shared step script
curl -fsSL --retry 3 -o /tmp/hyperfleet-hooks \
  "https://github.com/openshift-hyperfleet/hyperfleet-hooks/releases/download/${HOOKS_VERSION}/hyperfleet-hooks-linux-amd64"
chmod +x /tmp/hyperfleet-hooks
/tmp/hyperfleet-hooks verify-tekton-bundles --directory .tekton
```

The release asset must be built from a revision containing this command (see "Releasing a New Version" in the README). Consumer repositories wrap the invocation in a `make verify-tekton-bundles` target that takes the binary path from `HYPERFLEET_HOOKS`, so the Prow step and a developer run the same command.
