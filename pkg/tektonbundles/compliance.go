package tektonbundles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The compliance rules in this file are a port of checkImageCompliance from the
// Tekton Pipelines bundle resolver (pkg/resolution/resolver/bundle/bundle.go,
// v1.7.0, https://github.com/tektoncd/pipeline), which is licensed under the
// Apache License, Version 2.0. The function and MaximumBundleObjects are
// identical in v0.59.0, v0.68.0, v1.0.0, v1.3.0, v1.7.0 and main (checked
// October 2026), so a manifest passes here exactly when it passes the
// resolver's compliance check. The resolver's later taskRef name/kind lookup is
// not part of that check.
const (
	// maximumBundleObjects mirrors the resolver's MaximumBundleObjects.
	maximumBundleObjects = 20

	annotationAPIVersion = "dev.tekton.image.apiVersion"
	annotationName       = "dev.tekton.image.name"
	annotationKind       = "dev.tekton.image.kind"

	artifactTypeAbsent = "<absent>"
)

// bundleAnnotations lists the annotations every layer must carry, in the order
// the resolver checks them.
var bundleAnnotations = []string{annotationAPIVersion, annotationName, annotationKind}

// complianceFailure describes why a manifest is not a Tekton bundle.
type complianceFailure struct {
	// Reason is the first failed check, in the resolver's wording.
	Reason string
	// AlsoMissing lists the other required annotations missing from the failing layer.
	AlsoMissing []string
	// Layer is the index of the failing layer when AlsoMissing is set.
	Layer int
}

// bundleManifest is the part of an OCI/Docker image manifest that the check reads.
type bundleManifest struct {
	Layers []struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"layers"`
}

// checkCompliance applies the resolver's compliance rules to a raw image
// manifest. It returns the manifest's artifactType (artifactTypeAbsent when it
// has none) and nil when the manifest is compliant. Malformed or wrongly typed
// JSON is a failure, never a panic.
func checkCompliance(raw []byte) (string, *complianceFailure) {
	artifactType := artifactTypeOf(raw)

	var manifest bundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return artifactType, &complianceFailure{Reason: "the manifest is not valid image manifest JSON: " + err.Error()}
	}
	if len(manifest.Layers) == 0 {
		return artifactType, &complianceFailure{Reason: "the manifest has no layers, so it contains no Tekton objects"}
	}
	return artifactType, checkLayers(&manifest)
}

func checkLayers(manifest *bundleManifest) *complianceFailure {
	if len(manifest.Layers) > maximumBundleObjects {
		return &complianceFailure{
			Reason: fmt.Sprintf("contained more than the maximum %d allow objects", maximumBundleObjects),
		}
	}

	for i, layer := range manifest.Layers {
		var missing []string
		for _, key := range bundleAnnotations {
			if _, ok := layer.Annotations[key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			return &complianceFailure{
				Reason:      fmt.Sprintf("the layer %v does not contain a %s annotation", i, missing[0]),
				AlsoMissing: missing[1:],
				Layer:       i,
			}
		}

		if kind := layer.Annotations[annotationKind]; strings.TrimSuffix(strings.ToLower(kind), "s") != kind {
			return &complianceFailure{
				Reason: fmt.Sprintf("the layer %v the annotation %s must be lowercased and singular, found %s",
					i, annotationKind, kind),
			}
		}
	}
	return nil
}

// artifactTypeOf returns the manifest's artifactType, leniently: it works on
// manifests that fail the strict parse above, so diagnostics can still name it.
func artifactTypeOf(raw []byte) string {
	var top struct {
		ArtifactType json.RawMessage `json:"artifactType"`
	}
	if err := json.Unmarshal(raw, &top); err != nil || len(top.ArtifactType) == 0 {
		return artifactTypeAbsent
	}
	if bytes.Equal(top.ArtifactType, []byte("null")) {
		return artifactTypeAbsent
	}

	var value string
	if err := json.Unmarshal(top.ArtifactType, &value); err != nil {
		return "<invalid: " + string(top.ArtifactType) + ">"
	}
	if value == "" {
		return artifactTypeAbsent
	}
	return value
}
