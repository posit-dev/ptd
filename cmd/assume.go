package main

import (
	"log/slog"
	"os"

	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(assumeAdminRoleCmd)
	assumeAdminRoleCmd.Flags().BoolVarP(&exportRoleCredentials, "export", "e", true, "Export the role credentials")
}

var exportRoleCredentials bool

var assumeAdminRoleCmd = &cobra.Command{
	Use:   "assume <target>",
	Short: "Assume the admin role in a <target> account",
	Long:  `Assume the admin role in a <target> account.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exportCredentialsForTarget(cmd, args[0], cmd.CalledAs())
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}

func exportCredentialsForTarget(cmd *cobra.Command, target string, calledAs string) {
	// find the relevant ptd.yaml file, load it.
	t, err := legacy.TargetFromName(target)
	if err != nil {
		slog.Error("Could not load relevant ptd.yaml file", "error", err)
		return
	}

	creds, err := t.Credentials(cmd.Context())
	if err != nil {
		slog.Error("Failed to assume role", "error", err)
		return
	}

	if t.CloudProvider() == "azure" {
		cmd.Printf("# Azure session: %s\n", creds.Identity())
		cmd.Printf("# Azure credentials are not exported, the `az` cli state is set instead.\n")
		return
	}

	// Print some helpful comments if we're in a terminal
	if term.IsTerminal(int(os.Stdout.Fd())) {
		cmd.Printf("# Exporting session for %s\n", creds.Identity())
		cmd.Printf("# In order to use this directly, run:\n")
		cmd.Printf("# eval $(ptd assume %s)\n", calledAs)
	}

	var exportString string
	if exportRoleCredentials {
		exportString = "export "
	}

	// Print the export commands
	for k, v := range creds.EnvVars() {
		cmd.Printf("%s%s=%s\n", exportString, k, v)
	}
}
