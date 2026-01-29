package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/n9te9/federation-gateway/gateway"
)

var SampleGatewaySetting = &gateway.GatewaySetting{
	Endpoint: "/graphql",
	Port:     8080,
	Services: []gateway.GatewayService{
		{
			Name: "products",
			Host: "http://localhost:4001/graphql",
			SchemaFiles: []string{
				"./federation/product.gql",
			},
		},
		{
			Name: "reviews",
			Host: "http://localhost:4002/graphql",
			SchemaFiles: []string{
				"./federation/review.gql",
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

func Run() {
	settings, err := loadGatewaySetting()
	if err != nil {
		log.Fatalf("failed to load gateway settings: %v", err)
	}

	gw, err := gateway.NewGateway(settings)
	if err != nil {
		log.Fatalf("failed to build gateway: %v", err)
	}

	timeoutDuration, err := time.ParseDuration(settings.TimeoutDuration)
	if err != nil {
		log.Fatalf("failed to parse timeout duration: %v", err)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", settings.Port),
		Handler: gw,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("starting gateway server on port %d", settings.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("gateway server failed: %v", err)
		}
	}()

	<-ctx.Done()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
	defer cancel()

	log.Println("shutting down gateway server...")
	if err := srv.Shutdown(timeoutCtx); err != nil {
		log.Fatalf("failed to shutdown gateway server: %v", err)
	}
	log.Println("gateway server stopped")
}

func loadGatewaySetting() (*gateway.GatewaySetting, error) {
	f, err := os.Open("federation-gateway.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to open gateway settings file: %w", err)
	}
	defer f.Close()

	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway settings file: %w", err)
	}

	var settings gateway.GatewaySetting
	if err := yaml.Unmarshal(b, &settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal gateway settings: %w", err)
	}

	return &settings, nil
}
