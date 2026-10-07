package tektonbundles

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// manifestFetcher returns the raw image manifest a pinned reference resolves to.
//
// An error wrapping *invalidManifestError means the registry answered with
// something that cannot be a Tekton bundle. Any other error means the manifest
// could not be fetched, which says nothing about what the digest denotes.
type manifestFetcher func(ctx context.Context, ref name.Digest) ([]byte, error)

// invalidManifestError reports a registry answer that is not a usable image manifest.
type invalidManifestError struct {
	Reason string
	// Raw is whatever the registry returned, kept so diagnostics can name its artifactType.
	Raw []byte
}

func (e *invalidManifestError) Error() string { return e.Reason }

// defaultBackoff gives three attempts in total: native go-containerregistry
// retries on 408/429/5xx and transport errors, never on 401 or 404.
var defaultBackoff = remote.Backoff{Duration: time.Second, Factor: 2, Jitter: 0.1, Steps: 3}

// registryFetcher fetches manifests anonymously by digest.
type registryFetcher struct {
	backoff remote.Backoff
}

// fetch resolves ref the way the Tekton bundle resolver's remote.Image does,
// but reads only manifests: a single image manifest is returned as is, and an
// index or manifest list is followed to its linux/amd64 child. Layer blobs are
// never downloaded.
func (f registryFetcher) fetch(ctx context.Context, ref name.Digest) ([]byte, error) {
	desc, err := remote.Get(ref,
		remote.WithContext(ctx),
		remote.WithAuth(authn.Anonymous),
		remote.WithRetryBackoff(f.backoff),
	)
	if err != nil {
		return nil, err
	}

	switch desc.MediaType {
	case types.OCIManifestSchema1, types.DockerManifestSchema2:
		return desc.Manifest, nil
	case types.OCIImageIndex, types.DockerManifestList:
		return fetchAMD64Child(desc)
	default:
		return nil, &invalidManifestError{
			Reason: fmt.Sprintf("unsupported manifest media type %q: not an image manifest or index", desc.MediaType),
			Raw:    desc.Manifest,
		}
	}
}

func fetchAMD64Child(desc *remote.Descriptor) ([]byte, error) {
	index, err := v1.ParseIndexManifest(bytes.NewReader(desc.Manifest))
	if err != nil {
		return nil, &invalidManifestError{
			Reason: "the index manifest is not valid JSON: " + err.Error(),
			Raw:    desc.Manifest,
		}
	}
	if !hasLinuxAMD64Child(index) {
		return nil, &invalidManifestError{
			Reason: "no child with platform linux/amd64 in index",
			Raw:    desc.Manifest,
		}
	}

	image, err := desc.Image()
	if err != nil {
		return nil, err
	}
	return image.RawManifest()
}

// hasLinuxAMD64Child mirrors the library's platform selection: a child without
// a platform is taken to be linux/amd64.
func hasLinuxAMD64Child(index *v1.IndexManifest) bool {
	for _, child := range index.Manifests {
		if child.Platform == nil || (child.Platform.OS == "linux" && child.Platform.Architecture == "amd64") {
			return true
		}
	}
	return false
}
