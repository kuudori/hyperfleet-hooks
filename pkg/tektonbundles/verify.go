// Package tektonbundles verifies that every digest-pinned Konflux catalog
// reference in a directory of Tekton pipeline files resolves to a manifest the
// Tekton bundle resolver accepts, so a broken pin is caught before it merges.
package tektonbundles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

const (
	// overallTimeout bounds one Verify call, including all retries.
	overallTimeout = 4 * time.Minute
	// refTimeout bounds the fetches for a single reference, including an index child.
	refTimeout = 30 * time.Second
)

const (
	categoryInvalidPin      = "invalid pinned reference"
	categoryInvalidManifest = "invalid bundle manifest"
	categoryFetch           = "unable to fetch pinned manifest"
)

// verifier holds the seams the tests replace; production code uses newVerifier.
type verifier struct {
	fetch          manifestFetcher
	prefix         string
	overallTimeout time.Duration
	refTimeout     time.Duration
}

func newVerifier() *verifier {
	return &verifier{
		prefix:         catalogPrefix,
		fetch:          registryFetcher{backoff: defaultBackoff}.fetch,
		overallTimeout: overallTimeout,
		refTimeout:     refTimeout,
	}
}

// failure is one reportable problem, with everything an engineer needs to fix it.
type failure struct {
	Compliance   *complianceFailure
	Title        string
	Category     string
	Reason       string
	ArtifactType string
	Locations    []location
}

// Verify checks every digest-pinned quay.io/konflux-ci/tekton-catalog/ reference
// found in the immediate *.yaml files of directory.
//
// Each unique repository and digest is fetched once, anonymously and by digest
// only, and judged by the Tekton bundle resolver's own compliance rules. Tag-only
// references are counted but never fetched. A report goes to output. Verify
// returns a non-nil error if the directory or a file cannot be read or parsed,
// or if any reference is invalid or cannot be fetched.
func Verify(ctx context.Context, directory string, output io.Writer) error {
	return newVerifier().verify(ctx, directory, output)
}

func (v *verifier) verify(ctx context.Context, directory string, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, v.overallTimeout)
	defer cancel()

	found, err := discover(directory, v.prefix)
	if err != nil {
		return err
	}

	var failures []failure
	for _, pin := range found.Invalid {
		failures = append(failures, failure{
			Title:     "malformed pin",
			Category:  categoryInvalidPin,
			Reason:    pin.Err.Error(),
			Locations: []location{pin.Location},
		})
	}
	for _, ref := range found.Refs {
		if f := v.check(ctx, ref); f != nil {
			failures = append(failures, *f)
		}
	}

	total := len(found.Refs) + len(found.Invalid)
	if _, err := io.WriteString(output, renderReport(found, failures)); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d of %d pinned Tekton bundle references failed verification", len(failures), total)
	}
	return nil
}

// check fetches and judges one pin. It returns nil when the pin is a valid bundle.
func (v *verifier) check(ctx context.Context, ref *pinnedRef) *failure {
	f := failure{
		Title:     ref.Task + "@" + ref.Ref.DigestStr(),
		Locations: ref.Locations,
	}

	if err := ctx.Err(); err != nil {
		f.Category, f.Reason = categoryFetch, "not fetched: "+err.Error()
		return &f
	}

	ctx, cancel := context.WithTimeout(ctx, v.refTimeout)
	defer cancel()

	raw, err := v.fetch(ctx, ref.Ref)
	if invalid, ok := errors.AsType[*invalidManifestError](err); ok {
		f.Category, f.Reason = categoryInvalidManifest, invalid.Reason
		f.ArtifactType = artifactTypeOf(invalid.Raw)
		return &f
	}
	if err != nil {
		f.Category, f.Reason = categoryFetch, err.Error()
		return &f
	}

	artifactType, failed := checkCompliance(raw)
	if failed == nil {
		return nil
	}
	f.Category, f.Reason, f.Compliance, f.ArtifactType = categoryInvalidManifest, failed.Reason, failed, artifactType
	return &f
}

func renderReport(found *discovery, failures []failure) string {
	var b strings.Builder

	for _, f := range failures {
		fmt.Fprintf(&b, "FAIL %s\n", clean(f.Title))
		for _, loc := range f.Locations {
			fmt.Fprintf(&b, "  %s:%d %s\n", loc.File, loc.Line, clean(loc.Value))
		}
		fmt.Fprintf(&b, "  %s: %s\n", f.Category, clean(f.Reason))
		if c := f.Compliance; c != nil && len(c.AlsoMissing) > 0 {
			fmt.Fprintf(&b, "    (layer %d also missing %s)\n", c.Layer, strings.Join(c.AlsoMissing, ", "))
		}
		if f.ArtifactType != "" {
			fmt.Fprintf(&b, "  artifactType=%s\n", clean(f.ArtifactType))
		}
		b.WriteString("\n")
	}

	total := len(found.Refs) + len(found.Invalid)
	status := "OK"
	if len(failures) > 0 {
		status = "FAILED"
	}
	fmt.Fprintf(&b, "verify-tekton-bundles: %s\n", status)
	fmt.Fprintf(&b, "  files scanned:   %d\n", len(found.Files))
	fmt.Fprintf(&b, "  unique pins:     %d\n", len(found.Refs))
	fmt.Fprintf(&b, "  tag-only refs:   %d (not fetched)\n", found.TagOnly)
	if len(found.Invalid) > 0 {
		fmt.Fprintf(&b, "  invalid pins:    %d\n", len(found.Invalid))
	}
	fmt.Fprintf(&b, "  validated:       %d of %d\n", total-len(failures), total)
	if total == 0 {
		b.WriteString("  no digest-pinned catalog references found, nothing to validate\n")
	}
	return b.String()
}

// clean makes registry-supplied text safe to print on one line.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, s)
}
