package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_MultipleKeyDefinitions tests that a type with multiple @key definitions
// can be resolved using any of the keys.
func TestPlannerV2_MultipleKeyDefinitions(t *testing.T) {
	// User service with multiple keys (@key(fields: "id") and @key(fields: "username"))
	userSchema := `
		type User @key(fields: "id") @key(fields: "username") {
			id: ID!
			username: String!
			email: String!
		}

		type Query {
			user(id: ID!): User
			userByUsername(username: String!): User
		}
	`

	userSG, err := graph.NewSubGraphV2("user", []byte(userSchema), "http://user.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for user: %v", err)
	}

	// Post service extends User using the username key
	postSchema := `
		extend type User @key(fields: "username") {
			username: String! @external
			posts: [Post!]!
		}

		type Post {
			id: ID!
			title: String!
		}
	`

	postSG, err := graph.NewSubGraphV2("post", []byte(postSchema), "http://post.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for post: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{userSG, postSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Query user by ID and fetch posts (requires entity resolution with username key)
	query := `
		query {
			user(id: "1") {
				id
				username
				posts {
					title
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

	// Should have at least 2 steps:
	// 1. Query user service for user(id: "1") -> gets id, username
	// 2. Query post service with username for posts
	if len(plan.Steps) < 2 {
		t.Errorf("expected at least 2 steps for federation across multiple keys, got %d", len(plan.Steps))
	}

	// First step should be a query step to user service
	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("expected first step to be query type, got %v", plan.Steps[0].StepType)
	}

	// Second step should be entity step using username key
	if len(plan.Steps) > 1 && plan.Steps[1].StepType != planner.StepTypeEntity {
		t.Errorf("expected second step to be entity type, got %v", plan.Steps[1].StepType)
	}
}

// TestPlannerV2_MultipleKeyDefinitions_AlternateKey tests resolving entity using alternate key
func TestPlannerV2_MultipleKeyDefinitions_AlternateKey(t *testing.T) {
	// User service with multiple keys
	userSchema := `
		type User @key(fields: "id") @key(fields: "email") {
			id: ID!
			username: String!
			email: String!
		}

		type Query {
			user(id: ID!): User
			userByEmail(email: String!): User
		}
	`

	userSG, err := graph.NewSubGraphV2("user", []byte(userSchema), "http://user.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for user: %v", err)
	}

	// Profile service extends User using email key
	profileSchema := `
		extend type User @key(fields: "email") {
			email: String! @external
			bio: String!
		}
	`

	profileSG, err := graph.NewSubGraphV2("profile", []byte(profileSchema), "http://profile.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for profile: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{userSG, profileSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Query user by ID and fetch bio (requires entity resolution with email key)
	query := `
		query {
			user(id: "1") {
				email
				bio
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

	// Should have 2 steps: query user, then resolve bio from profile service
	if len(plan.Steps) < 2 {
		t.Errorf("expected at least 2 steps, got %d", len(plan.Steps))
	}

	// Verify we have both query and entity steps
	hasQueryStep := false
	hasEntityStep := false
	for _, step := range plan.Steps {
		if step.StepType == planner.StepTypeQuery {
			hasQueryStep = true
		}
		if step.StepType == planner.StepTypeEntity {
			hasEntityStep = true
		}
	}

	if !hasQueryStep {
		t.Error("expected to have a query step")
	}
	if !hasEntityStep {
		t.Error("expected to have an entity step for alternate key resolution")
	}
}
