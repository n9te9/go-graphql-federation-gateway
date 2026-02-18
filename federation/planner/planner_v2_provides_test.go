package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
)

// TestPlannerV2_ProvidesFieldOptimization tests that @provides marks ownership correctly
func TestPlannerV2_ProvidesFieldOptimization(t *testing.T) {
	// For now, this test verifies that @provides directive is parsed.
	// Full optimization behavior requires executor-level changes.
	// The test checks that @provides information is available in the schema.

	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float @provides(fields: "discount")
			discount: Float
		}

		type Query {
			product(id: ID!): Product
		}
	`

	productSG, err := graph.NewSubGraphV2("products", []byte(productSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}

	// Verify that @provides directive is parsed
	entity, exists := productSG.GetEntity("Product")
	if !exists {
		t.Fatal("Product entity not found")
	}

	// Check that price field has @provides directive
	var priceField *graph.Field
	for _, field := range entity.Fields {
		if field.Name == "price" {
			priceField = field
			break
		}
	}

	if priceField == nil {
		t.Fatal("price field not found")
	}

	if len(priceField.Provides) == 0 {
		t.Error("Expected price field to have @provides directive, but it doesn't")
	}

	if len(priceField.Provides) > 0 && priceField.Provides[0] != "discount" {
		t.Errorf("Expected @provides to specify 'discount', got %v", priceField.Provides)
	}
}
