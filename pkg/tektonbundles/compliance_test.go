package tektonbundles

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Corpus digests from the HYPERFLEET-1744 incident and the tag history around it.
const (
	digestScanSARIFCurrent = "sha256:12239e624c5bca759fc594bdefd3d4411a62c69bb4c29c5ad733b1e0d88fb8e9"
	digestScanSARIFEarly   = "sha256:e9088ff4bcc315abe05076cd066a27db058ced1e6fa177abda6840f16aff6e8b"
	digestScanDeleted      = "sha256:0f97bf8b6537ce98009ea8d336f02d48e7a8be1fb2ec5b42be3969e6d6788f76"
	digestScanValid        = "sha256:cc1b939d933d24727ef4b90e1c5c65a6d2a6c1c036cf74f685b597d611fa150f"
	digestChartSARIF       = "sha256:1cfcc0de161da38b5dd4b3b806a291688bcb50ffd41285f2ef72a772ce238896"
	digestChartValid       = "sha256:7615bae91322179953527aedd1e855c31f5aa1b36592962db8a063f909635a96"
	digestChartMissingYQ   = "sha256:be1234f453a7236fb40c0d8b4d98313f3cfb7e967e1b4816c1881b8fc48ab915"
	digestOPM              = "sha256:155d18958065b9f6fd7a0d333a6d098db78145d4fa9ddb8f18abcd0e4eb73bc8"

	sarifType = "application/sarif+json"
)

type corpusEntry struct {
	task   string
	digest string
	pass   bool
}

var corpus = []corpusEntry{
	// SARIF artifacts that were published under the bundle tags.
	{taskScan, digestScanSARIFCurrent, false},
	{taskScan, digestScanSARIFEarly, false},
	{taskChart, digestChartSARIF, false},
	{taskChart, "sha256:88492a27a47e13308aac3f58014a20a1d4d3a85ab596eca6f0e383e25f6168a7", false},
	{taskChart, "sha256:a4db7fcdbfff9db4e23902c00991089788db951778ead313f2352631b90be1ce", false},
	{taskChart, "sha256:4046f35c6d88ae8aa1b34063529382392704c848d4826f263535f92ac8f42ec0", false},
	// Valid bundles, including the pre-incident, repaired and later tag values.
	{taskScan, digestScanValid, true},
	{taskScan, "sha256:4b1856e41d087e53af26562fb16a490579777d9ff05af33011ce212d477c7cd5", true},
	{taskScan, "sha256:e2875a6c1368c3ed78dca9d01a6869ea6fdef03449e98970c8ba313095b58eff", true},
	{taskScan, "sha256:950ff25761458ba3a2b44748778c1cb31656149a4a3d56be4b32434d37827270", true},
	{taskChart, digestChartValid, true},
	{taskChart, "sha256:cc59ae16baa756d23f3e5f8328cd9f8ba39a388f8eafbb9db666dea694814d25", true},
	// Missing yq at runtime, but a perfectly valid bundle: runtime correctness is not promised.
	{taskChart, digestChartMissingYQ, true},
	{taskChart, "sha256:e18fe17384dbb24f55a2538263346865211ac3016df8c1ca94a0fba80f7930e2", true},
	{taskChart, "sha256:eb6cab2cae1ce96567d1f21dbbdbe4453de1429ac5ab9cf48aca1fbd090eb5fd", true},
	{taskChart, "sha256:e54c00019c9d6c534c11db93ce0c8eecca3989375b0eef5517727e96e28af190", true},
	// Pipeline in layer 0 and the task in layer 1.
	{"task-run-opm-command-oci-ta", digestOPM, true},
}

func TestCorpus(t *testing.T) {
	for _, entry := range corpus {
		t.Run(entry.task+"@"+entry.digest[:19], func(t *testing.T) {
			raw := fixture(t, entry.task, entry.digest)
			artifactType, failed := checkCompliance(raw)

			if entry.pass {
				if failed != nil {
					t.Fatalf("want pass, got failure: %+v", failed)
				}
				return
			}
			if failed == nil {
				t.Fatal("want failure, got pass")
			}
			if artifactType != sarifType {
				t.Errorf("artifactType = %q, want %q", artifactType, sarifType)
			}
			wantReason := "the layer 0 does not contain a dev.tekton.image.apiVersion annotation"
			if failed.Reason != wantReason {
				t.Errorf("reason = %q, want %q", failed.Reason, wantReason)
			}
			wantAlso := []string{annotationName, annotationKind}
			if !slices.Equal(failed.AlsoMissing, wantAlso) || failed.Layer != 0 {
				t.Errorf("also missing = %v on layer %d, want %v on layer 0", failed.AlsoMissing, failed.Layer, wantAlso)
			}
		})
	}
}

// TestCorpusFilesAreWhatTheirNamesSay guards the testdata: every file hashes to the digest in
// its name, and every file is either listed in the corpus or a current valid pin.
func TestCorpusFilesAreWhatTheirNamesSay(t *testing.T) {
	listed := map[string]bool{}
	for _, entry := range corpus {
		listed[entry.task+"_"+strings.Replace(entry.digest, ":", "_", 1)+".json"] = true
	}

	files, err := filepath.Glob(filepath.Join("testdata", "manifests", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, file := range files {
		base := filepath.Base(file)
		task, hexDigest, ok := strings.Cut(strings.TrimSuffix(base, ".json"), "_sha256_")
		if !ok {
			t.Errorf("%s: name is not <task>_sha256_<hex>.json", base)
			continue
		}
		raw := fixture(t, task, "sha256:"+hexDigest)
		if listed[base] {
			continue
		}
		// Not in the table: a current pin from a consumer repository, which must pass.
		if _, failed := checkCompliance(raw); failed != nil {
			t.Errorf("%s: current pin should be a valid bundle, got %+v", base, failed)
		}
	}
}

func TestCheckCompliance_ParityWithTektonResolver(t *testing.T) {
	annotated := func(kind string) map[string]string { return tektonLayer(kind, "obj") }
	without := func(key string) map[string]string {
		layer := annotated("task")
		delete(layer, key)
		return layer
	}

	tests := []struct {
		name         string
		mediaType    string
		artifactType string
		wantReason   string
		layers       []map[string]string
		wantAlso     []string
		wantPass     bool
	}{
		{
			name:      "valid single OCI task layer",
			mediaType: mediaOCIManifest,
			layers:    []map[string]string{annotated("task")},
			wantPass:  true,
		},
		{
			name:      "valid Docker manifest",
			mediaType: mediaDockerManifest,
			layers:    []map[string]string{annotated("task")},
			wantPass:  true,
		},
		{
			name:      "v1beta1 apiVersion",
			mediaType: mediaOCIManifest,
			layers: []map[string]string{{
				annotationAPIVersion: "tekton.dev/v1beta1", annotationName: "x", annotationKind: "task",
			}},
			wantPass: true,
		},
		{
			name:         "absent artifactType is fine, a Tekton-looking one is not required",
			mediaType:    mediaOCIManifest,
			artifactType: "application/vnd.dev.tekton.bundle",
			layers:       []map[string]string{annotated("task")},
			wantPass:     true,
		},
		{
			name:      "pipeline in layer 0 and task in layer 1",
			mediaType: mediaDockerManifest,
			layers:    []map[string]string{annotated("pipeline"), annotated("task")},
			wantPass:  true,
		},
		{
			name:      "repeated objects",
			mediaType: mediaDockerManifest,
			layers:    []map[string]string{annotated("pipelinerun"), annotated("pipelinerun")},
			wantPass:  true,
		},
		{
			name:      "11 annotated layers (the resolver's cap is 20)",
			mediaType: mediaOCIManifest,
			layers:    layersOf(11),
			wantPass:  true,
		},
		{
			name:      "20 annotated layers",
			mediaType: mediaOCIManifest,
			layers:    layersOf(20),
			wantPass:  true,
		},
		{
			name:       "21 layers",
			mediaType:  mediaOCIManifest,
			layers:     layersOf(21),
			wantReason: "contained more than the maximum 20 allow objects",
		},
		{
			name:       "zero layers",
			mediaType:  mediaOCIManifest,
			layers:     nil,
			wantReason: "the manifest has no layers, so it contains no Tekton objects",
		},
		{
			name:       "layer 1 missing apiVersion, layer 0 valid",
			mediaType:  mediaOCIManifest,
			layers:     []map[string]string{annotated("task"), without(annotationAPIVersion)},
			wantReason: "the layer 1 does not contain a dev.tekton.image.apiVersion annotation",
		},
		{
			name:       "layer 0 kind Task",
			mediaType:  mediaOCIManifest,
			layers:     []map[string]string{annotated("Task")},
			wantReason: "the layer 0 the annotation dev.tekton.image.kind must be lowercased and singular, found Task",
		},
		{
			name:       "plural kind",
			mediaType:  mediaOCIManifest,
			layers:     []map[string]string{annotated("tasks")},
			wantReason: "the layer 0 the annotation dev.tekton.image.kind must be lowercased and singular, found tasks",
		},
		{
			name:       "later layer with a plural kind",
			mediaType:  mediaOCIManifest,
			layers:     []map[string]string{annotated("task"), annotated("pipelines")},
			wantReason: "the layer 1 the annotation dev.tekton.image.kind must be lowercased and singular, found pipelines",
		},
		{
			name:       "missing name only",
			mediaType:  mediaOCIManifest,
			layers:     []map[string]string{without(annotationName)},
			wantReason: "the layer 0 does not contain a dev.tekton.image.name annotation",
		},
		{
			name:       "missing kind only",
			mediaType:  mediaOCIManifest,
			layers:     []map[string]string{without(annotationKind)},
			wantReason: "the layer 0 does not contain a dev.tekton.image.kind annotation",
		},
		{
			name:         "SARIF-shaped layer with only a title",
			mediaType:    mediaOCIManifest,
			artifactType: sarifType,
			layers:       []map[string]string{{"org.opencontainers.image.title": "x.sarif"}},
			wantReason:   "the layer 0 does not contain a dev.tekton.image.apiVersion annotation",
			wantAlso:     []string{annotationName, annotationKind},
		},
		{
			name:       "layer without any annotations",
			mediaType:  mediaOCIManifest,
			layers:     []map[string]string{nil},
			wantReason: "the layer 0 does not contain a dev.tekton.image.apiVersion annotation",
			wantAlso:   []string{annotationName, annotationKind},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := imageManifest(t, tt.mediaType, tt.artifactType, tt.layers)
			artifactType, failed := checkCompliance(raw)

			wantType := tt.artifactType
			if wantType == "" {
				wantType = artifactTypeAbsent
			}
			if artifactType != wantType {
				t.Errorf("artifactType = %q, want %q", artifactType, wantType)
			}
			if tt.wantPass {
				if failed != nil {
					t.Fatalf("want pass, got %+v", failed)
				}
				return
			}
			if failed == nil {
				t.Fatal("want failure, got pass")
			}
			if failed.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", failed.Reason, tt.wantReason)
			}
			if !slices.Equal(failed.AlsoMissing, tt.wantAlso) {
				t.Errorf("also missing = %v, want %v", failed.AlsoMissing, tt.wantAlso)
			}
		})
	}
}

func TestCheckCompliance_TopLevelAnnotationsDoNotRescueALayer(t *testing.T) {
	raw := []byte(`{"schemaVersion":2,"annotations":{"dev.tekton.image.apiVersion":"v1",` +
		`"dev.tekton.image.name":"x","dev.tekton.image.kind":"task"},` +
		`"layers":[{"mediaType":"x","size":1,"digest":"sha256:00"}]}`)

	_, failed := checkCompliance(raw)
	if failed == nil || !strings.Contains(failed.Reason, "the layer 0 does not contain") {
		t.Fatalf("want a layer 0 failure, got %+v", failed)
	}
}

func TestCheckCompliance_MalformedJSONFailsWithoutPanicking(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantType string
	}{
		{name: "empty", raw: "", wantType: artifactTypeAbsent},
		{name: "not JSON", raw: "<html>502</html>", wantType: artifactTypeAbsent},
		{name: "truncated", raw: `{"layers":[{"annotations":`, wantType: artifactTypeAbsent},
		{name: "array at top level", raw: `[]`, wantType: artifactTypeAbsent},
		{name: "null", raw: `null`, wantType: artifactTypeAbsent},
		{name: "layers is a string", raw: `{"artifactType":"a/b","layers":"x"}`, wantType: "a/b"},
		{name: "layers is an object", raw: `{"layers":{}}`, wantType: artifactTypeAbsent},
		{name: "layer is a string", raw: `{"layers":["x"]}`, wantType: artifactTypeAbsent},
		{name: "annotations is a list", raw: `{"layers":[{"annotations":[]}]}`, wantType: artifactTypeAbsent},
		{
			name:     "annotation value is a number",
			raw:      `{"layers":[{"annotations":{"dev.tekton.image.kind":1}}]}`,
			wantType: artifactTypeAbsent,
		},
		{name: "artifactType is a number", raw: `{"artifactType":5,"layers":"x"}`, wantType: "<invalid: 5>"},
		{name: "artifactType is null", raw: `{"artifactType":null,"layers":[]}`, wantType: artifactTypeAbsent},
		{name: "artifactType is empty", raw: `{"artifactType":"","layers":[]}`, wantType: artifactTypeAbsent},
		{name: "layers is null", raw: `{"layers":null}`, wantType: artifactTypeAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifactType, failed := checkCompliance([]byte(tt.raw))
			if failed == nil {
				t.Fatal("want failure, got pass")
			}
			if failed.Reason == "" {
				t.Error("failure has no reason")
			}
			if artifactType != tt.wantType {
				t.Errorf("artifactType = %q, want %q", artifactType, tt.wantType)
			}
		})
	}
}

func TestTestdataReadmeListsEveryFixture(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("testdata", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "manifests", "*.json"))
	for _, file := range files {
		task, hexDigest, _ := strings.Cut(strings.TrimSuffix(filepath.Base(file), ".json"), "_sha256_")
		source := "quay.io/konflux-ci/tekton-catalog/" + task + "@sha256:" + hexDigest
		if !strings.Contains(string(readme), source) {
			t.Errorf("testdata/README.md does not record the source ref %s", source)
		}
	}
}
