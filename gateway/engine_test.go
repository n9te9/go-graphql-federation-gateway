package gateway_test

import (
	"net/http"
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/gateway"
)

// minimalist Federation v2 SDL with a @key entity.
const sdlProducts = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key"])

type Query {
	product(id: ID!): Product
}

type Product @key(fields: "id") {
	id: ID!
	name: String
}`

const sdlReviews = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@external"])

type Query {
	reviews: [Review]
}

type Review @key(fields: "id") {
	id: ID!
	productId: ID! @external
	body: String
}`

func TestBuildEngine_Success(t *testing.T) {
	sdls := map[string]string{
		"products": sdlProducts,
		"reviews":  sdlReviews,
	}
	hosts := map[string]string{
		"products": "http://localhost:4001",
		"reviews":  "http://localhost:4002",
	}

	engine, err := gateway.BuildEngineForTest(sdls, hosts, &http.Client{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestBuildEngine_InvalidSDL(t *testing.T) {
	sdls := map[string]string{
		"bad": `this is not valid SDL { { { ]]]`,
	}
	hosts := map[string]string{
		"bad": "http://localhost:4001",
	}

	_, err := gateway.BuildEngineForTest(sdls, hosts, &http.Client{})
	if err == nil {
		t.Fatal("expected error for invalid SDL, got nil")
	}
}

func TestBuildEngine_EmptySDLs(t *testing.T) {
	_, err := gateway.BuildEngineForTest(map[string]string{}, map[string]string{}, &http.Client{})
	if err == nil {
		t.Fatal("expected error for empty SDL map, got nil")
	}
}

func TestCopyMap(t *testing.T) {
	orig := map[string]string{"a": "1", "b": "2"}
	cp := gateway.CopyMapForTest(orig)

	if len(cp) != len(orig) {
		t.Fatalf("length mismatch: got %d, want %d", len(cp), len(orig))
	}
	for k, v := range orig {
		if cp[k] != v {
			t.Errorf("key %q: got %q, want %q", k, cp[k], v)
		}
	}

	// Mutation of copy must not affect original.
	cp["a"] = "changed"
	if orig["a"] != "1" {
		t.Error("mutation of copy affected original")
	}
}
