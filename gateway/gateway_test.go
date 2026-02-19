package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGateway_ValidateAccessibility(t *testing.T) {
	// Create a test gateway with a schema containing @inaccessible field
	settings := GatewayOption{
		Endpoint:    "/graphql",
		ServiceName: "test-gateway",
		Port:        8080,
		Services: []GatewayService{
			{
				Name: "product",
				Host: "http://product.example.com",
				SchemaFiles: []string{
					"testdata/product-with-inaccessible.graphql",
				},
			},
		},
	}

	// Create test schema file
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			internalCode: String! @inaccessible
		}

		type Query {
			product(id: ID!): Product
		}
























































































}	// Cleanup test filefunc cleanupTestSchema(path string) {}	return nil	// In real implementation, this would create the file	// For testing purposes, we'll create a simple test helperfunc createTestSchema(path, content string) error {}	})		}			t.Errorf("Expected error code INACCESSIBLE_FIELD, got: %s", code)		if code != "INACCESSIBLE_FIELD" {		code := ext["code"].(string)		ext := errMap["extensions"].(map[string]any)		// Verify error code		}			t.Errorf("Expected inaccessible error message, got: %s", message)		if message != `Cannot query field "internalCode" on type "Product"` {		message := errMap["message"].(string)		errMap := errors[0].(map[string]any)		// Verify error message contains inaccessible field information		}			t.Fatal("Expected errors in response")		if !ok || len(errors) == 0 {		errors, ok := resp["errors"].([]any)		json.NewDecoder(w.Body).Decode(&resp)		var resp map[string]any		}			t.Fatalf("Expected status OK, got %d", w.Code)		if w.Code != http.StatusOK {		gw.ServeHTTP(w, httpReq)		w := httptest.NewRecorder()		httpReq := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))		body, _ := json.Marshal(req)		req := graphQLRequest{Query: query}		query := `{ product(id: "1") { id internalCode } }`	t.Run("query inaccessible field should fail", func(t *testing.T) {	})		}			}				}					}						}							}								t.Error("Expected no INACCESSIBLE_FIELD error")							if code, ok := ext["code"].(string); ok && code == "INACCESSIBLE_FIELD" {						if ext, ok := errMap["extensions"].(map[string]any); ok {					if errMap, ok := err.(map[string]any); ok {				for _, err := range errors {			if errors, ok := resp["errors"].([]any); ok {			// Check that no accessibility errors are returned			json.NewDecoder(w.Body).Decode(&resp)			var resp map[string]any		if w.Code == http.StatusOK {		gw.ServeHTTP(w, httpReq)		w := httptest.NewRecorder()		httpReq := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))		body, _ := json.Marshal(req)		req := graphQLRequest{Query: query}		query := `{ product(id: "1") { id name } }`	t.Run("query accessible field should succeed", func(t *testing.T) {	}		t.Fatalf("NewGateway failed: %v", err)	if err != nil {	gw, err := NewGateway(settings)	defer cleanupTestSchema("testdata/product-with-inaccessible.graphql")	}		t.Fatalf("Failed to create test schema: %v", err)	if err := createTestSchema("testdata/product-with-inaccessible.graphql", schema); err != nil {	// Write temporary test schema	`