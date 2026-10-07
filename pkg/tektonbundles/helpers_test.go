package tektonbundles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	mediaOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mediaDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	mediaOCIIndex       = "application/vnd.oci.image.index.v1+json"
	mediaDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaDockerSchema1  = "application/vnd.docker.distribution.manifest.v1+prettyjws"

	taskScan  = "task-rpms-signature-scan"
	taskChart = "task-build-helm-chart-oci-ta"
)

// fastBackoff keeps retry tests at millisecond scale; production uses defaultBackoff.
var fastBackoff = remote.Backoff{Duration: time.Millisecond, Factor: 2, Jitter: 0.1, Steps: 3}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tektonLayer returns the annotations of a compliant bundle layer.
func tektonLayer(kind, objectName string) map[string]string {
	return map[string]string{
		annotationAPIVersion: "v1",
		annotationName:       objectName,
		annotationKind:       kind,
	}
}

// layersOf returns n compliant task layers.
func layersOf(n int) []map[string]string {
	layers := make([]map[string]string, 0, n)
	for i := range n {
		layers = append(layers, tektonLayer("task", fmt.Sprintf("task-%d", i)))
	}
	return layers
}

// imageManifest builds a raw image manifest. artifactType is omitted when empty.
func imageManifest(t *testing.T, mediaType, artifactType string, layers []map[string]string) []byte {
	t.Helper()
	layerList := make([]map[string]any, 0, len(layers))
	for i, annotations := range layers {
		layer := map[string]any{
			"mediaType": "application/vnd.oci.image.layer.v1.tar",
			"size":      10,
			"digest":    fmt.Sprintf("sha256:%064x", i+1),
		}
		if annotations != nil {
			layer["annotations"] = annotations
		}
		layerList = append(layerList, layer)
	}
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaType,
		"config": map[string]any{
			"mediaType": "application/vnd.oci.empty.v1+json",
			"size":      2,
			"digest":    "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
		},
		"layers": layerList,
	}
	if artifactType != "" {
		manifest["artifactType"] = artifactType
	}
	return mustJSON(t, manifest)
}

type indexChild struct {
	platform map[string]string // nil omits the platform
	digest   string
	size     int
}

func indexManifest(t *testing.T, mediaType string, children ...indexChild) []byte {
	t.Helper()
	list := make([]map[string]any, 0, len(children))
	for _, child := range children {
		entry := map[string]any{
			"mediaType": mediaOCIManifest,
			"digest":    child.digest,
			"size":      child.size,
		}
		if child.platform != nil {
			entry["platform"] = child.platform
		}
		list = append(list, entry)
	}
	return mustJSON(t, map[string]any{"schemaVersion": 2, "mediaType": mediaType, "manifests": list})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// fixture reads a corpus manifest and checks its bytes hash to its digest.
func fixture(t *testing.T, task, digest string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "manifests", fmt.Sprintf("%s_%s.json", task, strings.Replace(digest, ":", "_", 1)))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	if got := sha256Digest(data); got != digest {
		t.Fatalf("fixture %s hashes to %s, want %s", path, got, digest)
	}
	return data
}

type served struct {
	mediaType string
	body      []byte
}

// fakeRegistry is an in-process registry that serves manifests and records every request.
type fakeRegistry struct {
	t         *testing.T
	server    *httptest.Server
	manifests map[string]served // keyed by the manifest reference: a digest or a tag
	script    map[string][]int  // reference -> statuses to answer with before serving normally
	onRequest func(reference string)
	requests  []string // "<reference>" for every manifest GET, in order
	mu        sync.Mutex
}

var manifestPath = regexp.MustCompile(`^/v2/(.+)/manifests/(.+)$`)

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	r := &fakeRegistry{t: t, manifests: map[string]served{}, script: map[string][]int{}}
	r.server = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.server.Close)
	return r
}

// add serves body under its digest (and any extra references) and returns the digest.
func (r *fakeRegistry) add(mediaType string, body []byte, alsoAs ...string) string {
	digest := sha256Digest(body)
	r.manifests[digest] = served{mediaType: mediaType, body: body}
	for _, reference := range alsoAs {
		r.manifests[reference] = served{mediaType: mediaType, body: body}
	}
	return digest
}

func (r *fakeRegistry) host() string { return strings.TrimPrefix(r.server.URL, "http://") }

// prefix is the catalog prefix of this registry, for verifier.prefix.
func (r *fakeRegistry) prefix() string { return r.host() + "/konflux-ci/tekton-catalog/" }

func (r *fakeRegistry) digestRef(task, digest string) name.Digest {
	r.t.Helper()
	ref, err := name.NewDigest(r.prefix() + task + "@" + digest)
	if err != nil {
		r.t.Fatalf("name.NewDigest: %v", err)
	}
	return ref
}

// attempts counts the manifest GETs made for reference.
func (r *fakeRegistry) attempts(reference string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, got := range r.requests {
		if got == reference {
			n++
		}
	}
	return n
}

func (r *fakeRegistry) allRequests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func (r *fakeRegistry) handle(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v2/" || req.URL.Path == "/v2" {
		w.WriteHeader(http.StatusOK)
		return
	}
	match := manifestPath.FindStringSubmatch(req.URL.Path)
	if match == nil || req.Method != http.MethodGet {
		r.t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		http.NotFound(w, req)
		return
	}
	reference := match[2]

	r.mu.Lock()
	r.requests = append(r.requests, reference)
	status := 0
	if queue := r.script[reference]; len(queue) > 0 {
		status, r.script[reference] = queue[0], queue[1:]
	}
	manifest, found := r.manifests[reference]
	hook := r.onRequest
	r.mu.Unlock()

	if hook != nil {
		hook(reference)
	}

	switch {
	case status == http.StatusUnauthorized:
		writeRegistryError(w, status, "UNAUTHORIZED", "authentication required")
	case status == http.StatusTooManyRequests:
		writeRegistryError(w, status, "TOOMANYREQUESTS", "slow down")
	case status >= http.StatusInternalServerError:
		writeRegistryError(w, status, "UNKNOWN", "backend failure")
	case !found:
		writeRegistryError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
	default:
		w.Header().Set("Content-Type", manifest.mediaType)
		w.Header().Set("Docker-Content-Digest", sha256Digest(manifest.body))
		_, _ = w.Write(manifest.body)
	}
}

func writeRegistryError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"errors":[{"code":%q,"message":%q}]}`, code, message)
}

// writeFile writes content into dir and returns its path.
func writeFile(t *testing.T, dir, fileName, content string) string {
	t.Helper()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
