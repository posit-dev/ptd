package eject

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/posit-dev/ptd/lib/attestation"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

type ConfigLoaderFunc func(types.Target) (interface{}, error)

type HandoffCollectorFunc func(ctx context.Context, t types.Target, opts Options, crDetails *ControlRoomDetails) error

type Options struct {
	TargetName        string
	OutputDir         string
	DryRun            bool
	CLIVersion        string
	WorkloadPath      string
	ControlRoomTarget types.Target     // needed to delete Mimir secret during eject
	ConfigLoader      ConfigLoaderFunc // nil defaults to helpers.ConfigForTarget
	HandoffCollector  HandoffCollectorFunc
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

func (o *Options) ptdYamlPath() string {
	return filepath.Join(o.WorkloadPath, "ptd.yaml")
}

func (o *Options) handoffCollector() HandoffCollectorFunc {
	if o.HandoffCollector != nil {
		return o.HandoffCollector
	}
	return collectAndRenderHandoff
}

// EjectRecord is written after eject completes to capture what was done.
type EjectRecord struct {
	EjectedAt           string               `json:"ejected_at"`
	ControlRoomSnapshot *ControlRoomSnapshot `json:"control_room_snapshot"`
	MimirSecretRemoved  bool                 `json:"mimir_secret_removed"`
	ConfigStripped      bool                 `json:"config_stripped"`
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

	// --- Pre-flight checks (eject only) ---
	if !opts.DryRun {
		pfResult, err := RunPreflightChecks(ctx, t, PreflightOptions{
			Config:            config,
			ControlRoomTarget: opts.ControlRoomTarget,
		})
		if err != nil {
			return fmt.Errorf("preflight checks failed: %w", err)
		}
		if !pfResult.Passed {
			return fmt.Errorf("preflight checks did not pass; aborting eject")
		}
	}

	// --- Bundle generation (always) ---
	controlRoomName := ""
	if opts.ControlRoomTarget != nil {
		controlRoomName = opts.ControlRoomTarget.Name()
	}
	crDetails, err := CollectControlRoomDetails(config, t.Name(), controlRoomName)
	if err != nil {
		return fmt.Errorf("failed to collect control room details: %w", err)
	}
	bundle.ControlRoom = crDetails
	slog.Info("Collected control room details",
		"account_id", crDetails.AccountID,
		"domain", crDetails.Domain,
		"connections", len(crDetails.Connections),
	)

	if opts.WorkloadPath != "" {
		if err := CopyWorkloadConfig(opts.WorkloadPath, opts.OutputDir); err != nil {
			return fmt.Errorf("failed to copy workload config: %w", err)
		}
		slog.Info("Copied workload config", "from", opts.WorkloadPath)
	}

	metadata, err := CollectMetadata(config, opts, time.Now())
	if err != nil {
		return fmt.Errorf("failed to collect metadata: %w", err)
	}
	if err := WriteMetadata(metadata, opts.OutputDir); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}
	slog.Info("Wrote metadata.json")

	hasConfig := opts.WorkloadPath != ""
	if err := WriteReadme(metadata, hasConfig, opts.OutputDir); err != nil {
		return fmt.Errorf("failed to write README: %w", err)
	}
	slog.Info("Wrote README.md")

	if err := WriteRemoveAccessRunbook(opts.OutputDir, crDetails, opts.TargetName, string(t.CloudProvider())); err != nil {
		return fmt.Errorf("failed to write remove-posit-access runbook: %w", err)
	}

	if err := opts.handoffCollector()(ctx, t, opts, crDetails); err != nil {
		return err
	}

	slog.Info("Eject bundle generated", "path", opts.OutputDir)

	// --- Eject (non-dry-run only) ---
	if opts.DryRun {
		slog.Info("Dry run complete. To proceed with eject, re-run with --dry-run=false")
		return nil
	}

	return runEjectSteps(ctx, t, opts, crDetails)
}

func runEjectSteps(ctx context.Context, t types.Target, opts Options, crDetails *ControlRoomDetails) error {
	slog.Info("Starting control room disconnection", "target", opts.TargetName)

	// Step 1: Snapshot control room config before stripping
	ptdYaml := opts.ptdYamlPath()
	snapshot, err := SnapshotControlRoomFields(ptdYaml)
	if err != nil {
		return fmt.Errorf("failed to snapshot control room fields: %w", err)
	}
	slog.Info("Snapshotted control room config", "fields", len(snapshot.Fields))

	// Step 2: Strip control room fields from ptd.yaml
	if err := StripControlRoomFields(ptdYaml); err != nil {
		return fmt.Errorf("failed to strip control room fields: %w", err)
	}
	slog.Info("Stripped control room fields from ptd.yaml")

	// Step 3: Delete Mimir password from control room
	mimirRemoved := false
	if opts.ControlRoomTarget != nil {
		if err := RemoveWorkloadMimirPassword(ctx, opts.ControlRoomTarget, opts.TargetName); err != nil {
			slog.Error("Failed to remove Mimir password from control room — the workload config has already been stripped. "+
				"The orphaned secret can be cleaned up manually or by re-running eject.",
				"error", err)
			// Continue — config is already stripped, this is a recoverable partial failure
		} else {
			mimirRemoved = true
		}
	} else {
		slog.Warn("No control room target available; skipping Mimir password removal")
	}

	// Step 4: Write eject record
	record := EjectRecord{
		EjectedAt:           time.Now().UTC().Format(time.RFC3339),
		ControlRoomSnapshot: snapshot,
		MimirSecretRemoved:  mimirRemoved,
		ConfigStripped:      true,
	}
	if err := writeEjectRecord(record, opts.OutputDir); err != nil {
		return fmt.Errorf("failed to write eject record: %w", err)
	}

	// --- Summary ---
	slog.Info("Eject complete",
		"target", opts.TargetName,
		"config_stripped", true,
		"mimir_secret_removed", mimirRemoved,
		"snapshot_fields", len(snapshot.Fields),
	)
	slog.Info("Next steps: the new owners should run 'ptd ensure' to converge infrastructure to the disconnected state")

	return nil
}

func writeEjectRecord(record EjectRecord, outputDir string) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal eject record: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(outputDir, "eject-record.json"), data, 0600)
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
