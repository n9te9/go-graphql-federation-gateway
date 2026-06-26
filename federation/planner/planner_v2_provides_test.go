package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	parserPkg "github.com/n9te9/graphql-parser/parser"
)

// helper: parse a GraphQL query into a Plan
func parsePlan(t *testing.T, p *planner.PlannerV2, query string) *planner.PlanV2 {
	t.Helper()
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
	return plan
}

// countEntitySteps returns the number of StepTypeEntity steps in the plan.
func countEntitySteps(plan *planner.PlanV2) int {
	n := 0
	for _, s := range plan.Steps {
		if s.StepType == planner.StepTypeEntity {
			n++
		}
	}
	return n
}

// TestPlannerV2_Provides_SkipsEntityStep verifies that when a field is annotated with
// @provides covering ALL requested child fields, the planner skips creating an entity step.
func TestPlannerV2_Provides_SkipsEntityStep(t *testing.T) {
	// posts service: owns Post, provides author.name via @provides
	postsSchema := `
		type Post @key(fields: "id") {
			id: ID!
			title: String!
			author: User! @provides(fields: "name")
		}

		type User @key(fields: "id") {
			id: ID! @external
			name: String! @external
		}

		type Query {
			post(id: ID!): Post
		}
	`

	// users service: owns User
	usersSchema := `
		type User @key(fields: "id") {
			id: ID!
			name: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	postsSG, err := graph.NewSubGraphV2("posts", []byte(postsSchema), "http://posts.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (posts) failed: %v", err)
	}
	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{postsSG, usersSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	// Query requests ONLY "name" — fully covered by @provides(fields: "name")
	plan := parsePlan(t, p, `{ post(id: "p1") { title author { name } } }`)

	entitySteps := countEntitySteps(plan)
	if entitySteps != 0 {
		t.Errorf("expected 0 entity steps (all fields provided), got %d", entitySteps)
		for _, s := range plan.Steps {
			t.Logf("  step %d: type=%v subgraph=%s parentType=%s", s.ID, s.StepType, s.SubGraph.Name, s.ParentType)
		}
	}
}

// TestPlannerV2_Provides_CreatesEntityStepForUncoveredFields verifies that when a field has
// @provides but the query requests additional fields NOT in @provides, an entity step IS created.
func TestPlannerV2_Provides_CreatesEntityStepForUncoveredFields(t *testing.T) {
	postsSchema := `
		type Post @key(fields: "id") {
			id: ID!
			title: String!
			author: User! @provides(fields: "name")
		}

		type User @key(fields: "id") {
			id: ID! @external
			name: String! @external
			email: String! @external
		}

		type Query {
			post(id: ID!): Post
		}
	`

	usersSchema := `
		type User @key(fields: "id") {
			id: ID!
			name: String!
			email: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	postsSG, err := graph.NewSubGraphV2("posts", []byte(postsSchema), "http://posts.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (posts) failed: %v", err)
	}
	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{postsSG, usersSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	// Query requests "email" which is NOT in @provides(fields: "name") → entity step needed
	plan := parsePlan(t, p, `{ post(id: "p1") { title author { name email } } }`)

	entitySteps := countEntitySteps(plan)
	if entitySteps == 0 {
		t.Error("expected entity step for 'email' (not covered by @provides), got 0")
	}
}

// TestPlannerV2_Provides_NoProvides_CreatesEntityStep verifies that without @provides,
// the entity step is always created (baseline / no regression).
func TestPlannerV2_Provides_NoProvides_CreatesEntityStep(t *testing.T) {
	postsSchema := `
		type Post @key(fields: "id") {
			id: ID!
			title: String!
			author: User!
		}

		type User @key(fields: "id") {
			id: ID! @external
			name: String! @external
		}

		type Query {
			post(id: ID!): Post
		}
	`

	usersSchema := `
		type User @key(fields: "id") {
			id: ID!
			name: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	postsSG, err := graph.NewSubGraphV2("posts", []byte(postsSchema), "http://posts.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (posts) failed: %v", err)
	}
	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{postsSG, usersSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	plan := parsePlan(t, p, `{ post(id: "p1") { title author { name } } }`)

	entitySteps := countEntitySteps(plan)
	if entitySteps == 0 {
		t.Error("expected entity step when @provides is absent, got 0")
	}
}

// TestPlannerV2_Provides_MultipleFields verifies @provides(fields: "name email") covers
// a query requesting both "name" and "email".
func TestPlannerV2_Provides_MultipleFields(t *testing.T) {
	postsSchema := `
		type Post @key(fields: "id") {
			id: ID!
			title: String!
			author: User! @provides(fields: "name email")
		}

		type User @key(fields: "id") {
			id: ID! @external
			name: String! @external
			email: String! @external
		}

		type Query {
			post(id: ID!): Post
		}
	`

	usersSchema := `
		type User @key(fields: "id") {
			id: ID!
			name: String!
			email: String!
		}

		type Query {
			user(id: ID!): User
		}
	`

	postsSG, err := graph.NewSubGraphV2("posts", []byte(postsSchema), "http://posts.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (posts) failed: %v", err)
	}
	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{postsSG, usersSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	// Both name and email are in @provides → no entity step
	plan := parsePlan(t, p, `{ post(id: "p1") { author { name email } } }`)

	entitySteps := countEntitySteps(plan)
	if entitySteps != 0 {
		t.Errorf("expected 0 entity steps (both fields provided), got %d", entitySteps)
	}
}

// TestPlannerV2_Provides_NestedFieldSet_SkipsEntityStep verifies that when @provides
// declares nested fields covering ALL requested child fields, the entity step is skipped.
func TestPlannerV2_Provides_NestedFieldSet_SkipsEntityStep(t *testing.T) {
	postsSchema := `
		type Address {
			city: String!
			country: String!
		}

		type Post @key(fields: "id") {
			id: ID!
			title: String!
			author: User! @provides(fields: "address { city country }")
		}

		type User @key(fields: "id") {
			id: ID! @external
			address: Address! @external
		}

		type Query {
			post(id: ID!): Post
		}
	`

	usersSchema := `
		type Address {
			city: String!
			country: String!
		}

		type User @key(fields: "id") {
			id: ID!
			address: Address!
		}

		type Query {
			user(id: ID!): User
		}
	`

	postsSG, err := graph.NewSubGraphV2("posts", []byte(postsSchema), "http://posts.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (posts) failed: %v", err)
	}
	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{postsSG, usersSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	// Query requests address { city country } — fully covered by @provides
	plan := parsePlan(t, p, `{ post(id: "p1") { title author { address { city country } } } }`)

	entitySteps := countEntitySteps(plan)
	if entitySteps != 0 {
		t.Errorf("expected 0 entity steps (all fields provided via nested @provides), got %d", entitySteps)
		for _, s := range plan.Steps {
			t.Logf("  step %d: type=%v subgraph=%s parentType=%s", s.ID, s.StepType, s.SubGraph.Name, s.ParentType)
		}
	}
}

// TestPlannerV2_Provides_NestedFieldSet_PartialCoverage verifies that when @provides
// declares nested fields but the query requests uncovered fields, an entity step IS created.
func TestPlannerV2_Provides_NestedFieldSet_PartialCoverage(t *testing.T) {
	postsSchema := `
		type Address {
			city: String!
			country: String!
			state: String!
		}

		type Post @key(fields: "id") {
			id: ID!
			title: String!
			author: User! @provides(fields: "address { city }")
		}

		type User @key(fields: "id") {
			id: ID! @external
			address: Address! @external
		}

		type Query {
			post(id: ID!): Post
		}
	`

	usersSchema := `
		type Address {
			city: String!
			country: String!
			state: String!
		}

		type User @key(fields: "id") {
			id: ID!
			address: Address!
		}

		type Query {
			user(id: ID!): User
		}
	`

	postsSG, err := graph.NewSubGraphV2("posts", []byte(postsSchema), "http://posts.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (posts) failed: %v", err)
	}
	usersSG, err := graph.NewSubGraphV2("users", []byte(usersSchema), "http://users.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}

	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{postsSG, usersSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(sg)

	// Query requests address { city country } but @provides only covers address { city }
	// → "country" is NOT covered → entity step needed
	plan := parsePlan(t, p, `{ post(id: "p1") { title author { address { city country } } } }`)

	entitySteps := countEntitySteps(plan)
	if entitySteps == 0 {
		t.Error("expected entity step for 'country' (not covered by @provides), got 0")
	}
}

// TestPlannerV2_ProvidesFieldOptimization is the original parsing test, kept for regression.
func TestPlannerV2_ProvidesFieldOptimization(t *testing.T) {
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

	entity, exists := productSG.GetEntity("Product")
	if !exists {
		t.Fatal("Product entity not found")
	}

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
