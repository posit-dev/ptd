package eject

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var controlRoomFieldPattern = regexp.MustCompile(`(?m)^(\s*control_room_\w+:.*)$`)

// CopyWorkloadConfig copies ptd.yaml, site_*/site.yaml, and customizations/
// into the eject bundle's config/ directory.
func CopyWorkloadConfig(workloadPath string, outputDir string) error {
	configDir := filepath.Join(outputDir, "config")

	if err := copyPtdYaml(workloadPath, configDir); err != nil {
		return fmt.Errorf("failed to copy ptd.yaml: %w", err)
	}

	if err := copySiteYamls(workloadPath, configDir); err != nil {
		return fmt.Errorf("failed to copy site configs: %w", err)
	}

	if err := copyCustomizations(workloadPath, configDir); err != nil {
		return fmt.Errorf("failed to copy customizations: %w", err)
	}

	return nil
}

func copyPtdYaml(workloadPath string, configDir string) error {
	src := filepath.Join(workloadPath, "ptd.yaml")
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	annotated := AnnotateControlRoomFields(string(data))

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, "ptd.yaml"), []byte(annotated), 0644)
}

// AnnotateControlRoomFields adds a comment to each control_room_* field
// indicating it will be removed during severance.
func AnnotateControlRoomFields(yaml string) string {
	return controlRoomFieldPattern.ReplaceAllString(yaml,
		"$1  # EJECT: removed during control room severance")
}

func copySiteYamls(workloadPath string, configDir string) error {
	entries, err := os.ReadDir(workloadPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "site_") {
			continue
		}

		siteYaml := filepath.Join(workloadPath, entry.Name(), "site.yaml")
		if _, err := os.Stat(siteYaml); os.IsNotExist(err) {
			continue
		}

		destDir := filepath.Join(configDir, entry.Name())
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return err
		}

		if err := copyFile(siteYaml, filepath.Join(destDir, "site.yaml")); err != nil {
			return fmt.Errorf("failed to copy %s/site.yaml: %w", entry.Name(), err)
		}
	}

	return nil
}

func copyCustomizations(workloadPath string, configDir string) error {
	customDir := filepath.Join(workloadPath, "customizations")
	if _, err := os.Stat(customDir); os.IsNotExist(err) {
		return nil // customizations are optional
	}

	destDir := filepath.Join(configDir, "customizations")
	return copyDir(customDir, destDir)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
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
