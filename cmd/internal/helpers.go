package internal

import (
	"fmt"
	"os"
	"path/filepath"
)

func DataDir() string {
	dataDir, ok := os.LookupEnv("XDG_DATA_HOME")
	if ok {
		dataDir = fmt.Sprintf("%s/ptd", dataDir)
	} else {
		dataDir = os.ExpandEnv("$HOME/.local/share/ptd")
	}
	return dataDir
}

func ConfigDir() string {
	configDir, ok := os.LookupEnv("XDG_CONFIG_HOME")
	if ok {
		configDir = fmt.Sprintf("%s/ptd", configDir)
	} else {
		configDir = os.ExpandEnv("$HOME/.config/ptd")
	}
	return configDir
}

func RegistryFilePath() string {
	return filepath.Join(DataDir(), "proxies.json")
}
