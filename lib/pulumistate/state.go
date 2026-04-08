package pulumistate

import "strings"

// PulumiState represents the structure of a Pulumi state JSON file
type PulumiState struct {
	Version    int              `json:"version"`
	Checkpoint PulumiCheckpoint `json:"checkpoint"`
}

// PulumiCheckpoint contains the checkpoint section of Pulumi state
type PulumiCheckpoint struct {
	Latest PulumiLatest `json:"latest"`
}

// PulumiLatest contains the latest deployment information
type PulumiLatest struct {
	Manifest  PulumiManifest   `json:"manifest"`
	Resources []PulumiResource `json:"resources"`
}

// PulumiManifest contains Pulumi version and timestamp information
type PulumiManifest struct {
	Time    string `json:"time"`
	Version string `json:"version"`
}

// PulumiResource represents a single resource in Pulumi state
type PulumiResource struct {
	Type     string                 `json:"type"`
	URN      string                 `json:"urn"`
	ID       string                 `json:"id"`
	Provider string                 `json:"provider"`
	Outputs  map[string]interface{} `json:"outputs"`
}

// IsInternalResource returns true for Pulumi-internal resources that should
// typically be excluded from user-facing resource lists.
func IsInternalResource(resourceType string) bool {
	return strings.HasPrefix(resourceType, "pulumi:pulumi:") ||
		strings.HasPrefix(resourceType, "pulumi:providers:")
}
