package main

import (
	"log"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/n9te9/federation-gateway/gateway"
	"github.com/n9te9/federation-gateway/server"
	"github.com/spf13/cobra"
)

var SampleGatewaySetting = &gateway.GatewaySetting{
	Endpoint:                    "/graphql",
	Port:                        9000,
	TimeoutDuration:             "5s",
	EnableComplementRequestId:   false,
	EnableHangOverRequestHeader: true,
	Services: []gateway.GatewayService{
		{
			Name: "products",
			Host: "http://localhost:4001/graphql",
			SchemaFiles: []string{
				"./product/product.gql",
			},
		},
		{
			Name: "reviews",
			Host: "http://localhost:4002/graphql",
			SchemaFiles: []string{
				"./inventory/review.gql",
			},
		},
	},
}

func Init() {
	f, err := os.Create("federation-gateway.yaml")
	if err != nil {
		log.Fatalf("failed to create sample gateway settings file: %v", err)
	}
	defer f.Close()

	b, err := yaml.Marshal(SampleGatewaySetting)
	if err != nil {
		log.Fatalf("failed to marshal sample gateway settings: %v", err)
	}

	if _, err := f.Write(b); err != nil {
		log.Fatalf("failed to write sample gateway settings file: %v", err)
	}
}

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
		Init()
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
