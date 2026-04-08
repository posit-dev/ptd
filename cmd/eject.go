package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/eject"
	"github.com/posit-dev/ptd/lib/types"
	"github.com/spf13/cobra"
)

var ejectDryRun bool
var ejectOutputDir string

func init() {
	rootCmd.AddCommand(ejectCmd)
	ejectCmd.Flags().BoolVar(&ejectDryRun, "dry-run", true, "Generate artifacts only; change nothing")
	ejectCmd.Flags().StringVar(&ejectOutputDir, "output-dir", "", "Output directory for eject artifacts (default: ./eject-<target>/)")
}

var ejectCmd = &cobra.Command{
	Use:   "eject <target>",
	Short: "Generate a customer infrastructure handoff bundle",
	Long: `Generate everything a customer needs to operate their PTD-managed infrastructure independently.

Produces an artifact bundle with configuration files, Pulumi state exports, resource inventory,
secret references, and operational documentation.

By default runs in dry-run mode (--dry-run=true), which generates the full artifact bundle
without modifying any infrastructure. Use --dry-run=false to proceed with control room severance.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runEject(cmd, args[0])
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}

func runEject(cmd *cobra.Command, targetName string) {
	ctx := cmd.Context()

	// Load target
	t, err := legacy.TargetFromName(targetName)
	if err != nil {
		slog.Error("Could not load target", "error", err)
		return
	}

	// Reject control room targets — eject only applies to workloads
	if t.Type() == types.TargetTypeControlRoom {
		slog.Error("Cannot eject a control room target; eject only applies to workloads", "target", targetName)
		return
	}

	// Determine output directory
	outputDir := ejectOutputDir
	if outputDir == "" {
		outputDir = fmt.Sprintf("./eject-%s/", targetName)
	}

	// Safety confirmation for non-dry-run mode
	if !ejectDryRun {
		fmt.Printf("WARNING: Running eject with --dry-run=false will proceed with control room severance.\n")
		fmt.Printf("Type the full target name (%s) to confirm: ", targetName)

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			slog.Error("Failed to read confirmation", "error", err)
			return
		}

		response = strings.TrimSpace(response)
		if response != targetName {
			slog.Error("Confirmation did not match target name; aborting", "expected", targetName, "got", response)
			return
		}

		slog.Info("Confirmation accepted, proceeding with eject")
	}

	opts := eject.Options{
		TargetName: targetName,
		OutputDir:  outputDir,
		DryRun:     ejectDryRun,
	}

	if err := eject.Run(ctx, t, opts); err != nil {
		slog.Error("Eject failed", "error", err)
		return
	}

	slog.Info("Eject complete", "target", targetName, "output-dir", outputDir, "dry-run", ejectDryRun)
}
