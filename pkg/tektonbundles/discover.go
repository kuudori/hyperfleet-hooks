package tektonbundles

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
)

// catalogPrefix is the repository prefix of the Konflux Tekton task bundle catalog.
const catalogPrefix = "quay.io/konflux-ci/tekton-catalog/"

// location is one place a catalog reference appears.
type location struct {
	File  string
	Value string
	Line  int
}

func (l location) String() string {
	return fmt.Sprintf("%s:%d %s", l.File, l.Line, l.Value)
}

// pinnedRef is a unique digest-pinned catalog reference and everywhere it is used.
type pinnedRef struct {
	// Ref is the canonical reference: repository and digest, with any tag removed.
	Ref name.Digest
	// Task is the catalog repository name below the prefix, e.g. "task-init".
	Task      string
	Locations []location
}

// invalidPin is a reference that looks like an attempted digest pin but is malformed.
type invalidPin struct {
	Err      error
	Location location
}

// discovery is everything found in a directory of pipeline files.
type discovery struct {
	Files   []string
	Refs    []*pinnedRef
	Invalid []invalidPin
	// TagOnly counts catalog references without a digest; they are never fetched.
	TagOnly int
}

// discover reads every immediate *.yaml file in directory, in sorted order, and
// collects the catalog references found in YAML scalar values.
func discover(directory, prefix string) (*discovery, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	result := &discovery{}
	byRef := map[string]*pinnedRef{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		result.Files = append(result.Files, path)

		nodes, err := parseDocuments(path)
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			walkScalars(node, func(n *yaml.Node) {
				result.add(byRef, prefix, location{File: path, Line: n.Line, Value: n.Value})
			})
		}
	}

	for _, ref := range result.Refs {
		slices.SortFunc(ref.Locations, compareLocations)
	}
	slices.SortFunc(result.Refs, func(a, b *pinnedRef) int {
		return strings.Compare(a.Ref.Name(), b.Ref.Name())
	})
	return result, nil
}

// add classifies one scalar value and records it when it is a catalog reference.
func (d *discovery) add(byRef map[string]*pinnedRef, prefix string, loc location) {
	value := loc.Value
	// Only complete scalars count: descriptive text that merely starts with the
	// catalog URL is not a reference.
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) || strings.ContainsAny(value, " \t\r\n") {
		return
	}
	if !strings.Contains(value, "@") {
		d.TagOnly++
		return
	}

	ref, err := name.NewDigest(value)
	if err == nil && !strings.HasPrefix(ref.Name(), prefix) {
		err = fmt.Errorf("canonical reference %q is outside %s", ref.Name(), prefix)
	}
	if err != nil {
		d.Invalid = append(d.Invalid, invalidPin{Location: loc, Err: err})
		return
	}

	key := ref.Name()
	pinned, ok := byRef[key]
	if !ok {
		pinned = &pinnedRef{Ref: ref, Task: strings.TrimPrefix(ref.Context().Name(), prefix)}
		byRef[key] = pinned
		d.Refs = append(d.Refs, pinned)
	}
	pinned.Locations = append(pinned.Locations, loc)
}

// parseDocuments decodes every YAML document in the file.
func parseDocuments(path string) ([]*yaml.Node, error) {
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var documents []*yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			return documents, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		documents = append(documents, &node)
	}
}

// walkScalars calls visit for every scalar node under node. Alias nodes are not
// followed: the aliased value is visited once, at its anchor.
func walkScalars(node *yaml.Node, visit func(*yaml.Node)) {
	switch node.Kind {
	case yaml.AliasNode:
		return
	case yaml.ScalarNode:
		visit(node)
		return
	case yaml.DocumentNode, yaml.SequenceNode, yaml.MappingNode:
		for _, child := range node.Content {
			walkScalars(child, visit)
		}
	}
}

func compareLocations(a, b location) int {
	if c := strings.Compare(a.File, b.File); c != 0 {
		return c
	}
	if a.Line != b.Line {
		return cmp.Compare(a.Line, b.Line)
	}
	return strings.Compare(a.Value, b.Value)
}
