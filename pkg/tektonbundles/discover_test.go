package tektonbundles

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func hex64(c string) string { return strings.Repeat(c, 64) }

func pin(task, tag, hexChar string) string {
	ref := catalogPrefix + task
	if tag != "" {
		ref += ":" + tag
	}
	return ref + "@sha256:" + hex64(hexChar)
}

func bundleParam(value string) string {
	return "        - name: bundle\n          value: " + value + "\n"
}

func TestDiscover(t *testing.T) {
	scan := catalogPrefix + taskScan

	tests := []struct {
		files       map[string]string
		name        string
		wantRefs    []string
		wantFiles   []string
		wantInvalid int
		wantTagOnly int
	}{
		{
			name: "push and tag pipelines, finally and several documents",
			files: map[string]string{
				"b-push.yaml": "spec:\n  tasks:\n" + bundleParam(pin("task-init", "0.2", "a")) +
					"  finally:\n" + bundleParam(pin("task-show-sbom", "0.1", "b")) +
					"---\nother:\n" + bundleParam(pin("task-clone", "0.1", "c")),
				"a-tag.yaml": "spec:\n  tasks:\n" + bundleParam(pin("task-init", "0.2", "a")),
			},
			wantRefs: []string{
				catalogPrefix + "task-clone@sha256:" + hex64("c"),
				catalogPrefix + "task-init@sha256:" + hex64("a"),
				catalogPrefix + "task-show-sbom@sha256:" + hex64("b"),
			},
			wantFiles: []string{"a-tag.yaml", "b-push.yaml"},
		},
		{
			name: "quoted scalars of every style",
			files: map[string]string{"p.yaml": "a: \"" + pin("task-a", "1", "a") + "\"\n" +
				"b: '" + pin("task-b", "1", "b") + "'\n" +
				"c: |-\n  " + pin("task-c", "1", "c") + "\n"},
			wantRefs: []string{
				catalogPrefix + "task-a@sha256:" + hex64("a"),
				catalogPrefix + "task-b@sha256:" + hex64("b"),
				catalogPrefix + "task-c@sha256:" + hex64("c"),
			},
			wantFiles: []string{"p.yaml"},
		},
		{
			name: "a value behind an alias is counted once, at its anchor",
			files: map[string]string{"p.yaml": "first:\n  value: &pin " + pin("task-a", "1", "a") + "\n" +
				"second:\n  value: *pin\nthird: *pin\n"},
			wantRefs:  []string{catalogPrefix + "task-a@sha256:" + hex64("a")},
			wantFiles: []string{"p.yaml"},
		},
		{
			name: "comments, documentation and other registries are not references",
			files: map[string]string{"p.yaml": "# " + pin("task-commented", "1", "a") + "\n" +
				"description: " + scan + " is documented at https://quay.io/konflux-ci/tekton-catalog/x\n" +
				"url: https://" + scan + "\n" +
				"other: quay.io/konflux-ci/other-catalog/task-x@sha256:" + hex64("b") + "\n" +
				"docker: docker.io/library/busybox@sha256:" + hex64("c") + "\n" +
				"bare: " + catalogPrefix + "\n"},
			wantFiles: []string{"p.yaml"},
		},
		{
			name:  "tag-only references are counted and never become refs",
			files: map[string]string{"p.yaml": "a: " + scan + ":0.2\nb: " + scan + "\nc: " + pin("task-a", "1", "a") + "\n"},
			wantRefs: []string{
				catalogPrefix + "task-a@sha256:" + hex64("a"),
			},
			wantFiles:   []string{"p.yaml"},
			wantTagOnly: 2,
		},
		{
			name: "a tagged digest and the same digest untagged are one ref",
			files: map[string]string{
				"a.yaml": "a: " + pin("task-a", "1.0", "a") + "\n",
				"b.yaml": "a: " + pin("task-a", "", "a") + "\nb: " + pin("task-a", "2.0", "a") + "\n",
			},
			wantRefs:  []string{catalogPrefix + "task-a@sha256:" + hex64("a")},
			wantFiles: []string{"a.yaml", "b.yaml"},
		},
		{
			name:  "distinct repositories sharing a hash stay distinct",
			files: map[string]string{"p.yaml": "a: " + pin("task-a", "", "a") + "\nb: " + pin("task-b", "", "a") + "\n"},
			wantRefs: []string{
				catalogPrefix + "task-a@sha256:" + hex64("a"),
				catalogPrefix + "task-b@sha256:" + hex64("a"),
			},
			wantFiles: []string{"p.yaml"},
		},
		{
			name: "invalid attempted pins are reported, valid ones still found",
			files: map[string]string{"p.yaml": "short: " + scan + ":0.2@sha256:abc\n" +
				"algo: " + scan + "@sha512:" + hex64("a") + "\n" +
				"empty: " + scan + "@\n" +
				"upper: " + catalogPrefix + "Task-Upper@sha256:" + hex64("a") + "\n" +
				"ok: " + pin("task-ok", "1", "b") + "\n"},
			wantRefs:    []string{catalogPrefix + "task-ok@sha256:" + hex64("b")},
			wantFiles:   []string{"p.yaml"},
			wantInvalid: 4,
		},
		{
			name: "only immediate .yaml files are read",
			files: map[string]string{
				"p.yaml":         "a: " + pin("task-a", "", "a") + "\n",
				"q.yml":          "a: " + pin("task-yml", "", "b") + "\n",
				"r.yaml.bak":     "a: " + pin("task-bak", "", "c") + "\n",
				"nested/s.yaml":  "a: " + pin("task-nested", "", "d") + "\n",
				"README.md":      pin("task-md", "", "e") + "\n",
				"dir.yaml/x.txt": "ignored",
			},
			wantRefs:  []string{catalogPrefix + "task-a@sha256:" + hex64("a")},
			wantFiles: []string{"p.yaml"},
		},
		{
			name:      "no pins at all is an explicit empty result",
			files:     map[string]string{"p.yaml": "kind: Pipeline\n", "empty.yaml": ""},
			wantFiles: []string{"empty.yaml", "p.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tt.files {
				path := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := discover(dir, catalogPrefix)
			if err != nil {
				t.Fatalf("discover: %v", err)
			}

			var gotRefs []string
			for _, ref := range got.Refs {
				gotRefs = append(gotRefs, ref.Ref.Name())
			}
			if !slices.Equal(gotRefs, tt.wantRefs) {
				t.Errorf("refs = %v, want %v", gotRefs, tt.wantRefs)
			}
			var gotFiles []string
			for _, file := range got.Files {
				gotFiles = append(gotFiles, filepath.Base(file))
			}
			if !slices.Equal(gotFiles, tt.wantFiles) {
				t.Errorf("files = %v, want %v", gotFiles, tt.wantFiles)
			}
			if len(got.Invalid) != tt.wantInvalid {
				t.Errorf("invalid pins = %d (%v), want %d", len(got.Invalid), got.Invalid, tt.wantInvalid)
			}
			if got.TagOnly != tt.wantTagOnly {
				t.Errorf("tag-only = %d, want %d", got.TagOnly, tt.wantTagOnly)
			}
		})
	}
}

func TestDiscover_KeepsEverySourceLocationInOrder(t *testing.T) {
	dir := t.TempDir()
	tagged := pin("task-a", "1.0", "a")
	untagged := pin("task-a", "", "a")
	writeFile(t, dir, "b.yaml", "x: 1\ny: "+untagged+"\n")
	writeFile(t, dir, "a.yaml", "x: 1\nz: 2\nvalue: "+tagged+"\nagain: \""+untagged+"\"\n")

	got, err := discover(dir, catalogPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Refs) != 1 {
		t.Fatalf("got %d refs, want 1", len(got.Refs))
	}
	if got.Refs[0].Task != "task-a" {
		t.Errorf("task = %q, want task-a", got.Refs[0].Task)
	}

	want := []location{
		{File: filepath.Join(dir, "a.yaml"), Line: 3, Value: tagged},
		{File: filepath.Join(dir, "a.yaml"), Line: 4, Value: untagged},
		{File: filepath.Join(dir, "b.yaml"), Line: 2, Value: untagged},
	}
	if !slices.Equal(got.Refs[0].Locations, want) {
		t.Errorf("locations =\n%v\nwant\n%v", got.Refs[0].Locations, want)
	}
}

func TestDiscover_Errors(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		if _, err := discover(filepath.Join(t.TempDir(), "absent"), catalogPrefix); err == nil {
			t.Fatal("want an error for a missing directory")
		}
	})

	t.Run("directory is a file", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "file", "x")
		if _, err := discover(path, catalogPrefix); err == nil {
			t.Fatal("want an error when the directory is a file")
		}
	})

	t.Run("malformed YAML", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "bad.yaml", "a: [unterminated\n")
		_, err := discover(dir, catalogPrefix)
		if err == nil || !strings.Contains(err.Error(), "bad.yaml") {
			t.Fatalf("want a parse error naming the file, got %v", err)
		}
	})

	t.Run("malformed second document", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "bad.yaml", "a: 1\n---\nb: [\n")
		if _, err := discover(dir, catalogPrefix); err == nil {
			t.Fatal("want a parse error for the second document")
		}
	})

	t.Run("unreadable file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can read anything")
		}
		dir := t.TempDir()
		path := writeFile(t, dir, "locked.yaml", "a: 1\n")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := discover(dir, catalogPrefix); err == nil {
			t.Fatal("want a read error")
		}
	})
}

func TestDiscover_AliasedValueHasOneLocation(t *testing.T) {
	dir := t.TempDir()
	ref := pin("task-a", "1", "a")
	writeFile(t, dir, "p.yaml", "first:\n  value: &pin "+ref+"\nsecond:\n  value: *pin\nthird: *pin\n")

	got, err := discover(dir, catalogPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Refs) != 1 || len(got.Refs[0].Locations) != 1 {
		t.Fatalf("want one ref with one location, got %+v", got.Refs)
	}
	if line := got.Refs[0].Locations[0].Line; line != 2 {
		t.Errorf("location line = %d, want the anchor line 2", line)
	}
}
