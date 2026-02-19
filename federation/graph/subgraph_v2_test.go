package graph_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
)

func TestNewSubGraphV2(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	if sg.Name != "product" {
		t.Errorf("expected name 'product', got '%s'", sg.Name)
	}

	if sg.Host != "http://product.example.com" {
		t.Errorf("expected host 'http://product.example.com', got '%s'", sg.Host)
	}

	entities := sg.GetEntities()
	if len(entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(entities))
	}

	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if len(productEntity.Keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(productEntity.Keys))
	}

	if productEntity.Keys[0].FieldSet != "id" {
		t.Errorf("expected key field 'id', got '%s'", productEntity.Keys[0].FieldSet)
	}

	if !productEntity.Keys[0].Resolvable {
		t.Error("expected key to be resolvable")
	}

	if productEntity.IsExtension() {
		t.Error("expected Product entity to not be an extension")
	}

	if len(productEntity.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(productEntity.Fields))
	}
}

func TestNewSubGraphV2_WithExtension(t *testing.T) {
	schema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}

		type Review {
			id: ID!
			rating: Int!
		}
	`

	sg, err := graph.NewSubGraphV2("review", []byte(schema), "http://review.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if !productEntity.IsExtension() {
		t.Error("expected Product entity to be an extension")
	}
}

func TestNewSubGraphV2_WithDirectives(t *testing.T) {
	schema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @external
			reviews: [Review!]! @requires(fields: "name")
		}

		type Review {
			id: ID!
			rating: Int!
			product: Product @provides(fields: "name")
		}
	`

	sg, err := graph.NewSubGraphV2("review", []byte(schema), "http://review.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	reviewsField, ok := productEntity.Fields["reviews"]
	if !ok {
		t.Fatal("reviews field not found")
	}

	if len(reviewsField.Requires) != 1 || reviewsField.Requires[0] != "name" {
		t.Errorf("expected requires 'name', got %v", reviewsField.Requires)
	}
}

func TestNewSubGraphV2_WithShareable(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @shareable
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	if !nameField.IsShareable() {
		t.Error("expected name field to be shareable")
	}
}

func TestNewSubGraphV2_WithNonResolvableKey(t *testing.T) {
	schema := `
		type Product @key(fields: "id", resolvable: false) {
			id: ID!
			name: String!
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	if len(productEntity.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(productEntity.Keys))
	}

	if productEntity.Keys[0].Resolvable {
		t.Error("expected key to be non-resolvable")
	}
}

func TestNewSubGraphV2_WithOverride(t *testing.T) {
	schema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @override(from: "products")
		}
	`

	sg, err := graph.NewSubGraphV2("product-v2", []byte(schema), "http://product-v2.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	override := nameField.GetOverride()
	if override == nil {
		t.Fatal("expected override metadata, got nil")
	}

	if override.From != "products" {
		t.Errorf("expected override from 'products', got '%s'", override.From)
	}
}

func TestNewSubGraphV2_WithInaccessible(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			internalCode: String! @inaccessible
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	internalCodeField, ok := productEntity.Fields["internalCode"]
	if !ok {
		t.Fatal("internalCode field not found")
	}

	if !internalCodeField.IsInaccessible() {
		t.Error("expected internalCode field to be inaccessible")
	}

	// Check that other fields are not inaccessible
	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	if nameField.IsInaccessible() {
		t.Error("expected name field to be accessible")
	}
}

func TestNewSubGraphV2_WithTag(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @tag(name: "public")
			price: Float! @tag(name: "public") @tag(name: "partner")
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	productEntity, ok := entities["Product"]
	if !ok {
		t.Fatal("Product entity not found")
	}

	nameField, ok := productEntity.Fields["name"]
	if !ok {
		t.Fatal("name field not found")
	}

	tags := nameField.GetTags()
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(tags))
	}

	if tags[0] != "public" {
		t.Errorf("expected tag 'public', got '%s'", tags[0])
	}

	priceField, ok := productEntity.Fields["price"]
	if !ok {
		t.Fatal("price field not found")
	}

	tags = priceField.GetTags()
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}

	if tags[0] != "public" || tags[1] != "partner" {
		t.Errorf("expected tags 'public' and 'partner', got %v", tags)
	}
}

func TestNewSubGraphV2_WithInterfaceObject(t *testing.T) {
	schema := `
		type Node @key(fields: "id") @interfaceObject {
			id: ID!
		}
	`

	sg, err := graph.NewSubGraphV2("nodes", []byte(schema), "http://nodes.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entities := sg.GetEntities()
	nodeEntity, ok := entities["Node"]
	if !ok {
		t.Fatal("Node entity not found")
	}

	if !nodeEntity.IsInterfaceObject() {
		t.Error("expected Node entity to be an interface object")
	}
}

func TestNewSubGraphV2_WithComposeDirective(t *testing.T) {
	schema := `
		schema @composeDirective(name: "@custom") {
			query: Query
		}

		directive @custom on FIELD_DEFINITION

		type Product @key(fields: "id") {
			id: ID!
			name: String! @custom
		}

		type Query {
			product(id: ID!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	composeDirectives := sg.GetComposeDirectives()
	if len(composeDirectives) != 1 {
		t.Fatalf("expected 1 compose directive, got %d", len(composeDirectives))
	}

	if composeDirectives[0] != "@custom" {
		t.Errorf("expected compose directive '@custom', got '%s'", composeDirectives[0])
	}
}
