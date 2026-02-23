package planner_test

import (
	"fmt"
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// Debug test: can we get len(subGraphs)==0 by using a field that's @external in all subgraphs?
func TestDebugNoSubGraphField(t *testing.T) {
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
		}
		type Query { review(id: ID!): Review }
	`
	ordersSchema := `
		extend type Review @key(fields: "id") {
			id: ID! @external
			ghostProp: String! @external
		}
		type Order @key(fields: "id") { id: ID! amount: Float! }
		type Query { order(id: ID!): Order }
	`
	reviewsSg, _ := graph.NewSubGraphV2("reviews", []byte(reviewSchema), "http://reviews.example.com")
	ordersSg, _ := graph.NewSubGraphV2("orders", []byte(ordersSchema), "http://orders.example.com")
	sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{reviewsSg, ordersSg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2: %v", err)
	}

	// Check Ownership
	key := "Review.ghostProp"
	owners := sg.Ownership[key]
	fmt.Printf("Ownership[%s] = len=%d\n", key, len(owners))

	// Check if ghostProp is in merged schema
	for _, def := range sg.Schema.Definitions {
		if objDef, ok := def.(*ast.ObjectTypeDefinition); ok && objDef.Name.String() == "Review" {
			for _, f := range objDef.Fields {
				fmt.Printf("Review.%s\n", f.Name.String())
			}
		}
	}

	p := planner.NewPlannerV2(sg)

	l := lexer.New(`query { review(id: "R1") { id body ghostProp } order(id: "O1") { id amount } }`)
	par := parser.New(l)
	doc := par.ParseDocument()
	if len(par.Errors()) > 0 {
		t.Fatalf("parse errors: %v", par.Errors())
	}

	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized: %v", err)
	}
	t.Logf("total steps: %d", len(plan.Steps))
}
