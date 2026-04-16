package eject

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type ControlRoomSnapshot struct {
	AccountID   string `json:"account_id"`
	ClusterName string `json:"cluster_name"`
	Domain      string `json:"domain"`
	Region      string `json:"region"`
}

type specEnvelope struct {
	Spec yaml.Node `yaml:"spec"`
}

func SnapshotControlRoomFields(ptdYamlPath string) (*ControlRoomSnapshot, error) {
	data, err := os.ReadFile(ptdYamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ptd.yaml: %w", err)
	}

	var env specEnvelope
	if err := yaml.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("failed to parse ptd.yaml: %w", err)
	}

	var specMap map[string]interface{}
	if err := env.Spec.Decode(&specMap); err != nil {
		return nil, fmt.Errorf("failed to decode spec: %w", err)
	}

	getString := func(key string) string {
		if v, ok := specMap[key]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}

	snapshot := &ControlRoomSnapshot{
		AccountID:   getString("control_room_account_id"),
		ClusterName: getString("control_room_cluster_name"),
		Domain:      getString("control_room_domain"),
		Region:      getString("control_room_region"),
	}

	return snapshot, nil
}

var controlRoomValuePattern = regexp.MustCompile(`(?m)^(\s*control_room_\w+:\s*)(.+)$`)

func StripControlRoomFields(ptdYamlPath string) error {
	data, err := os.ReadFile(ptdYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read ptd.yaml: %w", err)
	}

	stripped := controlRoomValuePattern.ReplaceAllStringFunc(string(data), func(match string) string {
		parts := controlRoomValuePattern.FindStringSubmatch(match)
		// parts[1] is the key+colon prefix, parts[2] is the value
		value := strings.TrimSpace(parts[2])

		// Preserve inline comments
		if idx := strings.Index(value, " #"); idx >= 0 {
			return parts[1] + `""` + value[idx:]
		}
		if idx := strings.Index(value, "\t#"); idx >= 0 {
			return parts[1] + `""` + value[idx:]
		}

		return parts[1] + `""`
	})

	return os.WriteFile(ptdYamlPath, []byte(stripped), 0644)
}
