package eject

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/posit-dev/ptd/lib/pulumistate"
)

type ResourceInventoryEntry struct {
	URN        string `json:"urn"`
	Type       string `json:"type"`
	Provider   string `json:"provider"`
	PhysicalID string `json:"physical_id"`
	Stack      string `json:"stack"`
	Purpose    string `json:"purpose"`
}

func ParseResourceInventory(data []byte, stateKey string) ([]ResourceInventoryEntry, error) {
	var state pulumistate.PulumiState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state JSON: %w", err)
	}

	stack := stackNameFromKey(stateKey)
	purpose := purposeFromKey(stateKey)
	var entries []ResourceInventoryEntry

	for _, res := range state.Checkpoint.Latest.Resources {
		if pulumistate.IsInternalResource(res.Type) {
			continue
		}

		entries = append(entries, ResourceInventoryEntry{
			URN:        res.URN,
			Type:       res.Type,
			Provider:   providerName(res.Provider),
			PhysicalID: resolvePhysicalID(res),
			Stack:      stack,
			Purpose:    purpose,
		})
	}

	return entries, nil
}

// Prefers outputs.arn (AWS) or outputs.id, falls back to the top-level id field.
func resolvePhysicalID(res pulumistate.PulumiResource) string {
	if arn, ok := res.Outputs["arn"].(string); ok && arn != "" {
		return arn
	}
	if id, ok := res.Outputs["id"].(string); ok && id != "" {
		return id
	}
	return res.ID
}

// e.g. "urn:pulumi:prod::proj::pulumi:providers:aws::default::id" → "aws"
func providerName(providerURN string) string {
	parts := strings.Split(providerURN, "::")
	for _, part := range parts {
		if strings.HasPrefix(part, "pulumi:providers:") {
			return strings.TrimPrefix(part, "pulumi:providers:")
		}
	}
	return ""
}

// e.g. ".pulumi/stacks/project/stack.json" → "project/stack"
func stackNameFromKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) >= 4 {
		return parts[2] + "/" + strings.TrimSuffix(parts[3], ".json")
	}
	return key
}

// e.g. ".pulumi/stacks/ptd-aws-workload-persistent/prod.json" → "persistent"
func purposeFromKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) < 4 {
		return ""
	}
	project := parts[2]
	projectParts := strings.Split(project, "-")
	// step name is everything after ptd-{cloud}-{target_type}-
	if len(projectParts) >= 4 {
		return strings.Join(projectParts[3:], "-")
	}
	return project
}
