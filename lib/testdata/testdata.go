package testdata

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func Setup(_ *testing.T) (func(), error) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return func() {}, err
	}

	viper.SetDefault("TOP", filepath.Join(strings.TrimSpace(string(top)), "lib", "testdata"))

	return func() {}, nil
}
