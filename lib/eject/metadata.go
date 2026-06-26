package eject

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/posit-dev/ptd/lib/types"
)

type Metadata struct {
	EjectTimestamp string `json:"eject_timestamp"`
	CLIVersion     string `json:"cli_version"`
	TargetName     string `json:"target_name"`
	CloudProvider  string `json:"cloud_provider"`
	Region         string `json:"region"`
	AccountID      string `json:"account_id"`
	DryRun         bool   `json:"dry_run"`
}

func CollectMetadata(config interface{}, opts Options, now time.Time) (*Metadata, error) {
	m := &Metadata{
		EjectTimestamp: now.UTC().Format(time.RFC3339),
		CLIVersion:     opts.CLIVersion,
		TargetName:     opts.TargetName,
		DryRun:         opts.DryRun,
	}

	switch cfg := config.(type) {
	case types.AWSWorkloadConfig:
		m.CloudProvider = string(types.AWS)
		m.Region = cfg.Region
		m.AccountID = cfg.AccountID
	case types.AzureWorkloadConfig:
		m.CloudProvider = string(types.Azure)
		m.Region = cfg.Region
		m.AccountID = cfg.SubscriptionID
	default:
		return nil, fmt.Errorf("unsupported config type for metadata: %T", config)
	}

	return m, nil
}

func WriteMetadata(metadata *Metadata, outputDir string) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(outputDir, "metadata.json"), data, 0644)
}
