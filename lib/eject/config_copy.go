package eject

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

var controlRoomFieldPattern = regexp.MustCompile(`(?m)^(\s*control_room_\w+:.*)$`)

// CopyWorkloadConfig copies the entire workload directory into the eject
// bundle's config/ directory. The copied ptd.yaml is annotated with comments
// on control_room_* fields indicating they'll be cleared during eject.
func CopyWorkloadConfig(workloadPath string, outputDir string) error {
	configDir := filepath.Join(outputDir, "config")

	if err := copyDir(workloadPath, configDir); err != nil {
		return fmt.Errorf("failed to copy workload config: %w", err)
	}

	if err := annotatePtdYaml(configDir); err != nil {
		return fmt.Errorf("failed to annotate ptd.yaml: %w", err)
	}

	return nil
}

func annotatePtdYaml(configDir string) error {
	path := filepath.Join(configDir, "ptd.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	annotated := AnnotateControlRoomFields(string(data))
	return os.WriteFile(path, []byte(annotated), 0644)
}

// AnnotateControlRoomFields adds a comment to each control_room_* field
// indicating it will be cleared during eject.
func AnnotateControlRoomFields(yaml string) string {
	return controlRoomFieldPattern.ReplaceAllString(yaml,
		"$1  # EJECT: cleared during eject")
}

func copyFile(src, dst string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(filepath.Clean(dst))
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}
		return copyFile(path, destPath)
	})
}
