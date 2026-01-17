package main

import (
	"github.com/n9te9/federation-gateway/server"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of Federation Gateway",
	Run: func(cmd *cobra.Command, args []string) {
		println("Federation Gateway v0.0.0-rc")
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Federation Gateway project",
	Run: func(cmd *cobra.Command, args []string) {
		server.Init()
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Federation Gateway server",
	Run: func(cmd *cobra.Command, args []string) {
		server.Run()
	},
}

func main() {
	rootCmd := cobra.Command{}

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(serveCmd)

	if err := rootCmd.Execute(); err != nil {
		panic(err)
	}
}
