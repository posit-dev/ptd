package main

import (
	"log/slog"

	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(hashCmd)
}

var hashCmd = &cobra.Command{
	Use:   "hash <target>",
	Short: "Return a stable hash value for a target name.",
	Long:  `Return a stable hash value for a target name. This is useful for generating unique identifiers based on target names.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// find the relevant ptd.yaml file, load it.
		t, err := legacy.TargetFromName(args[0])
		if err != nil {
			slog.Error("Could not load relevant ptd.yaml file", "error", err)
			return
		}

		cmd.Printf("%s\n", t.HashName())
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}
