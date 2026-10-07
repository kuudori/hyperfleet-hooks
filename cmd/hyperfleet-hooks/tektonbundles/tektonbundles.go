package tektonbundles

import (
	"github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/tektonbundles"
	"github.com/spf13/cobra"
)

const defaultDirectory = ".tekton"

// NewCommand creates the verify-tekton-bundles command
func NewCommand() *cobra.Command {
	var directory string

	cmd := &cobra.Command{
		Use:   "verify-tekton-bundles",
		Short: "Verify pinned Konflux Tekton task bundle digests",
		Long: `Verify that every digest-pinned quay.io/konflux-ci/tekton-catalog/ reference in
the *.yaml files of a directory resolves to a Tekton bundle.

Each unique repository and digest is fetched once, anonymously, by digest only,
and checked with the same compliance rules the Tekton bundle resolver applies at
PipelineRun time. Tag-only references are counted but not fetched. The command
exits non-zero if any pin is not a valid bundle, cannot be fetched after retries,
or if the directory or a file cannot be read.

Examples:
  # Check the .tekton directory of the current repository
  hyperfleet-hooks verify-tekton-bundles

  # Check another directory
  hyperfleet-hooks verify-tekton-bundles --directory path/to/.tekton`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tektonbundles.Verify(cmd.Context(), directory, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&directory, "directory", defaultDirectory,
		"Directory holding the pipeline *.yaml files to check")

	return cmd
}
