package testdata

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/viper"
)

func Setup(_ *testing.T) (func(), error) {
	// Use runtime.Caller to locate this file's directory, which is the
	// testdata directory itself. This is reliable regardless of working
	// directory, environment variables, or git hook context (where
	// git rev-parse --show-toplevel can return incorrect results).
	_, thisFile, _, _ := runtime.Caller(0)
	testdataDir := filepath.Dir(thisFile)

	viper.Set("TOP", testdataDir)

	return func() {}, nil
}
