package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n9te9/federation-gateway/gateway"
	"github.com/n9te9/federation-gateway/registry"
)

type registryServer struct {
	registry        *registry.Registry
	graphqlEndpoint string
}

func (s *registryServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/schema/registration":
		if req.Method == http.MethodPost {
			s.registry.RegisterGateway(w, req)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

type Graph struct {
	Name string
	Host string
	SDL  string
}

func RunRegistry(graphs []*Graph) error {
	if len(graphs) == 0 {
		return errors.New("no graphs provided")
	}

	reg := registry.NewRegistry()
	reg.Start()

	s := &registryServer{
		registry:        reg,
		graphqlEndpoint: "/graphql",
	}

	srv := &http.Server{
		Addr:    ":8080",
		Handler: s,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt, os.Kill)
	defer stop()
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return err
	}

	return nil
}

func RunGateway() {
	gw := gateway.NewGateway()
	srv := &http.Server{
		Addr:    ":8081",
		Handler: gw,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()
}
