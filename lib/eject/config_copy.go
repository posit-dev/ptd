package eject

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

var controlRoomFieldPattern = regexp.MustCompile(`(?m)^(\s*)(control_room_\w+:.*)$`)

// CopyWorkloadConfig copies the entire workload directory into the eject
// bundle's config/ directory. In the copied ptd.yaml the control_room_* fields
// are commented out (their original values preserved for reference), since the
// bundle is a reference snapshot rather than an operational config.
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

// AnnotateControlRoomFields comments out each control_room_* field, preserving
// the original value for reference. The eject bundle's config/ptd.yaml is a
// reference snapshot, not a live config, so the fields are made inert rather
// than left as active YAML with a trailing comment.
func AnnotateControlRoomFields(yaml string) string {
	return controlRoomFieldPattern.ReplaceAllString(yaml,
		"${1}# ${2}  # EJECT: cleared during eject")
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
