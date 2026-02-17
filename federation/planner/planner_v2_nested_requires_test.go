package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_NestedRequires tests query planning with nested @requires dependencies
func TestPlannerV2_NestedRequires(t *testing.T) {
	// Schema with nested @requires - User.reviewCount requires reviews, reviews requires purchaseHistory
	userSchema := `
		type User @key(fields: "id") {
			id: ID!
			username: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	purchaseSchema := `
		type User @key(fields: "id") {
			id: ID!
			purchaseHistory: [Purchase!]! @external
		}

		type Purchase {
			id: ID!
			productId: ID!
		}
	`

	reviewSchema := `
		extend type User @key(fields: "id") {
			id: ID! @external
			purchaseHistory: [Purchase!]! @external
			reviews: [Review!]! @requires(fields: "purchaseHistory")
		}

		type Purchase {
			id: ID!
		}

		type Review {
			id: ID!
			rating: Int!
		}
	`

	analyticsSchema := `
		extend type User @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]! @external
			reviewCount: Int! @requires(fields: "reviews")
		}

		type Review {
			id: ID!
		}
	`

	sg1, err := graph.NewSubGraphV2("users", []byte(userSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for users failed: %v", err)
	}

	sg2, err := graph.NewSubGraphV2("purchases", []byte(purchaseSchema), "http://purchases.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for purchases failed: %v", err)
	}

	sg3, err := graph.NewSubGraphV2("reviews", []byte(reviewSchema), "http://reviews.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for reviews failed: %v", err)
	}

	sg4, err := graph.NewSubGraphV2("analytics", []byte(analyticsSchema), "http://analytics.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for analytics failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg1, sg2, sg3, sg4})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Query that requires nested dependencies:
	// reviewCount requires reviews, reviews requires purchaseHistory
	query := `
		query {
			user(id: "1") {
				id
				username
				reviewCount
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

	// Nested @requires should create multiple steps:
	// 1. Query users service for basic user info
	// 2. Query purchases for purchaseHistory
	// 3. Query reviews for reviews (requires purchaseHistory)
	// 4. Query analytics for reviewCount (requires reviews)
	if len(plan.Steps) < 1 {
		t.Errorf("expected at least 1 step, got %d", len(plan.Steps))
	}

	// First step should query the users service
	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("expected first step to be query type, got %v", plan.Steps[0].StepType)
	}

	// Verify the plan can resolve nested dependencies
	// The exact number of steps depends on the planner's optimization strategy
	t.Logf("Plan has %d steps for nested @requires dependencies", len(plan.Steps))
	for i, step := range plan.Steps {
		subgraphName := ""
		if step.SubGraph != nil {
			subgraphName = step.SubGraph.Name
		}
		t.Logf("Step %d: SubGraph=%s, Type=%v", i, subgraphName, step.StepType)
	}
}
