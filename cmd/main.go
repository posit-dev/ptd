package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/posit-dev/ptd/cmd/internal"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var rootCmd = &cobra.Command{
	Use:   "ptd",
	Short: "ptd is a tool for managing posit team environments",
	Long: `ptd is a tool for managing ptd environments. It is designed to work with multiple cloud environments
           and provides a simple interface for managing these environments`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Set up logging based on the verbose flag
		// Do this in PersistentPreRunE so it executes after flags are parsed (unlike init())
		setupLogging()

		// Set up global context values
		ctx := context.WithValue(cmd.Context(), "verbose", viper.GetBool("verbose"))
		cmd.SetContext(ctx)

		// Validate infrastructure path for commands that work with targets
		if shouldValidateTargetConfigPath(cmd) {
			if err := helpers.ValidateTargetsConfigPath(); err != nil {
				slog.Error("Invalid targets configuration directory", "error", err)
				return err
			}
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose/debug output")
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))

	// Add targets-config-dir flag
	rootCmd.PersistentFlags().StringP("targets-config-dir", "", "",
		"Path to targets configuration directory (absolute or relative to TOP)")
	viper.BindPFlag("targets_config_dir", rootCmd.PersistentFlags().Lookup("targets-config-dir"))

	// Determine project root directory
	// Priority: 1) PROJECT_ROOT env var, 2) binary location, 3) git
	var defaultTop string
	var err error

	// Check for explicit PROJECT_ROOT environment variable
	if root := os.Getenv("PROJECT_ROOT"); root != "" {
		defaultTop = root
	} else {
		// Try to derive from binary location (works in released environments)
		if executable, execErr := os.Executable(); execErr == nil {
			// Binary is at .local/bin/ptd, project root is 2 levels up
			defaultTop = filepath.Dir(filepath.Dir(executable))
		} else {
			// Fall back to git (useful in development)
			defaultTop, err = helpers.GitTop()
			if err != nil {
				slog.Error("Could not figure out default top directory", "error", err)
				return
			}
		}
	}

	viper.SetDefault("TOP", defaultTop)

	// Set up configuration file
	setupConfig()

	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)

	// ensure the data directory exists
	if err = os.MkdirAll(internal.DataDir(), 0755); err != nil {
		slog.Error("Could not create data directory", "error", err)
		return
	}
}

func main() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func setupLogging() {
	var logger *slog.Logger

	// Get verbose setting from viper (combines config file, env vars, and flags)
	verbose := viper.GetBool("verbose")

	// Check if we're running in an interactive terminal
	if term.IsTerminal(int(os.Stdout.Fd())) {
		// Use charmbracelet/log for interactive terminals
		charmLogger := log.New(os.Stderr)

		// Set log level based on verbose flag
		if verbose {
			slog.Info("Verbose mode enabled, setting log level to debug")
			charmLogger.SetLevel(log.DebugLevel)
		} else {
			charmLogger.SetLevel(log.InfoLevel)
		}

		logger = slog.New(charmLogger)
	} else {
		// Use existing text handler for non-interactive environments
		opts := &slog.HandlerOptions{Level: slog.LevelInfo}

		if verbose {
			slog.Info("Verbose mode enabled, setting log level to debug")
			opts.Level = slog.LevelDebug
		}

		logger = slog.New(internal.NewCliOutputHandler(os.Stderr, opts))
	}

	slog.SetDefault(logger)
}

// shouldValidateTargetConfigPath determines if a command needs target config path validation.
// Returns true for commands that work with targets, false otherwise.
func shouldValidateTargetConfigPath(cmd *cobra.Command) bool {
	commandPath := cmd.CommandPath()

	// Commands that DON'T need target config path validation
	skipCommands := []string{
		"ptd config",
		"ptd version",
		"ptd help",
		"ptd completion",
		"ptd admin export-accounts",
		"ptd admin generate-role",
	}

	// Check if this command should skip validation
	for _, skipCmd := range skipCommands {
		if commandPath == skipCmd || strings.HasPrefix(commandPath, skipCmd+" ") {
			return false
		}
	}

	return true
}

func setupConfig() {
	// Set config file name and type
	viper.SetConfigName("ptdconfig")
	viper.SetConfigType("yaml")

	// Add config file search paths
	viper.AddConfigPath(internal.ConfigDir()) // ~/.config/ptd/
	viper.AddConfigPath(internal.DataDir())   // ~/.local/share/ptd/
	viper.AddConfigPath(".")                  // current directory
	if home, err := os.UserHomeDir(); err == nil {
		viper.AddConfigPath(home) // home directory
	}

	// Set environment variable prefix
	viper.SetEnvPrefix("PTD")
	viper.AutomaticEnv()

	// Read config file if it exists
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found, that's okay
			slog.Debug("Config file not found, using defaults and environment variables")
		} else {
			// Config file was found but another error was produced
			slog.Warn("Error reading config file", "error", err)
		}
	} else {
		slog.Info("Using config file", "file", viper.ConfigFileUsed())
	}
}
