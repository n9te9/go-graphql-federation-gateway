package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	parserPkg "github.com/n9te9/graphql-parser/parser"
)

// findFieldInSelections searches for a field by name in a selection set (non-recursive).
func findFieldInSelections(sels []ast.Selection, name string) *ast.Field {
	for _, sel := range sels {
		if f, ok := sel.(*ast.Field); ok && f.Name.String() == name {
			return f
		}
	}
	return nil
}

// hasFieldName checks whether a field with the given name exists in the selection set.
func hasFieldName(sels []ast.Selection, name string) bool {
	return findFieldInSelections(sels, name) != nil
}

// TestPlannerV2_NestKey_SingleNested verifies that a simple nested @key
// (e.g. "coordinate { lat lng }") is correctly injected into the parent step.
func TestPlannerV2_NestKey_SingleNested(t *testing.T) {
	// LocationService owns Location with nested key
	locationSchema := `
		type Location @key(fields: "coordinate { lat lng }") {
			coordinate: Coordinate!
			name: String!
		}

		type Coordinate {
			lat: Float!
			lng: Float!
		}

		type Query {
			location(id: ID!): Location
		}
	`

	// WeatherService extends Location with nested key reference
	weatherSchema := `
		extend type Location @key(fields: "coordinate { lat lng }") {
			coordinate: Coordinate! @external
			weather: String!
		}

		type Coordinate {
			lat: Float!
			lng: Float!
		}
	`

	locationSG, err := graph.NewSubGraphV2("location", []byte(locationSchema), "http://location.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (location) failed: %v", err)
	}
	weatherSG, err := graph.NewSubGraphV2("weather", []byte(weatherSchema), "http://weather.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (weather) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{locationSG, weatherSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	query := `
		query {
			location(id: "1") {
				name
				weather
			}
		}
	`

	l := lexer.New(query)
	par := parserPkg.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse error: %v", par.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(plan.Steps) < 2 {
		t.Fatalf("expected at least 2 steps, got %d", len(plan.Steps))
	}

	// Step 0: root query to location service
	rootStep := plan.Steps[0]
	if rootStep.StepType != planner.StepTypeQuery {
		t.Errorf("expected step[0] to be StepTypeQuery, got %v", rootStep.StepType)
	}

	// The root step's SelectionSet should contain `location { ... coordinate { lat lng } ... }`
	locationField := findFieldInSelections(rootStep.SelectionSet, "location")
	if locationField == nil {
		t.Fatal("expected 'location' field in root step SelectionSet")
	}

	// coordinate should be injected as a nested selection
	coordinateField := findFieldInSelections(locationField.SelectionSet, "coordinate")
	if coordinateField == nil {
		t.Fatal("expected 'coordinate' to be injected into location SelectionSet for nested key")
	}

	// coordinate should have lat and lng children
	if !hasFieldName(coordinateField.SelectionSet, "lat") {
		t.Error("expected 'lat' injected under coordinate")
	}
	if !hasFieldName(coordinateField.SelectionSet, "lng") {
		t.Error("expected 'lng' injected under coordinate")
	}

	// Step 1: entity step to weather service
	entityStep := plan.Steps[1]
	if entityStep.StepType != planner.StepTypeEntity {
		t.Errorf("expected step[1] to be StepTypeEntity, got %v", entityStep.StepType)
	}
}

// TestPlannerV2_NestKey_MixedFlatAndNested verifies that a mixed key
// ("id location { lat lng }") injects both flat and nested key fields.
func TestPlannerV2_NestKey_MixedFlatAndNested(t *testing.T) {
	ownerSchema := `
		type Order @key(fields: "id location { lat lng }") {
			id: ID!
			location: Coordinate!
			status: String!
		}

		type Coordinate {
			lat: Float!
			lng: Float!
		}

		type Query {
			order(id: ID!): Order
		}
	`

	extSchema := `
		extend type Order @key(fields: "id location { lat lng }") {
			id: ID! @external
			location: Coordinate! @external
			tracking: String!
		}

		type Coordinate {
			lat: Float!
			lng: Float!
		}
	`

	ownerSG, err := graph.NewSubGraphV2("orders", []byte(ownerSchema), "http://orders.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (orders) failed: %v", err)
	}
	extSG, err := graph.NewSubGraphV2("tracking", []byte(extSchema), "http://tracking.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (tracking) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{ownerSG, extSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	query := `
		query {
			order(id: "o1") {
				status
				tracking
			}
		}
	`

	l := lexer.New(query)
	par := parserPkg.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse error: %v", par.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(plan.Steps) < 2 {
		t.Fatalf("expected at least 2 steps, got %d", len(plan.Steps))
	}

	rootStep := plan.Steps[0]
	orderField := findFieldInSelections(rootStep.SelectionSet, "order")
	if orderField == nil {
		t.Fatal("expected 'order' in root step SelectionSet")
	}

	// flat "id" should be injected
	if !hasFieldName(orderField.SelectionSet, "id") {
		t.Error("expected flat key field 'id' injected into order SelectionSet")
	}

	// nested "location { lat lng }" should be injected
	locationField := findFieldInSelections(orderField.SelectionSet, "location")
	if locationField == nil {
		t.Fatal("expected nested key field 'location' injected into order SelectionSet")
	}
	if !hasFieldName(locationField.SelectionSet, "lat") {
		t.Error("expected 'lat' under location")
	}
	if !hasFieldName(locationField.SelectionSet, "lng") {
		t.Error("expected 'lng' under location")
	}
}
