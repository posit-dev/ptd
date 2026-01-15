package main

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var Version string

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of the PTD CLI",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Println("PTD CLI " + Version)
	},
}
