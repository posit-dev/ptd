package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/attestation"
	"github.com/spf13/cobra"
)

var attestationOutputFile string
var attestationFormat string

func init() {
	rootCmd.AddCommand(attestationCmd)

	attestationCmd.Flags().StringVarP(&attestationOutputFile, "output", "o", "", "Output file path (default: attestation-<target>-<date>.<ext>)")
	attestationCmd.Flags().StringVar(&attestationFormat, "format", "both", "Output format: markdown, pdf, or both")
}

var attestationCmd = &cobra.Command{
	Use:   "attestation <target>",
	Short: "Generate an installation attestation document",
	Long: `Generate an installation attestation document for a workload, including product versions, infrastructure resources, and Pulumi state references.

NOTE: This command is experimental. Output format and content are subject to change.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]
		runAttestation(cmd, target)
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}

func runAttestation(cmd *cobra.Command, targetName string) {
	ctx := cmd.Context()

	// Load target
	t, err := legacy.TargetFromName(targetName)
	if err != nil {
		slog.Error("Could not load target", "error", err)
		return
	}

	workloadPath := legacy.WorkloadPathFromTargetName(targetName)

	slog.Info("Collecting attestation data", "target", targetName)

	// Collect attestation data
	data, err := attestation.Collect(ctx, t, workloadPath)
	if err != nil {
		slog.Error("Failed to collect attestation data", "error", err)
		return
	}

	dateStr := time.Now().Format("2006-01-02")
	baseName := fmt.Sprintf("attestation-%s-%s", targetName, dateStr)

	switch attestationFormat {
	case "markdown", "md":
		if err := writeMarkdown(data, baseName); err != nil {
			slog.Error("Failed to write markdown", "error", err)
			return
		}
	case "pdf":
		if err := writePDF(data, baseName); err != nil {
			slog.Error("Failed to write PDF", "error", err)
			return
		}
	case "both":
		if err := writeMarkdown(data, baseName); err != nil {
			slog.Error("Failed to write markdown", "error", err)
			return
		}
		if err := writePDF(data, baseName); err != nil {
			slog.Error("Failed to write PDF", "error", err)
			return
		}
	default:
		slog.Error("Invalid format", "format", attestationFormat, "valid", "markdown, pdf, both")
		return
	}
}

func writeMarkdown(data *attestation.AttestationData, baseName string) error {
	outputPath := attestationOutputFile
	if outputPath == "" {
		outputPath = baseName + ".md"
	} else if attestationFormat == "both" {
		// When writing both formats with explicit output, derive md path
		ext := filepath.Ext(outputPath)
		outputPath = outputPath[:len(outputPath)-len(ext)] + ".md"
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if err := attestation.RenderMarkdown(f, data); err != nil {
		return fmt.Errorf("failed to render markdown: %w", err)
	}

	slog.Info("Wrote markdown attestation", "path", outputPath)
	return nil
}

func writePDF(data *attestation.AttestationData, baseName string) error {
	outputPath := attestationOutputFile
	if outputPath == "" {
		outputPath = baseName + ".pdf"
	} else if attestationFormat == "both" {
		ext := filepath.Ext(outputPath)
		outputPath = outputPath[:len(outputPath)-len(ext)] + ".pdf"
	}

	if err := attestation.RenderPDF(outputPath, data); err != nil {
		return fmt.Errorf("failed to render PDF: %w", err)
	}

	slog.Info("Wrote PDF attestation", "path", outputPath)
	return nil
}
