package tektonbundles

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func validBundle(t *testing.T) []byte {
	t.Helper()
	return imageManifest(t, mediaOCIManifest, "", layersOf(1))
}

func TestRegistryFetcher_RetryBehavior(t *testing.T) {
	tests := []struct {
		name         string
		script       []int
		wantAttempts int
		wantErr      bool
		wantStatus   int
	}{
		{name: "healthy first try", wantAttempts: 1},
		{
			name:         "two 429s then OK passes on attempt three",
			script:       []int{429, 429},
			wantAttempts: 3,
		},
		{
			name:         "transient 5xx recovers",
			script:       []int{503, 500},
			wantAttempts: 3,
		},
		{
			name:         "retries exhaust on persistent 429",
			script:       []int{429, 429, 429, 429},
			wantAttempts: 3,
			wantErr:      true,
			wantStatus:   http.StatusTooManyRequests,
		},
		{
			name:         "retries exhaust on persistent 503",
			script:       []int{503, 503, 503, 503},
			wantAttempts: 3,
			wantErr:      true,
			wantStatus:   http.StatusServiceUnavailable,
		},
		{
			name:         "401 is permanent",
			script:       []int{401, 401, 401},
			wantAttempts: 1,
			wantErr:      true,
			wantStatus:   http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := newFakeRegistry(t)
			body := validBundle(t)
			digest := registry.add(mediaOCIManifest, body)
			registry.script[digest] = tt.script

			got, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef("task-a", digest))

			if n := registry.attempts(digest); n != tt.wantAttempts {
				t.Errorf("attempts = %d, want %d", n, tt.wantAttempts)
			}
			if tt.wantErr {
				var transportErr *transport.Error
				if !errors.As(err, &transportErr) || transportErr.StatusCode != tt.wantStatus {
					t.Fatalf("want a registry error with status %d, got %v", tt.wantStatus, err)
				}
				var invalid *invalidManifestError
				if errors.As(err, &invalid) {
					t.Error("a registry failure must not be classified as an invalid manifest")
				}
				return
			}
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Error("raw manifest bytes changed in transit")
			}
		})
	}
}

func TestRegistryFetcher_MissingManifestIsNotRetried(t *testing.T) {
	registry := newFakeRegistry(t)
	digest := sha256Digest([]byte("deleted upstream"))

	_, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef(taskScan, digest))

	var transportErr *transport.Error
	if !errors.As(err, &transportErr) || transportErr.StatusCode != http.StatusNotFound {
		t.Fatalf("want a 404, got %v", err)
	}
	if !strings.Contains(err.Error(), "MANIFEST_UNKNOWN") {
		t.Errorf("error %q should carry MANIFEST_UNKNOWN", err)
	}
	if n := registry.attempts(digest); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

func TestRegistryFetcher_ContextCancellationStopsRetries(t *testing.T) {
	registry := newFakeRegistry(t)
	digest := registry.add(mediaOCIManifest, validBundle(t))
	registry.script[digest] = []int{503, 503, 503, 503}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry.onRequest = func(string) { cancel() }

	// A one-hour backoff proves the retry loop is interrupted rather than waited out.
	slow := fastBackoff
	slow.Duration = time.Hour

	start := time.Now()
	_, err := registryFetcher{backoff: slow}.fetch(ctx, registry.digestRef("task-a", digest))

	if err == nil {
		t.Fatal("want an error after cancellation")
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("fetch took %v after cancellation", time.Since(start))
	}
	if n := registry.attempts(digest); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

func TestRegistryFetcher_ManifestKinds(t *testing.T) {
	annotated := layersOf(1)

	t.Run("OCI and Docker image manifests are returned as is", func(t *testing.T) {
		for _, mediaType := range []string{mediaOCIManifest, mediaDockerManifest} {
			registry := newFakeRegistry(t)
			body := imageManifest(t, mediaType, "", annotated)
			digest := registry.add(mediaType, body)

			got, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef("task-a", digest))
			if err != nil || !bytes.Equal(got, body) {
				t.Errorf("%s: err=%v, bytes equal=%v", mediaType, err, bytes.Equal(got, body))
			}
		}
	})

	t.Run("an index is followed to its linux/amd64 child, whatever its position", func(t *testing.T) {
		for _, indexType := range []string{mediaOCIIndex, mediaDockerList} {
			registry := newFakeRegistry(t)
			arm := registry.add(mediaOCIManifest, imageManifest(t, mediaOCIManifest, "", layersOf(2)))
			childBody := imageManifest(t, mediaOCIManifest, "", annotated)
			amd := registry.add(mediaOCIManifest, childBody)
			index := indexManifest(t, indexType,
				indexChild{digest: arm, size: 100, platform: map[string]string{"os": "linux", "architecture": "arm64"}},
				indexChild{digest: amd, size: len(childBody), platform: map[string]string{"os": "linux", "architecture": "amd64"}},
			)
			digest := registry.add(indexType, index)

			got, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef("task-a", digest))
			if err != nil {
				t.Fatalf("%s: %v", indexType, err)
			}
			if !bytes.Equal(got, childBody) {
				t.Errorf("%s: want the amd64 child manifest, got something else", indexType)
			}
			if registry.attempts(amd) != 1 || registry.attempts(arm) != 0 {
				t.Errorf("%s: child fetches amd64=%d arm64=%d, want 1 and 0",
					indexType, registry.attempts(amd), registry.attempts(arm))
			}
		}
	})

	t.Run("an index child that fails to fetch is a fetch error, not an invalid manifest", func(t *testing.T) {
		registry := newFakeRegistry(t)
		missing := sha256Digest([]byte("child never published"))
		index := indexManifest(t, mediaOCIIndex, indexChild{digest: missing, size: 10})
		digest := registry.add(mediaOCIIndex, index)

		_, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef("task-a", digest))

		var invalid *invalidManifestError
		if err == nil || errors.As(err, &invalid) {
			t.Fatalf("want a plain fetch error, got %v", err)
		}
		if registry.attempts(missing) != 1 {
			t.Errorf("child attempts = %d, want 1", registry.attempts(missing))
		}
	})

	t.Run("an index with only an arm64 child is invalid and fetches nothing else", func(t *testing.T) {
		registry := newFakeRegistry(t)
		arm := registry.add(mediaOCIManifest, imageManifest(t, mediaOCIManifest, "", annotated))
		index := indexManifest(t, mediaOCIIndex,
			indexChild{digest: arm, size: 100, platform: map[string]string{"os": "linux", "architecture": "arm64"}})
		digest := registry.add(mediaOCIIndex, index)

		_, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef("task-a", digest))

		var invalid *invalidManifestError
		if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "no child with platform linux/amd64") {
			t.Fatalf("want an invalid-manifest error, got %v", err)
		}
		if got := registry.allRequests(); len(got) != 1 || got[0] != digest {
			t.Errorf("requests = %v, want only the index", got)
		}
	})

	t.Run("a child without a platform counts as linux/amd64", func(t *testing.T) {
		registry := newFakeRegistry(t)
		childBody := imageManifest(t, mediaOCIManifest, "", annotated)
		child := registry.add(mediaOCIManifest, childBody)
		index := indexManifest(t, mediaOCIIndex, indexChild{digest: child, size: len(childBody)})
		digest := registry.add(mediaOCIIndex, index)

		got, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef("task-a", digest))
		if err != nil || !bytes.Equal(got, childBody) {
			t.Fatalf("err=%v, bytes equal=%v", err, bytes.Equal(got, childBody))
		}
	})

	t.Run("schema 1 and unknown media types are invalid", func(t *testing.T) {
		for _, mediaType := range []string{mediaDockerSchema1, "application/json", "text/plain"} {
			registry := newFakeRegistry(t)
			body := []byte(`{"schemaVersion":1,"artifactType":"x/y","layers":[]}`)
			digest := registry.add(mediaType, body)

			_, err := registryFetcher{backoff: fastBackoff}.fetch(t.Context(), registry.digestRef("task-a", digest))

			var invalid *invalidManifestError
			if !errors.As(err, &invalid) {
				t.Fatalf("%s: want an invalid-manifest error, got %v", mediaType, err)
			}
			if got := artifactTypeOf(invalid.Raw); got != "x/y" {
				t.Errorf("%s: artifactType of the raw answer = %q, want x/y", mediaType, got)
			}
		}
	})
}
