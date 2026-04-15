package eject

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/posit-dev/ptd/lib/attestation"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

type ConfigLoaderFunc func(types.Target) (interface{}, error)

type Options struct {
	TargetName   string
	OutputDir    string
	DryRun       bool
	WorkloadPath string
	ConfigLoader ConfigLoaderFunc // nil defaults to helpers.ConfigForTarget
}

type Bundle struct {
	ControlRoom *ControlRoomDetails `json:"control_room"`
}

func (o *Options) configLoader() ConfigLoaderFunc {
	if o.ConfigLoader != nil {
		return o.ConfigLoader
	}
	return helpers.ConfigForTarget
}

func Run(ctx context.Context, t types.Target, opts Options) error {
	slog.Info("Starting eject", "target", opts.TargetName, "output-dir", opts.OutputDir, "dry-run", opts.DryRun)

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	bundle := &Bundle{}

	config, err := opts.configLoader()(t)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	crDetails, err := CollectControlRoomDetails(config, t.Name())
	if err != nil {
		return fmt.Errorf("failed to collect control room details: %w", err)
	}
	bundle.ControlRoom = crDetails
	slog.Info("Collected control room details",
		"account_id", crDetails.AccountID,
		"domain", crDetails.Domain,
		"connections", len(crDetails.Connections),
	)

	if err := collectAndRenderHandoff(ctx, t, opts, crDetails); err != nil {
		return err
	}

	slog.Info("Eject bundle generated", "path", opts.OutputDir)
	return nil
}

func collectAndRenderHandoff(ctx context.Context, t types.Target, opts Options, crDetails *ControlRoomDetails) error {
	if opts.WorkloadPath == "" {
		slog.Warn("WorkloadPath not set, skipping handoff document generation")
		return nil
	}

	slog.Info("Collecting infrastructure data")
	attData, err := attestation.Collect(ctx, t, opts.WorkloadPath)
	if err != nil {
		return fmt.Errorf("failed to collect infrastructure data: %w", err)
	}
	slog.Info("Collected infrastructure data",
		"sites", len(attData.Sites),
		"stacks", len(attData.Stacks),
	)

	slog.Info("Building resource inventory")
	var allResources []ResourceInventoryEntry
	for key, data := range attData.RawStateFiles {
		entries, err := ParseResourceInventory(data, key)
		if err != nil {
			slog.Warn("Failed to parse resource inventory for state file", "key", key, "error", err)
			continue
		}
		allResources = append(allResources, entries...)
	}
	slog.Info("Built resource inventory", "resources", len(allResources))

	secrets := EnumerateSecrets(t)
	slog.Info("Enumerated secret references", "secrets", len(secrets))

	handoff := &HandoffData{
		AttestationData: attData,
		ControlRoom:     crDetails,
		Resources:       allResources,
		Secrets:         secrets,
		DryRun:          opts.DryRun,
	}

	sort.Slice(handoff.Stacks, func(i, j int) bool {
		return attestation.StackOrder(handoff.Stacks[i].ProjectName) < attestation.StackOrder(handoff.Stacks[j].ProjectName)
	})

	baseName := fmt.Sprintf("%s_handoff", opts.TargetName)
	pdfPath := filepath.Join(opts.OutputDir, baseName+".pdf")
	mdPath := filepath.Join(opts.OutputDir, baseName+".md")

	slog.Info("Rendering handoff PDF", "path", pdfPath)
	if err := RenderHandoffPDF(pdfPath, handoff); err != nil {
		return fmt.Errorf("failed to render handoff PDF: %w", err)
	}

	slog.Info("Rendering handoff markdown", "path", mdPath)
	mdFile, err := os.Create(mdPath)
	if err != nil {
		return fmt.Errorf("failed to create markdown file: %w", err)
	}
	defer mdFile.Close()

	if err := RenderHandoffMarkdown(mdFile, handoff); err != nil {
		return fmt.Errorf("failed to render handoff markdown: %w", err)
	}

	return nil
}
