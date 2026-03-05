package graph_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
)

// TestParseKeyFieldSet tests the parseKeyFieldSet function via EntityKey.ParsedFields.
// We access parsed fields through NewSubGraphV2 which calls parseEntityKeys internally.
func TestParseKeyFieldSet_ScalarKey(t *testing.T) {
	schema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`
	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entity, ok := sg.GetEntity("Product")
	if !ok {
		t.Fatal("expected Product entity")
	}
	if len(entity.Keys) == 0 {
		t.Fatal("expected at least one key")
	}

	parsed := entity.Keys[0].ParsedFields
	if len(parsed) != 1 {
		t.Fatalf("expected 1 parsed field, got %d", len(parsed))
	}
	if parsed[0].Name != "id" {
		t.Errorf("expected Name='id', got %q", parsed[0].Name)
	}
	if parsed[0].Fields != nil {
		t.Errorf("expected leaf node (Fields==nil) for scalar key, got %v", parsed[0].Fields)
	}
}

func TestParseKeyFieldSet_CompositeKey(t *testing.T) {
	schema := `
		type Flight @key(fields: "number departureDate") {
			number: String!
			departureDate: String!
			destination: String!
		}
		type Query { flight(number: String!, departureDate: String!): Flight }
	`
	sg, err := graph.NewSubGraphV2("flight", []byte(schema), "http://flight.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entity, ok := sg.GetEntity("Flight")
	if !ok {
		t.Fatal("expected Flight entity")
	}

	parsed := entity.Keys[0].ParsedFields
	if len(parsed) != 2 {
		t.Fatalf("expected 2 parsed fields, got %d", len(parsed))
	}
	if parsed[0].Name != "number" {
		t.Errorf("expected parsed[0].Name='number', got %q", parsed[0].Name)
	}
	if parsed[1].Name != "departureDate" {
		t.Errorf("expected parsed[1].Name='departureDate', got %q", parsed[1].Name)
	}
}

func TestParseKeyFieldSet_NestedKey(t *testing.T) {
	schema := `
		type Location @key(fields: "coordinate { lat lng }") {
			coordinate: Coordinate!
			name: String!
		}
		type Coordinate {
			lat: Float!
			lng: Float!
		}
		type Query { location(id: ID!): Location }
	`
	sg, err := graph.NewSubGraphV2("location", []byte(schema), "http://location.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entity, ok := sg.GetEntity("Location")
	if !ok {
		t.Fatal("expected Location entity")
	}

	parsed := entity.Keys[0].ParsedFields
	if len(parsed) != 1 {
		t.Fatalf("expected 1 top-level parsed field, got %d", len(parsed))
	}

	coord := parsed[0]
	if coord.Name != "coordinate" {
		t.Errorf("expected Name='coordinate', got %q", coord.Name)
	}
	if len(coord.Fields) != 2 {
		t.Fatalf("expected 2 child fields under coordinate, got %d", len(coord.Fields))
	}
	if coord.Fields[0].Name != "lat" {
		t.Errorf("expected Fields[0].Name='lat', got %q", coord.Fields[0].Name)
	}
	if coord.Fields[1].Name != "lng" {
		t.Errorf("expected Fields[1].Name='lng', got %q", coord.Fields[1].Name)
	}
	// Children should be leaf nodes
	if coord.Fields[0].Fields != nil {
		t.Error("expected lat to be a leaf node")
	}
	if coord.Fields[1].Fields != nil {
		t.Error("expected lng to be a leaf node")
	}
}

func TestParseKeyFieldSet_MixedFlatAndNested(t *testing.T) {
	schema := `
		type Order @key(fields: "id location { lat lng }") {
			id: ID!
			location: Coordinate!
		}
		type Coordinate {
			lat: Float!
			lng: Float!
		}
		type Query { order(id: ID!): Order }
	`
	sg, err := graph.NewSubGraphV2("order", []byte(schema), "http://order.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entity, ok := sg.GetEntity("Order")
	if !ok {
		t.Fatal("expected Order entity")
	}

	parsed := entity.Keys[0].ParsedFields
	if len(parsed) != 2 {
		t.Fatalf("expected 2 top-level parsed fields, got %d", len(parsed))
	}

	// First: scalar "id"
	if parsed[0].Name != "id" {
		t.Errorf("expected parsed[0].Name='id', got %q", parsed[0].Name)
	}
	if parsed[0].Fields != nil {
		t.Error("expected 'id' to be a leaf node")
	}

	// Second: nested "location { lat lng }"
	if parsed[1].Name != "location" {
		t.Errorf("expected parsed[1].Name='location', got %q", parsed[1].Name)
	}
	if len(parsed[1].Fields) != 2 {
		t.Fatalf("expected 2 children under location, got %d", len(parsed[1].Fields))
	}
}

func TestParseKeyFieldSet_DeeplyNested(t *testing.T) {
	schema := `
		type Shipment @key(fields: "destination { address { zip } }") {
			destination: Destination!
		}
		type Destination {
			address: Address!
		}
		type Address {
			zip: String!
		}
		type Query { shipment(id: ID!): Shipment }
	`
	sg, err := graph.NewSubGraphV2("shipment", []byte(schema), "http://shipment.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	entity, ok := sg.GetEntity("Shipment")
	if !ok {
		t.Fatal("expected Shipment entity")
	}

	parsed := entity.Keys[0].ParsedFields
	if len(parsed) != 1 {
		t.Fatalf("expected 1 top-level field, got %d", len(parsed))
	}

	dest := parsed[0]
	if dest.Name != "destination" {
		t.Errorf("expected 'destination', got %q", dest.Name)
	}
	if len(dest.Fields) != 1 {
		t.Fatalf("expected 1 child under destination, got %d", len(dest.Fields))
	}

	addr := dest.Fields[0]
	if addr.Name != "address" {
		t.Errorf("expected 'address', got %q", addr.Name)
	}
	if len(addr.Fields) != 1 {
		t.Fatalf("expected 1 child under address, got %d", len(addr.Fields))
	}
	if addr.Fields[0].Name != "zip" {
		t.Errorf("expected 'zip', got %q", addr.Fields[0].Name)
	}
}

func TestParseKeyFieldSet_EmptyFieldSet(t *testing.T) {
	// No @key directive — entity should have no keys
	schema := `
		type Product {
			id: ID!
		}
		type Query { product(id: ID!): Product }
	`
	sg, err := graph.NewSubGraphV2("product", []byte(schema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	_, ok := sg.GetEntity("Product")
	if ok {
		t.Error("expected no entity for type without @key")
	}
}
