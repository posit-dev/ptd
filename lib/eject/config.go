package eject

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ControlRoomSnapshot captures all control_room_* fields from ptd.yaml.
// Fields are discovered dynamically so new control_room_* fields are
// automatically included without code changes.
type ControlRoomSnapshot struct {
	Fields map[string]string `json:"fields"`
}

func (s *ControlRoomSnapshot) Get(key string) string {
	return s.Fields[key]
}

type specEnvelope struct {
	Spec yaml.Node `yaml:"spec"`
}

const controlRoomPrefix = "control_room_"

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

	fields := make(map[string]string)
	for k, v := range specMap {
		if strings.HasPrefix(k, controlRoomPrefix) {
			if s, ok := v.(string); ok {
				fields[k] = s
			} else {
				fields[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	return &ControlRoomSnapshot{Fields: fields}, nil
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
