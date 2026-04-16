package eject

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

type ConfigLoaderFunc func(types.Target) (interface{}, error)

type Options struct {
	TargetName   string
	OutputDir    string
	DryRun       bool
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

	rbData, err := buildRunbookData(config, opts.TargetName)
	if err != nil {
		return fmt.Errorf("failed to build runbook data: %w", err)
	}

	runbooks, err := GenerateRunbooks(rbData)
	if err != nil {
		return fmt.Errorf("failed to generate runbooks: %w", err)
	}

	runbooksDir := filepath.Join(opts.OutputDir, "runbooks")
	if err := os.MkdirAll(runbooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create runbooks directory: %w", err)
	}

	for filename, content := range runbooks {
		path := filepath.Join(runbooksDir, filename)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write runbook %s: %w", filename, err)
		}
		slog.Info("Generated runbook", "path", path)
	}

	slog.Info("Eject bundle generated", "path", opts.OutputDir)
	return nil
}

func buildRunbookData(config interface{}, targetName string) (*RunbookData, error) {
	data := &RunbookData{WorkloadName: targetName}

	switch cfg := config.(type) {
	case types.AWSWorkloadConfig:
		data.Cloud = "aws"
		data.Region = cfg.Region
		data.ClusterName = awsClusterName(targetName, cfg.Clusters)
		data.Sites = sortedSites(cfg.Sites)
	case types.AzureWorkloadConfig:
		data.Cloud = "azure"
		data.Region = cfg.Region
		data.ResourceGroup = fmt.Sprintf("rsg-ptd-%s", sanitizeName(targetName))
		data.ClusterName = azureClusterName(targetName, cfg.Clusters)
		data.Sites = sortedSites(cfg.Sites)
	default:
		return nil, fmt.Errorf("unsupported config type for target %s", targetName)
	}

	return data, nil
}

func sortedSites(sites map[string]types.SiteConfig) []SiteData {
	names := slices.Sorted(maps.Keys(sites))
	out := make([]SiteData, 0, len(names))
	for _, name := range names {
		out = append(out, SiteData{Name: name, Domain: sites[name].Spec.Domain})
	}
	return out
}

// sanitizeName mirrors the Azure naming convention: lowercase, non-alphanumeric
// characters replaced with hyphens.
func sanitizeName(name string) string {
	s := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9-]`)
	return re.ReplaceAllString(s, "-")
}

func awsClusterName(targetName string, clusters map[string]types.AWSWorkloadClusterConfig) string {
	releases := slices.Sorted(maps.Keys(clusters))
	if len(releases) == 0 {
		return fmt.Sprintf("default_%s-control-plane", targetName)
	}
	return fmt.Sprintf("default_%s-%s-control-plane", targetName, releases[0])
}

func azureClusterName(targetName string, clusters map[string]types.AzureWorkloadClusterConfig) string {
	releases := slices.Sorted(maps.Keys(clusters))
	if len(releases) == 0 {
		return sanitizeName(targetName)
	}
	return fmt.Sprintf("%s-%s", sanitizeName(targetName), releases[0])
}
