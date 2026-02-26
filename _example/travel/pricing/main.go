package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/n9te9/go-graphql-federation-gateway/_example/travel/pricing/graph"
)

const defaultPort = "8080"

func accessLog(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		operation := "-"
		body400 := string(body)
		if len(body400) > 400 {
			body400 = body400[:400]
		}
		if strings.Contains(body400, "_entities") {
			operation = "_entities"
		} else if strings.Contains(body400, "query") || strings.Contains(body400, "mutation") {
			operation = "rootQuery"
		}
		next.ServeHTTP(w, r)
		log.Printf("[ACCESS] service=%s op=%s path=%s remote=%s dur=%s",
			service, operation, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	srv := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))

	mux := http.NewServeMux()
	mux.Handle("/", playground.Handler("GraphQL playground", "/query"))
	mux.Handle("/query", srv)

	log.Printf("connect to http://localhost:%s/ for GraphQL playground", port)
	log.Fatal(http.ListenAndServe(":"+port, accessLog("pricing", mux)))
}
