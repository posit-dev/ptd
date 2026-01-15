package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/rstudio/ptd/cmd/internal"
	"github.com/rstudio/ptd/lib/helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage ptd configuration",
	Long:  `Manage ptd configuration files and settings`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	Long:  `Show the current configuration values and which config file is being used`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("PTD Configuration")
		fmt.Println("================")

		if viper.ConfigFileUsed() != "" {
			fmt.Printf("Config file: %s\n", viper.ConfigFileUsed())
		} else {
			fmt.Println("Config file: None (using defaults and environment variables)")
		}

		// Show targets configuration directory with source
		fmt.Println("\nTargets configuration directory:")
		targetsConfigDir := helpers.GetTargetsConfigPath()
		fmt.Printf("  Path: %s\n", targetsConfigDir)

		// Determine configuration source
		if cmd.Flags().Changed("targets-config-dir") {
			fmt.Println("  Source: CLI flag (--targets-config-dir)")
		} else if os.Getenv("PTD_TARGETS_CONFIG_DIR") != "" {
			fmt.Println("  Source: Environment variable (PTD_TARGETS_CONFIG_DIR)")
		} else if viper.IsSet("targets_config_dir") && viper.GetString("targets_config_dir") != "" {
			fmt.Println("  Source: Config file")
		} else {
			fmt.Println("  Source: Default (TOP/infra)")
		}

		fmt.Println("\nAll configuration values:")
		allSettings := viper.AllSettings()

		if len(allSettings) == 0 {
			fmt.Println("  (no configuration values set)")
			return
		}

		for key, value := range allSettings {
			fmt.Printf("  %s: %v\n", key, value)
		}
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new configuration file",
	Long:  `Initialize a new configuration file with default values`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return initConfigFile()
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show configuration file paths",
	Long:  `Show the paths where ptd looks for configuration files`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("PTD configuration file search paths:")
		fmt.Printf("1. %s/ptdconfig.yaml\n", internal.ConfigDir())
		fmt.Printf("2. %s/ptdconfig.yaml\n", internal.DataDir())
		fmt.Printf("3. ./ptdconfig.yaml (current directory)\n")
		if home, err := os.UserHomeDir(); err == nil {
			fmt.Printf("4. %s/ptdconfig.yaml (home directory)\n", home)
		}
		fmt.Println("\nEnvironment variables with 'PTD_' prefix are also read automatically.")
	},
}

func initConfigFile() error {
	configDir := internal.ConfigDir()
	configFile := filepath.Join(configDir, "ptdconfig.yaml")

	// Check if config file already exists
	if _, err := os.Stat(configFile); err == nil {
		fmt.Printf("Configuration file already exists: %s\n", configFile)
		return nil
	}

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		slog.Error("Error creating config directory", "error", err, "configDir", configDir)
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write config file with comments
	file, err := os.Create(configFile)
	if err != nil {
		slog.Error("Error creating config file", "error", err, "configFile", configFile)
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()

	// Write config file with helpful comments
	configContent := `# PTD Configuration File
# This file contains configuration settings for the PTD CLI tool

# Enable verbose/debug output
verbose: false

# Path to targets configuration directory
# Contains __ctrl__/ and __work__/ subdirectories with target configs
# Can be absolute or relative to project root (TOP)
# Default: ./infra (relative to TOP)
# Uncomment and modify to use a custom location:
# targets_config_dir: ./infra
`

	if _, err := file.WriteString(configContent); err != nil {
		slog.Error("Error writing config file", "error", err, "configFile", configFile)
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("Configuration file created: %s\n", configFile)
	fmt.Println("You can now edit this file to customize your ptd settings.")
	slog.Info("Configuration file initialized successfully", "file", configFile)
	return nil
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configPathCmd)
	rootCmd.AddCommand(configCmd)
}
