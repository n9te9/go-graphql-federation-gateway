package planner_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

func TestPlanner_Plan(t *testing.T) {
	tests := []struct {
		name       string
		doc        *ast.Document
		superGraph *graph.SuperGraph
		variables  map[string]any
		want       *planner.Plan
		wantErr    error
	}{
		{
			name: "Happy case: Plan simple query",
			doc: func() *ast.Document {
				l := lexer.New(`
                    query {
                        products {
                            upc
                            name
                            width
                            height
                        }
                    }
                `)
				p := parser.New(l)
				doc := p.ParseDocument()
				if len(p.Errors()) > 0 {
					t.Fatal(p.Errors())
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				sdl := `type Query { products: [Product] } type Product { upc: String! name: String price: Int }`
				sg1, _ := graph.NewSubGraph("aaaaaaaaa", []byte(sdl), "")
				subgraphSDL := `extend type Product @key(fields: "upc") { upc: String! @external width: Int height: Int price: Int @external }`
				sg2, _ := graph.NewSubGraph("hogehoge", []byte(subgraphSDL), "")
				superGraph, _ := graph.NewSuperGraph([]*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				RootSelections: []*planner.Selection{
					{
						ParentType: "Query",
						Field:      "products",
						Arguments:  map[string]any{},
						SubSelections: []*planner.Selection{
							{ParentType: "Product", Field: "upc", Arguments: map[string]any{}, SubSelections: nil},
							{ParentType: "Product", Field: "name", Arguments: map[string]any{}, SubSelections: nil},
							{ParentType: "Product", Field: "width", Arguments: map[string]any{}, SubSelections: nil},
							{ParentType: "Product", Field: "height", Arguments: map[string]any{}, SubSelections: nil},
						},
					},
				},
				Steps: []*planner.Step{
					{
						ID:       0,
						SubGraph: &graph.SubGraph{Name: "aaaaaaaaa", OwnershipTypes: map[string]struct{}{"Product": {}, "Query": {}}},
						RootFields: []string{
							"products",
						},
						Selections: []*planner.Selection{
							{
								ParentType:    "Product",
								Field:         "upc",
								Arguments:     map[string]any{},
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "name",
								Arguments:     map[string]any{},
								SubSelections: []*planner.Selection{},
							},
						},
						RootArguments: map[string]map[string]any{
							"products": {},
						},
						OperationType: "query",
					},
					{
						ID:       1,
						SubGraph: &graph.SubGraph{Name: "hogehoge", OwnershipTypes: map[string]struct{}{}},
						Selections: []*planner.Selection{
							{
								ParentType:    "Product",
								Field:         "width",
								Arguments:     map[string]any{},
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "height",
								Arguments:     map[string]any{},
								SubSelections: []*planner.Selection{},
							},
						},
						DependsOn: []int{0},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := planner.NewPlanner(tt.superGraph, planner.PlannerOption{})
			got, err := p.Plan(tt.doc, tt.variables)
			if err != nil {
				if tt.wantErr == nil {
					t.Errorf("Planner.Plan() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if err.Error() != tt.wantErr.Error() {
					t.Errorf("Planner.Plan() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				return
			}

			if diff := cmp.Diff(tt.want, got,
				cmpopts.IgnoreUnexported(planner.Step{}, graph.SubGraph{}),
				cmpopts.IgnoreFields(ast.Document{}, "Definitions"),
				cmpopts.IgnoreFields(graph.SubGraph{}, "SDL", "Schema"),
			); diff != "" {
				t.Errorf("Planner.Plan() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
