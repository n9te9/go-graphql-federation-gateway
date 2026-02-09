package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/n9te9/go-graphql-federation-gateway/gateway"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const gatewayVersion = "v0.1.0"

func Run() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	settings, err := loadGatewaySetting()
	if err != nil {
		log.Fatalf("failed to load gateway settings: %v", err)
	}

	gw, err := gateway.NewGateway(*settings)
	if err != nil {
		log.Fatalf("failed to build gateway: %v", err)
	}

	gwHandler := http.Handler(gw)
	if settings.Opentelemetry.TracingSetting.Enable {
		gwHandler = otelhttp.NewHandler(http.Handler(gw), settings.ServiceName)
	}

	timeoutDuration, err := time.ParseDuration(settings.TimeoutDuration)
	if err != nil {
		log.Fatalf("failed to parse timeout duration: %v", err)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", settings.Port),
		Handler: gwHandler,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill, syscall.SIGTERM)
	defer cancel()

	shutdown, err := gateway.InitTracer(ctx, settings.ServiceName, gatewayVersion)
	if err != nil {
		log.Fatalf("failed to initialize tracer: %v", err)
	}

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

	if err := shutdown(timeoutCtx); err != nil {
		log.Fatalf("failed to shutdown tracer: %v", err)
	}

	log.Println("gateway server stopped")
}

func loadGatewaySetting() (*gateway.GatewayOption, error) {
	f, err := os.Open("gateway.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to open gateway settings file: %w", err)
	}
	defer f.Close()

	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway settings file: %w", err)
	}

	var settings gateway.GatewayOption
	if err := yaml.Unmarshal(b, &settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal gateway settings: %w", err)
	}

	return &settings, nil
}
