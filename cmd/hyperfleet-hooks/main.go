package main

import (
	"os"

	"github.com/openshift-hyperfleet/hyperfleet-hooks/cmd/hyperfleet-hooks/commitlint"
	"github.com/openshift-hyperfleet/hyperfleet-hooks/cmd/hyperfleet-hooks/tektonbundles"
	"github.com/openshift-hyperfleet/hyperfleet-hooks/cmd/hyperfleet-hooks/version"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "hyperfleet-hooks",
	Short:        "HyperFleet pre-commit hooks validator",
	Long:         "hyperfleet-hooks validates artifacts against HyperFleet project standards.",
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(version.NewCommand())
	rootCmd.AddCommand(commitlint.NewCommand())
	rootCmd.AddCommand(tektonbundles.NewCommand())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
