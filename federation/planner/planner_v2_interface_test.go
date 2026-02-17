package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_InterfaceTypes tests query planning with GraphQL interface types
func TestPlannerV2_InterfaceTypes(t *testing.T) {
	// Schema with interface type
	schema := ` 
		interface Node {
			id: ID!
		}

		type Product implements Node {
			id: ID!
			name: String!
			price: Int!
		}

		type User implements Node {
			id: ID!
			username: String!
			email: String!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	sg, err := graph.NewSubGraphV2("api", []byte(schema), "http://api.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Query using interface type with inline fragments
	query := `
		query {
			node(id: "1") {
				id
				__typename
				... on Product {
					name
					price
				}
				... on User {
					username
					email
				}
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should have 1 step for querying the node field
	if len(plan.Steps) < 1 {
		t.Errorf("expected at least 1 step, got %d", len(plan.Steps))
	}

	// First step should be a query step
	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("expected first step to be query type, got %v", plan.Steps[0].StepType)
	}

	// The selection set should include __typename and inline fragments
	step := plan.Steps[0]
	if len(step.SelectionSet) == 0 {
		t.Error("expected selection set to have selections")
	}
}
