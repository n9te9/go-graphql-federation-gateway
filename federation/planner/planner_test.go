package planner_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/n9te9/federation-gateway/federation/graph"
	"github.com/n9te9/federation-gateway/federation/planner"
	"github.com/n9te9/goliteql/query"
	"github.com/n9te9/goliteql/schema"
)

func TestPlanner_Plan(t *testing.T) {
	tests := []struct {
		name       string
		doc        *query.Document
		superGraph *graph.SuperGraph
		want       *planner.Plan
		wantErr    error
	}{
		{
			name: "Happy case: Plan simple query",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`
					query {
						products {
							upc
							name
							weight
							height
						}
					}
				`))
				if err != nil {
					t.Fatal(err)
				}

				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`
				sg, err := graph.NewRootSubGraph("aaaaaaaaa", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				superGraph, err := graph.NewSuperGraphFromBytes([]byte(sdl))
				if err != nil {
					t.Fatalf("failed to parse root schema: %v", err)
				}
				superGraph.RootGraph = sg

				subgraphSDL := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					height: Int
					price: Int @external
				}`

				sg, err = graph.NewSubGraph("hogehoge", []byte(subgraphSDL), "")
				if err != nil {
					t.Fatal(err)
				}

				if err := superGraph.Merge(sg); err != nil {
					t.Fatal(err)
				}

				return superGraph
			}(),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						SubGraph: func() *graph.SubGraph {
							sdl := `extend type Product @key(fields: "upc") {
								upc: String! @external
								weight: Int
								height: Int
								price: Int @external
							}`
							sg, err := graph.NewSubGraph("hogehoge", []byte(sdl), "")
							if err != nil {
								t.Fatal(err)
							}

							return sg
						}(),
						Selections: []*planner.Selection{
							{
								ParentType: "Product",
								Field:      "weight",
							},
							{
								ParentType: "Product",
								Field:      "height",
							},
						},
						DependsOn: nil,
						Status:    planner.Pending,
						Err:       nil,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := planner.NewPlanner(tt.superGraph)
			got, err := p.Plan(tt.doc)
			if (err != nil) != (tt.wantErr != nil) {
				t.Fatalf("Planner.Plan() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			ignores := []cmp.Option{
				cmpopts.IgnoreUnexported(planner.Step{}, graph.SubGraph{}),
				cmpopts.IgnoreFields(schema.Schema{}, "Tokens"),
				cmpopts.IgnoreFields(graph.SubGraph{}, "SDL"),
			}

			if diff := cmp.Diff(got, tt.want, ignores...); diff != "" {
				t.Errorf("Planner.Plan() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
