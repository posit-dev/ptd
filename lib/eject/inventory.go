package eject

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ResourceInventoryEntry struct {
	URN        string `json:"urn"`
	Type       string `json:"type"`
	Provider   string `json:"provider"`
	PhysicalID string `json:"physical_id"`
	Stack      string `json:"stack"`
	Purpose    string `json:"purpose"`
}

// Pulumi state structures — richer than attestation's, includes id/outputs/provider
type pulumiState struct {
	Version    int              `json:"version"`
	Checkpoint pulumiCheckpoint `json:"checkpoint"`
}

type pulumiCheckpoint struct {
	Latest pulumiLatest `json:"latest"`
}

type pulumiLatest struct {
	Manifest  pulumiManifest  `json:"manifest"`
	Resources []pulumiFullRes `json:"resources"`
}

type pulumiManifest struct {
	Time    string `json:"time"`
	Version string `json:"version"`
}

type pulumiFullRes struct {
	Type     string                 `json:"type"`
	URN      string                 `json:"urn"`
	ID       string                 `json:"id"`
	Provider string                 `json:"provider"`
	Outputs  map[string]interface{} `json:"outputs"`
}

func ParseResourceInventory(data []byte, stateKey string) ([]ResourceInventoryEntry, error) {
	var state pulumiState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state JSON: %w", err)
	}

	stack := stackNameFromKey(stateKey)
	purpose := purposeFromKey(stateKey)
	var entries []ResourceInventoryEntry

	for _, res := range state.Checkpoint.Latest.Resources {
		if strings.HasPrefix(res.Type, "pulumi:pulumi:") ||
			strings.HasPrefix(res.Type, "pulumi:providers:") {
			continue
		}

		physicalID := resolvePhysicalID(res)

		entries = append(entries, ResourceInventoryEntry{
			URN:        res.URN,
			Type:       res.Type,
			Provider:   providerName(res.Provider),
			PhysicalID: physicalID,
			Stack:      stack,
			Purpose:    purpose,
		})
	}

	return entries, nil
}

// resolvePhysicalID picks the best physical identifier for a resource.
// Prefers outputs.arn (AWS) or outputs.id, falls back to the top-level id field.
func resolvePhysicalID(res pulumiFullRes) string {
	if arn, ok := res.Outputs["arn"].(string); ok && arn != "" {
		return arn
	}
	if id, ok := res.Outputs["id"].(string); ok && id != "" {
		return id
	}
	return res.ID
}

// providerName extracts a short provider name from a Pulumi provider URN.
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

// stackNameFromKey extracts "project/stack" from ".pulumi/stacks/project/stack.json"
func stackNameFromKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) >= 4 {
		return parts[2] + "/" + strings.TrimSuffix(parts[3], ".json")
	}
	return key
}

// purposeFromKey extracts the step name from the project portion of the key.
// e.g. ".pulumi/stacks/ptd-aws-workload-persistent/prod.json" → "persistent"
func purposeFromKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) < 4 {
		return ""
	}
	project := parts[2]
	projectParts := strings.Split(project, "-")
	// ptd-{cloud}-{target_type}-{step...} — step is everything after the 3rd dash
	if len(projectParts) >= 4 {
		return strings.Join(projectParts[3:], "-")
	}
	return project
}
