package tektonbundles

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func writePipeline(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestNewCommand_Flags(t *testing.T) {
	cmd := NewCommand()

	require.Equal(t, "verify-tekton-bundles", cmd.Name())
	flag := cmd.Flags().Lookup("directory")
	require.NotNil(t, flag)
	require.Equal(t, ".tekton", flag.DefValue)
	require.Nil(t, cmd.Flags().Lookup("author"), "the command must not know about authors")
	require.Nil(t, cmd.Flags().Lookup("pr"), "the command must not know about pull requests")
}

func TestRun_DefaultDirectoryIsDotTekton(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".tekton"), 0o750))
	writePipeline(t, filepath.Join(root, ".tekton"), "p.yaml", "kind: Pipeline\n")
	t.Chdir(root)

	out, err := run(t)

	require.NoError(t, err)
	require.Contains(t, out, "files scanned:   1")
}

func TestRun_UsesRequestedDirectory(t *testing.T) {
	dir := t.TempDir()
	writePipeline(t, dir, "a.yaml", "kind: Pipeline\n")
	writePipeline(t, dir, "b.yaml", "kind: Pipeline\n")

	out, err := run(t, "--directory", dir)

	require.NoError(t, err)
	require.Contains(t, out, "files scanned:   2")
}

func TestRun_ExplicitZeroPinResultSucceeds(t *testing.T) {
	dir := t.TempDir()
	writePipeline(t, dir, "p.yaml", "ref: quay.io/konflux-ci/tekton-catalog/task-init:0.2\n")

	out, err := run(t, "--directory", dir)

	require.NoError(t, err)
	require.Contains(t, out, "verify-tekton-bundles: OK")
	require.Contains(t, out, "unique pins:     0")
	require.Contains(t, out, "tag-only refs:   1 (not fetched)")
	require.Contains(t, out, "nothing to validate")
}

func TestRun_MissingDirectoryFails(t *testing.T) {
	_, err := run(t, "--directory", filepath.Join(t.TempDir(), "absent"))

	require.Error(t, err)
}

func TestRun_PartialFailureReturnsNonZero(t *testing.T) {
	dir := t.TempDir()
	// A malformed pin fails offline, with no registry involved.
	writePipeline(t, dir, "p.yaml", "ref: quay.io/konflux-ci/tekton-catalog/task-init:0.2@sha256:nothex\n")

	out, err := run(t, "--directory", dir)

	require.Error(t, err)
	require.Contains(t, err.Error(), "1 of 1 pinned Tekton bundle references failed verification")
	require.Contains(t, out, "invalid pinned reference")
	require.Contains(t, out, "p.yaml:1 quay.io/konflux-ci/tekton-catalog/task-init:0.2@sha256:nothex")
}

func TestRun_RejectsPositionalArguments(t *testing.T) {
	_, err := run(t, ".tekton/*.yaml")

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "accepts 0 arg"))
}
