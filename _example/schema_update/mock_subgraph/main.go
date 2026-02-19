// Package main implements a minimal mock subgraph server for the schema_update example.
//
// The server serves three endpoints:
//
//	POST /_service        — returns the current SDL (queried by the gateway on startup and apply)
//	POST /query           — handles GraphQL queries (returns stub data)
//	PUT  /schema          — hot-swaps the in-memory SDL (for demo / testing purposes)
//
// Usage:
//
//	go run ./mock_subgraph --addr :8501 --name products --version v1
//	go run ./mock_subgraph --addr :8502 --name reviews  --version v1
//
// To trigger a schema update demo:
//
//	# 1. Update the SDL on the subgraph
//	curl -X PUT http://localhost:8501/schema -d '...<new SDL>...'
//
//	# 2. Notify the gateway to re-fetch and apply
//	curl -X POST http://localhost:9000/products/apply
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
)

// sdlV1 is the initial schema for a service (add a @key entity + simple query).
const sdlV1Products = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@shareable"])

type Query {
  product(id: ID!): Product
  products: [Product!]
}

type Product @key(fields: "id") {
  id: ID!
  name: String!
  price: Int!
}
`

// sdlV2Products adds a new field to Product without breaking the existing schema.
// Send this via PUT /schema and then POST /products/apply to the gateway to demo live updates.
const sdlV2Products = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@shareable"])

type Query {
  product(id: ID!): Product
  products: [Product!]
}

type Product @key(fields: "id") {
  id: ID!
  name: String!
  price: Int!
  description: String
}
`

const sdlV1Reviews = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@external"])

type Query {
  reviews: [Review!]
}

type Review @key(fields: "id") {
  id: ID!
  body: String!
  productId: ID! @external
}
`

var (
	addr    = flag.String("addr", ":8501", "listen address")
	name    = flag.String("name", "products", "subgraph name (products|reviews)")
	version = flag.String("version", "v1", "initial SDL version (v1)")
)

func main() {
	flag.Parse()

	var initialSDL string
	switch *name {
	case "products":
		initialSDL = sdlV1Products
	case "reviews":
		initialSDL = sdlV1Reviews
	default:
		log.Fatalf("unknown subgraph name %q (choose products or reviews)", *name)
	}

	srv := newSubgraphServer(initialSDL)
	mux := http.NewServeMux()

	// POST /_service — SDL introspection (called by the gateway on startup and on /apply)
	mux.HandleFunc("/_service", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sdl := srv.getSDL()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"data": map[string]any{
				"_service": map[string]any{"sdl": sdl},
			},
		})
	})

	// POST /query — stub GraphQL handler
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"data": map[string]any{
				"products": []map[string]any{
					{"id": "1", "name": "Widget", "price": 999},
				},
				"reviews": []map[string]any{
					{"id": "r1", "body": "Great product!", "productId": "1"},
				},
			},
		})
	})

	// PUT /schema — hot-swap the in-memory SDL for demo purposes
	mux.HandleFunc("/schema", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		srv.setSDL(string(body))
		log.Printf("[%s] SDL updated (%d bytes) — POST /%s/apply to the gateway to activate", *name, len(body), *name)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	})

	// GET / — health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": *name}) //nolint:errcheck
	})

	log.Printf("[%s] listening on %s (SDL version: %s)", *name, *addr, *version)
	log.Printf("[%s] demo commands:", *name)
	log.Printf("  Update SDL:   curl -X PUT http://localhost%s/schema -d '%s'", *addr, "<new SDL>")
	log.Printf("  Apply to GW:  curl -X POST http://localhost:9000/%s/apply", *name)

	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

type subgraphServer struct {
	mu  sync.RWMutex
	sdl string
}

func newSubgraphServer(sdl string) *subgraphServer {
	return &subgraphServer{sdl: sdl}
}

func (s *subgraphServer) getSDL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sdl
}

func (s *subgraphServer) setSDL(sdl string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sdl = sdl
}

// sdlV2Products is accessible as a constant for demo scripts.
var _ = fmt.Sprintf("sdlV2Products: %s", sdlV2Products)
