package tektonbundles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
)

// registryVerifier returns a verifier wired to the fake registry through the real fetcher.
func registryVerifier(registry *fakeRegistry) *verifier {
	return &verifier{
		prefix:         registry.prefix(),
		fetch:          registryFetcher{backoff: fastBackoff}.fetch,
		overallTimeout: time.Minute,
		refTimeout:     10 * time.Second,
	}
}

// fakeFetcher answers from a table and counts calls per canonical reference.
type fakeFetcher struct {
	answers map[string]fakeAnswer
	calls   map[string]int
	mu      sync.Mutex
}

type fakeAnswer struct {
	err error
	raw []byte
}

func newFakeFetcher(answers map[string]fakeAnswer) *fakeFetcher {
	return &fakeFetcher{answers: answers, calls: map[string]int{}}
}

func (f *fakeFetcher) fetch(_ context.Context, ref name.Digest) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[ref.Name()]++
	answer, ok := f.answers[ref.Name()]
	if !ok {
		return nil, fmt.Errorf("unexpected fetch of %s", ref.Name())
	}
	return answer.raw, answer.err
}

func (f *fakeFetcher) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, calls := range f.calls {
		n += calls
	}
	return n
}

func fakeVerifier(f *fakeFetcher) *verifier {
	return &verifier{
		prefix:         catalogPrefix,
		fetch:          f.fetch,
		overallTimeout: time.Minute,
		refTimeout:     10 * time.Second,
	}
}

func pipelineFile(refs ...string) string {
	var b strings.Builder
	b.WriteString("spec:\n  tasks:\n")
	for _, ref := range refs {
		b.WriteString(bundleParam(ref))
	}
	return b.String()
}

func TestVerify_IncidentCorpusThroughARegistry(t *testing.T) {
	registry := newFakeRegistry(t)
	registry.add(mediaOCIManifest, fixture(t, taskScan, digestScanSARIFCurrent))
	registry.add(mediaOCIManifest, fixture(t, taskChart, digestChartSARIF))
	validScan := fixture(t, taskScan, digestScanValid)
	registry.add(mediaOCIManifest, validScan)
	// The tag now points at a healthy replacement; resolving it would hide the bad digest.
	registry.add(mediaOCIManifest, validScan, "0.2.2", "0.4.0")
	// digestScanDeleted is never registered: the registry answers 404 MANIFEST_UNKNOWN.

	prefix := registry.prefix()
	dir := t.TempDir()
	scanSARIF := prefix + taskScan + ":0.2.2@" + digestScanSARIFCurrent
	chartSARIF := prefix + taskChart + ":0.4.0@" + digestChartSARIF
	scanValid := prefix + taskScan + ":0.2.2@" + digestScanValid
	scanDeleted := prefix + taskScan + "@" + digestScanDeleted
	writeFile(t, dir, "applier-push.yaml", "kind: PipelineRun\nspec:\n  tasks:\n"+
		bundleParam(scanSARIF)+bundleParam(scanValid))
	writeFile(t, dir, "applier-pull.yaml", "spec:\n  tasks:\n"+
		bundleParam(scanSARIF)+bundleParam(chartSARIF)+bundleParam(scanDeleted)+bundleParam(prefix+"task-init:0.2"))

	var out bytes.Buffer
	err := registryVerifier(registry).verify(t.Context(), dir, &out)

	if err == nil || !strings.Contains(err.Error(), "3 of 4 pinned Tekton bundle references failed verification") {
		t.Fatalf("error = %v, want 3 of 4 failed", err)
	}
	report := out.String()

	blocks := map[string]string{}
	for _, block := range strings.Split(report, "\n\n") {
		first, _, _ := strings.Cut(block, "\n")
		blocks[strings.TrimPrefix(first, "FAIL ")] = block
	}

	scanBlock := blocks[taskScan+"@"+digestScanSARIFCurrent]
	for _, want := range []string{
		filepath.Join(dir, "applier-pull.yaml") + ":4 " + scanSARIF,
		filepath.Join(dir, "applier-push.yaml") + ":5 " + scanSARIF,
		"invalid bundle manifest: the layer 0 does not contain a dev.tekton.image.apiVersion annotation",
		"(layer 0 also missing dev.tekton.image.name, dev.tekton.image.kind)",
		"artifactType=application/sarif+json",
	} {
		if !strings.Contains(scanBlock, want) {
			t.Errorf("SARIF scan diagnostic lacks %q:\n%s", want, scanBlock)
		}
	}
	if strings.Index(scanBlock, "applier-pull.yaml") > strings.Index(scanBlock, "applier-push.yaml") {
		t.Errorf("locations are not sorted:\n%s", scanBlock)
	}

	chartBlock := blocks[taskChart+"@"+digestChartSARIF]
	if !strings.Contains(chartBlock, ":6 "+chartSARIF) ||
		!strings.Contains(chartBlock, "artifactType=application/sarif+json") {
		t.Errorf("SARIF chart diagnostic is incomplete:\n%s", chartBlock)
	}

	deletedBlock := blocks[taskScan+"@"+digestScanDeleted]
	if !strings.Contains(deletedBlock, "unable to fetch pinned manifest") ||
		!strings.Contains(deletedBlock, "MANIFEST_UNKNOWN") ||
		strings.Contains(deletedBlock, "invalid bundle manifest") ||
		strings.Contains(deletedBlock, "artifactType") {
		t.Errorf("deleted digest must be reported as a fetch failure only:\n%s", deletedBlock)
	}
	if _, listed := blocks[taskScan+"@"+digestScanValid]; listed {
		t.Error("the valid pin must not be reported")
	}

	for _, want := range []string{
		"verify-tekton-bundles: FAILED",
		"files scanned:   2",
		"unique pins:     4",
		"tag-only refs:   1 (not fetched)",
		"validated:       1 of 4",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("summary lacks %q:\n%s", want, report)
		}
	}

	// One fetch per unique digest, 404 not retried, and not one tag endpoint request.
	if n := registry.attempts(digestScanSARIFCurrent); n != 1 {
		t.Errorf("SARIF digest fetched %d times, want once for two locations", n)
	}
	if n := registry.attempts(digestScanDeleted); n != 1 {
		t.Errorf("deleted digest attempted %d times, want 1", n)
	}
	for _, reference := range registry.allRequests() {
		if !strings.HasPrefix(reference, "sha256:") {
			t.Errorf("registry received a request for non-digest reference %q", reference)
		}
	}
}

func TestVerify_ValidPinsPass(t *testing.T) {
	registry := newFakeRegistry(t)
	prefix := registry.prefix()
	chart := registry.add(mediaOCIManifest, fixture(t, taskChart, digestChartMissingYQ))
	opm := registry.add(mediaDockerManifest, fixture(t, "task-run-opm-command-oci-ta", digestOPM))

	childBody := imageManifest(t, mediaOCIManifest, "", layersOf(11))
	child := registry.add(mediaOCIManifest, childBody)
	index := registry.add(mediaOCIIndex, indexManifest(t, mediaOCIIndex,
		indexChild{digest: child, size: len(childBody), platform: map[string]string{"os": "linux", "architecture": "amd64"}}))

	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", pipelineFile(prefix+taskChart+":0.4.0@"+chart, prefix+"task-run-opm-command-oci-ta@"+opm))
	writeFile(t, dir, "b.yaml", pipelineFile(prefix+taskChart+"@"+chart, prefix+"task-multi-arch:1@"+index,
		prefix+"task-tag-only:1"))

	var out bytes.Buffer
	if err := registryVerifier(registry).verify(t.Context(), dir, &out); err != nil {
		t.Fatalf("verify: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"verify-tekton-bundles: OK",
		"files scanned:   2",
		"unique pins:     3",
		"tag-only refs:   1 (not fetched)",
		"validated:       3 of 3",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "FAIL") {
		t.Errorf("unexpected failure in output:\n%s", out.String())
	}
	if n := registry.attempts(chart); n != 1 {
		t.Errorf("digest used in two files fetched %d times, want 1", n)
	}
}

func TestVerify_ArchitectureMismatchIndexFails(t *testing.T) {
	registry := newFakeRegistry(t)
	body := imageManifest(t, mediaOCIManifest, "", layersOf(1))
	arm := registry.add(mediaOCIManifest, body)
	index := registry.add(mediaOCIIndex, indexManifest(t, mediaOCIIndex,
		indexChild{digest: arm, size: len(body), platform: map[string]string{"os": "linux", "architecture": "arm64"}}))

	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", pipelineFile(registry.prefix()+"task-arm-only@"+index))

	var out bytes.Buffer
	err := registryVerifier(registry).verify(t.Context(), dir, &out)

	if err == nil {
		t.Fatal("want failure")
	}
	wants := []string{"invalid bundle manifest: no child with platform linux/amd64 in index", "artifactType=<absent>"}
	for _, want := range wants {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestVerify_ZeroPinsIsAnExplicitSuccess(t *testing.T) {
	fetcher := newFakeFetcher(nil)
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "kind: Pipeline\nuse: "+catalogPrefix+"task-init:0.2\n")

	var out bytes.Buffer
	if err := fakeVerifier(fetcher).verify(t.Context(), dir, &out); err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, want := range []string{"verify-tekton-bundles: OK", "unique pins:     0", "tag-only refs:   1 (not fetched)",
		"nothing to validate"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if fetcher.total() != 0 {
		t.Errorf("fetched %d times, want none", fetcher.total())
	}
}

func TestVerify_ReadAndParseErrorsFail(t *testing.T) {
	fetcher := newFakeFetcher(nil)

	t.Run("missing directory", func(t *testing.T) {
		var out bytes.Buffer
		err := fakeVerifier(fetcher).verify(t.Context(), filepath.Join(t.TempDir(), ".tekton"), &out)
		if err == nil {
			t.Fatal("want an error")
		}
	})

	t.Run("malformed YAML", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "bad.yaml", "a: [\n")
		if err := fakeVerifier(fetcher).verify(t.Context(), dir, &bytes.Buffer{}); err == nil {
			t.Fatal("want an error")
		}
	})

	t.Run("exported entry point", func(t *testing.T) {
		if err := Verify(t.Context(), filepath.Join(t.TempDir(), "absent"), &bytes.Buffer{}); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestVerify_InvalidAttemptedPinsFailWithLocation(t *testing.T) {
	fetcher := newFakeFetcher(nil)
	dir := t.TempDir()
	bad := catalogPrefix + "task-init:0.2@sha256:deadbeef"
	writeFile(t, dir, "a.yaml", pipelineFile(bad))

	var out bytes.Buffer
	err := fakeVerifier(fetcher).verify(t.Context(), dir, &out)

	if err == nil || !strings.Contains(err.Error(), "1 of 1") {
		t.Fatalf("error = %v, want 1 of 1 failed", err)
	}
	location := filepath.Join(dir, "a.yaml") + ":4 " + bad
	for _, want := range []string{"invalid pinned reference", location, "invalid pins:    1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if fetcher.total() != 0 {
		t.Error("an invalid pin must not be fetched")
	}
}

func TestVerify_AggregatesAllFailuresAndKeepsGoing(t *testing.T) {
	good := validBundle(t)
	bad := imageManifest(t, mediaOCIManifest, "", layersOf(21))
	refs := map[string]fakeAnswer{
		catalogPrefix + "task-a@sha256:" + hex64("a"): {raw: bad},
		catalogPrefix + "task-b@sha256:" + hex64("b"): {err: errors.New("registry unreachable")},
		catalogPrefix + "task-c@sha256:" + hex64("c"): {raw: good},
		catalogPrefix + "task-d@sha256:" + hex64("d"): {raw: []byte("<html>")},
	}
	fetcher := newFakeFetcher(refs)
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", pipelineFile(
		pin("task-d", "", "d"), pin("task-c", "", "c"), pin("task-b", "", "b"), pin("task-a", "", "a")))

	var out bytes.Buffer
	err := fakeVerifier(fetcher).verify(t.Context(), dir, &out)

	if err == nil || !strings.Contains(err.Error(), "3 of 4") {
		t.Fatalf("error = %v, want 3 of 4", err)
	}
	if fetcher.total() != 4 {
		t.Errorf("fetched %d refs, want all 4 despite earlier failures", fetcher.total())
	}
	report := out.String()
	order := []string{"FAIL task-a@", "FAIL task-b@", "FAIL task-d@"}
	last := -1
	for _, marker := range order {
		i := strings.Index(report, marker)
		if i <= last {
			t.Errorf("%q missing or out of order in:\n%s", marker, report)
		}
		last = i
	}
	if strings.Contains(report, "FAIL task-c@") {
		t.Error("the valid pin must not be reported")
	}
	if !strings.Contains(report, "unable to fetch pinned manifest: registry unreachable") {
		t.Errorf("fetch failure not reported separately:\n%s", report)
	}
	if !strings.Contains(report, "contained more than the maximum 20 allow objects") {
		t.Errorf("layer-count failure missing:\n%s", report)
	}
}

func TestVerify_RegistryOutageFailsClosed(t *testing.T) {
	registry := newFakeRegistry(t)
	digest := registry.add(mediaOCIManifest, validBundle(t))
	registry.script[digest] = []int{503, 503, 503, 503, 503}

	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", pipelineFile(registry.prefix()+"task-a@"+digest))

	var out bytes.Buffer
	err := registryVerifier(registry).verify(t.Context(), dir, &out)

	if err == nil {
		t.Fatal("an exhausted outage must fail")
	}
	if strings.Contains(out.String(), "invalid bundle manifest") ||
		!strings.Contains(out.String(), "unable to fetch pinned manifest") {
		t.Errorf("outage must be a fetch failure, not an invalid manifest:\n%s", out.String())
	}
	if n := registry.attempts(digest); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

func TestVerify_TransientFailureRecovers(t *testing.T) {
	registry := newFakeRegistry(t)
	digest := registry.add(mediaOCIManifest, validBundle(t))
	registry.script[digest] = []int{429, 429}

	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", pipelineFile(registry.prefix()+"task-a@"+digest))

	var out bytes.Buffer
	if err := registryVerifier(registry).verify(t.Context(), dir, &out); err != nil {
		t.Fatalf("verify: %v\n%s", err, out.String())
	}
	if n := registry.attempts(digest); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

func TestVerify_ContextBounds(t *testing.T) {
	refs := map[string]fakeAnswer{}
	for _, c := range []string{"a", "b"} {
		refs[pin("task-"+c, "", c)] = fakeAnswer{raw: validBundle(t)}
	}
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", pipelineFile(pin("task-a", "", "a"), pin("task-b", "", "b")))

	t.Run("canceled before the run fetches nothing and fails every pin", func(t *testing.T) {
		fetcher := newFakeFetcher(refs)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		var out bytes.Buffer
		err := fakeVerifier(fetcher).verify(ctx, dir, &out)

		if err == nil || !strings.Contains(err.Error(), "2 of 2") {
			t.Fatalf("error = %v, want 2 of 2", err)
		}
		if fetcher.total() != 0 {
			t.Errorf("fetched %d times after cancellation", fetcher.total())
		}
		if !strings.Contains(out.String(), "context canceled") {
			t.Errorf("report should name the cause:\n%s", out.String())
		}
	})

	t.Run("a hung fetch is cut off by the per-reference timeout", func(t *testing.T) {
		v := &verifier{
			prefix: catalogPrefix,
			fetch: func(ctx context.Context, _ name.Digest) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			overallTimeout: time.Minute,
			refTimeout:     20 * time.Millisecond,
		}
		var out bytes.Buffer
		start := time.Now()
		err := v.verify(t.Context(), dir, &out)

		if err == nil || !strings.Contains(out.String(), "context deadline exceeded") {
			t.Fatalf("want deadline failures, got err=%v\n%s", err, out.String())
		}
		if strings.Count(out.String(), "FAIL ") != 2 {
			t.Errorf("both refs should fail independently:\n%s", out.String())
		}
		if time.Since(start) > 5*time.Second {
			t.Errorf("took %v", time.Since(start))
		}
	})

	t.Run("the overall budget fails the remaining pins without fetching them", func(t *testing.T) {
		calls := 0
		v := &verifier{
			prefix: catalogPrefix,
			fetch: func(ctx context.Context, _ name.Digest) ([]byte, error) {
				calls++
				<-ctx.Done()
				return nil, ctx.Err()
			},
			overallTimeout: 20 * time.Millisecond,
			refTimeout:     time.Minute,
		}
		var out bytes.Buffer
		err := v.verify(t.Context(), dir, &out)

		if err == nil || calls != 1 {
			t.Fatalf("err=%v calls=%d, want a failure after exactly one fetch", err, calls)
		}
		if !strings.Contains(out.String(), "not fetched: context deadline exceeded") {
			t.Errorf("remaining pin should be reported as not fetched:\n%s", out.String())
		}
	})
}

func TestVerify_RegistryTextCannotInjectReportLines(t *testing.T) {
	layers := []map[string]string{tektonLayer("Task\x1b[31m", "n")}
	raw := imageManifest(t, mediaOCIManifest, "x/y\nFAIL task-forged@sha256:00", layers)
	fetcher := newFakeFetcher(map[string]fakeAnswer{catalogPrefix + "task-a@sha256:" + hex64("a"): {raw: raw}})
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", pipelineFile(pin("task-a", "", "a")))

	var out bytes.Buffer
	_ = fakeVerifier(fetcher).verify(t.Context(), dir, &out)

	if strings.Contains(out.String(), "\nFAIL task-forged") || strings.Contains(out.String(), "\x1b") {
		t.Errorf("registry-controlled text reached the report unsanitized:\n%q", out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestVerify_ReportWriteErrorIsReturned(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "kind: Pipeline\n")
	err := fakeVerifier(newFakeFetcher(nil)).verify(t.Context(), dir, failingWriter{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("error = %v, want the write error", err)
	}
}

func TestProductionSettings(t *testing.T) {
	// The values the plan fixes: three attempts, one-second doubling backoff, 30s per
	// reference and four minutes overall, all against the real catalog.
	if defaultBackoff.Steps != 3 || defaultBackoff.Duration != time.Second || defaultBackoff.Factor != 2 {
		t.Errorf("defaultBackoff = %+v", defaultBackoff)
	}
	v := newVerifier()
	if v.prefix != "quay.io/konflux-ci/tekton-catalog/" ||
		v.refTimeout != 30*time.Second || v.overallTimeout != 4*time.Minute {
		t.Errorf("newVerifier() = prefix %q, refTimeout %v, overallTimeout %v", v.prefix, v.refTimeout, v.overallTimeout)
	}
}
