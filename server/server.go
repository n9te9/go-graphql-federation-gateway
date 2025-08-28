package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n9te9/federation-gateway/registry"
)

type server struct {
	registry        *registry.Registry
	graphqlEndpoint string
}

func (s *server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/schema/registration":
		if req.Method == http.MethodPost {
			s.registry.RegisterGateway(w, req)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	case s.graphqlEndpoint:
		if req.Method == http.MethodPost {
			s.registry.AppliedGateway().ServeHTTP(w, req)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func Run() error {
	reg := registry.NewRegistry("http://localhost:8080", http.NotFoundHandler())
	reg.Start()

	s := &server{
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
