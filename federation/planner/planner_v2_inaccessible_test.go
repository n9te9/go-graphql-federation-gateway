package planner_test

import (
	"strings"
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	gqlparser "github.com/n9te9/graphql-parser/parser"
)

func buildInaccessibleSuperGraph(t *testing.T) *graph.SuperGraphV2 {
	t.Helper()

	userSchema := `
		type User @key(fields: "id") {
			id: ID!
			name: String!
			internalId: ID! @inaccessible
		}

		type Query {
			user(id: ID!): User
		}
	`

	userSG, err := graph.NewSubGraphV2("users", []byte(userSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{userSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	return superGraph
}

func TestPlannerV2_Inaccessible_NestedFieldReturnsError(t *testing.T) {
	superGraph := buildInaccessibleSuperGraph(t)
	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			user(id: "1") {
				id
				internalId
			}
		}
	`

	l := lexer.New(query)
	par := gqlparser.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse error: %v", par.Errors())
	}

	_, err := p.Plan(doc, nil)
	if err == nil {
		t.Fatal("expected an error when querying @inaccessible field 'internalId', but got nil")
	}
	if !strings.Contains(err.Error(), "inaccessible") {
		t.Errorf("expected error message to mention 'inaccessible', got: %v", err)
	}
}

func TestPlannerV2_Inaccessible_AccessibleFieldsSucceed(t *testing.T) {
	superGraph := buildInaccessibleSuperGraph(t)
	p := planner.NewPlannerV2(superGraph)

	// Only querying accessible fields - should succeed
	query := `
		query {
			user(id: "1") {
				id
				name
			}
		}
	`

	l := lexer.New(query)
	par := gqlparser.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse error: %v", par.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("expected no error for accessible fields, got: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least one step in the plan")
	}
}

func TestPlannerV2_Inaccessible_RootFieldWithInaccessibleNested(t *testing.T) {
	// An @inaccessible field nested in multi-subgraph scenario
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
			internalReviewScore: Float! @inaccessible
		}

		type Review {
			id: ID!
			text: String!
		}
	`

	productSG, err := graph.NewSubGraphV2("products", []byte(productSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}
	reviewSG, err := graph.NewSubGraphV2("reviews", []byte(reviewSchema), "http://reviews.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, reviewSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Querying the @inaccessible field on an extension type
	query := `
		query {
			product(id: "1") {
				id
				name
				internalReviewScore
			}
		}
	`

	l := lexer.New(query)
	par := gqlparser.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse error: %v", par.Errors())
	}

	_, err = p.Plan(doc, nil)
	if err == nil {
		t.Fatal("expected an error when querying @inaccessible field 'internalReviewScore', but got nil")
	}
	if !strings.Contains(err.Error(), "inaccessible") {
		t.Errorf("expected error message to mention 'inaccessible', got: %v", err)
	}
}
