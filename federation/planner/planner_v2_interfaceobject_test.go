package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_InterfaceObjectDirective_BasicQuery tests query planning when
// a type is defined with @interfaceObject (object type pattern).
// Scenario: CoreService has `type Node @interfaceObject @key(fields: "id")` (base query field)
//
//	MetadataService has `type Node @interfaceObject @key(fields: "id")` (additional field)
func TestPlannerV2_InterfaceObjectDirective_BasicQuery(t *testing.T) {
	coreSchema := `
		type Node @key(fields: "id") @interfaceObject {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	reviewsSchema := `
		extend type Node @key(fields: "id") @interfaceObject {
			id: ID! @external
			reviewCount: Int!
		}
	`

	coreSG, err := graph.NewSubGraphV2("core", []byte(coreSchema), "http://core.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for core: %v", err)
	}

	reviewsSG, err := graph.NewSubGraphV2("reviews", []byte(reviewsSchema), "http://reviews.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for reviews: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, reviewsSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			node(id: "123") {
				id
				reviewCount
			}
		}
	`

	l := lexer.New(query)
	pr := parser.New(l)
	doc := pr.ParseDocument()
	if len(pr.Errors()) > 0 {
		t.Fatalf("parse error: %v", pr.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should have at least 2 steps:
	// Step 0: root query to core (node { id })
	// Step 1: entity fetch to reviews (reviewCount)
	if len(plan.Steps) < 2 {
		t.Fatalf("expected at least 2 steps, got %d", len(plan.Steps))
	}

	// Step 0: Query step to core service
	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("expected step 0 to be query type, got %v", plan.Steps[0].StepType)
	}
	if plan.Steps[0].SubGraph.Name != "core" {
		t.Errorf("expected step 0 to go to 'core', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	// Find the entity step for reviews
	var reviewsStep *planner.StepV2
	for _, step := range plan.Steps {
		if step.StepType == planner.StepTypeEntity && step.SubGraph.Name == "reviews" {
			reviewsStep = step
			break
		}
	}

	if reviewsStep == nil {
		t.Fatal("expected an entity step for reviews service, got none")
	}

	// The entity step should resolve the Node entity
	if reviewsStep.ParentType != "Node" {
		t.Errorf("expected entity step ParentType to be 'Node', got '%s'", reviewsStep.ParentType)
	}
}

// TestPlannerV2_InterfaceObjectDirective_InterfaceType tests query planning when
// a type is defined as `interface Node @interfaceObject @key(fields: "id")`.
func TestPlannerV2_InterfaceObjectDirective_InterfaceType(t *testing.T) {
	// CoreService: Node is defined as an interface with @interfaceObject
	coreSchema := `
		interface Node @interfaceObject @key(fields: "id") {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	// MetadataService: Node is extended as interface with @interfaceObject
	metadataSchema := `
		extend interface Node @interfaceObject @key(fields: "id") {
			id: ID! @external
			createdAt: String!
		}
	`

	coreSG, err := graph.NewSubGraphV2("core", []byte(coreSchema), "http://core.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for core: %v", err)
	}

	metadataSG, err := graph.NewSubGraphV2("metadata", []byte(metadataSchema), "http://metadata.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for metadata: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, metadataSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			node(id: "123") {
				id
				createdAt
			}
		}
	`

	l := lexer.New(query)
	pr := parser.New(l)
	doc := pr.ParseDocument()
	if len(pr.Errors()) > 0 {
		t.Fatalf("parse error: %v", pr.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should have at least 2 steps:
	// Step 0: root query to core (node { id })
	// Step 1: entity fetch to metadata (createdAt)
	if len(plan.Steps) < 2 {
		t.Fatalf("expected at least 2 steps, got %d", len(plan.Steps))
	}

	// Step 0: should be a query step to core
	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("expected step 0 to be query type, got %v", plan.Steps[0].StepType)
	}
	if plan.Steps[0].SubGraph.Name != "core" {
		t.Errorf("expected step 0 to go to 'core', got '%s'", plan.Steps[0].SubGraph.Name)
	}

	// Find the entity step for metadata service
	var metadataStep *planner.StepV2
	for _, step := range plan.Steps {
		if step.StepType == planner.StepTypeEntity && step.SubGraph.Name == "metadata" {
			metadataStep = step
			break
		}
	}

	if metadataStep == nil {
		t.Fatal("expected an entity step for metadata service, got none")
	}

	// ParentType should be "Node"
	if metadataStep.ParentType != "Node" {
		t.Errorf("expected entity step ParentType to be 'Node', got '%s'", metadataStep.ParentType)
	}
}

// TestPlannerV2_InterfaceObjectDirective_MultipleSubgraphs tests planning with
// three subgraphs where Node @interfaceObject is extended in two services.
func TestPlannerV2_InterfaceObjectDirective_MultipleSubgraphs(t *testing.T) {
	coreSchema := `
		type Node @key(fields: "id") @interfaceObject {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	reviewsSchema := `
		extend type Node @key(fields: "id") @interfaceObject {
			id: ID! @external
			reviewCount: Int!
		}
	`

	analyticsSchema := `
		extend type Node @key(fields: "id") @interfaceObject {
			id: ID! @external
			viewCount: Int!
		}
	`

	coreSG, _ := graph.NewSubGraphV2("core", []byte(coreSchema), "http://core.example.com")
	reviewsSG, _ := graph.NewSubGraphV2("reviews", []byte(reviewsSchema), "http://reviews.example.com")
	analyticsSG, _ := graph.NewSubGraphV2("analytics", []byte(analyticsSchema), "http://analytics.example.com")

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, reviewsSG, analyticsSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			node(id: "123") {
				id
				reviewCount
				viewCount
			}
		}
	`

	l := lexer.New(query)
	pr := parser.New(l)
	doc := pr.ParseDocument()

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should have 3 steps: 1 root + 2 entity fetches
	if len(plan.Steps) < 3 {
		t.Fatalf("expected at least 3 steps (root + 2 entity), got %d", len(plan.Steps))
	}

	// Verify both reviews and analytics have entity steps
	reviewsFound := false
	analyticsFound := false
	for _, step := range plan.Steps {
		if step.StepType == planner.StepTypeEntity {
			switch step.SubGraph.Name {
			case "reviews":
				reviewsFound = true
				if step.ParentType != "Node" {
					t.Errorf("expected reviews entity step ParentType 'Node', got '%s'", step.ParentType)
				}
			case "analytics":
				analyticsFound = true
				if step.ParentType != "Node" {
					t.Errorf("expected analytics entity step ParentType 'Node', got '%s'", step.ParentType)
				}
			}
		}
	}

	if !reviewsFound {
		t.Error("expected entity step for reviews service")
	}
	if !analyticsFound {
		t.Error("expected entity step for analytics service")
	}
}
