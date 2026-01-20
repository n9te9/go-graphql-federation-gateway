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
							width
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
				sg, err := graph.NewBaseSubGraph("aaaaaaaaa", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				superGraph, err := graph.NewSuperGraphFromBytes([]byte(sdl))
				if err != nil {
					t.Fatalf("failed to parse root schema: %v", err)
				}
				superGraph.SubGraphs = append(superGraph.SubGraphs, sg)

				subgraphSDL := `extend type Product @key(fields: "upc") {
					upc: String! @external
					width: Int
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
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`
							sg, err := graph.NewBaseSubGraph("aaaaaaaaa", []byte(sdl), "")
							if err != nil {
								t.Fatal(err)
							}
							sg.BaseName = "products"

							return sg
						}(),
						Selections: []*planner.Selection{
							{
								ParentType: "Product",
								Field:      "upc",
							},
							{
								ParentType: "Product",
								Field:      "name",
							},
						},
						IsBase:    true,
						DependsOn: []int{},
						Err:       nil,
						Done:      make(chan struct{}),
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `extend type Product @key(fields: "upc") {
								upc: String! @external
								width: Int
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
								Field:      "width",
							},
							{
								ParentType: "Product",
								Field:      "height",
							},
						},
						DependsOn: []int{0},
						Err:       nil,
						Done:      make(chan struct{}),
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Product",
						Field:      "products",
						SubSelections: []*planner.Selection{
							{
								ParentType: "Product",
								Field:      "upc",
							},
							{
								ParentType: "Product",
								Field:      "name",
							},
							{
								ParentType: "Product",
								Field:      "width",
							},
							{
								ParentType: "Product",
								Field:      "height",
							},
						},
					},
				},
			},
		},
		{
			name: "Complex case: Plan query with nested dependencies across 3 subgraphs",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`
					query {
						products {
							upc
							name
							reviews {
								body
								author {
									username
								}
							}
						}
					}
				`))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				productSDL := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String }`
				reviewSDL := `type Review { id: ID! body: String author: User product: Product }
							extend type User @key(fields: "id") { id: ID! @external reviews: [Review] }
							extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] }`
				userSDL := `type Query { me: User } type User @key(fields: "id") { id: ID! username: String }`

				sg1, _ := graph.NewBaseSubGraph("product", []byte(productSDL), "")
				sg2, _ := graph.NewSubGraph("review", []byte(reviewSDL), "")
				sg3, _ := graph.NewSubGraph("user", []byte(userSDL), "")

				superGraph, _ := graph.NewSuperGraphFromBytes([]byte(productSDL))
				superGraph.SubGraphs = append(superGraph.SubGraphs, sg1)
				superGraph.Merge(sg2)
				superGraph.Merge(sg3)

				return superGraph
			}(),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String }`
							sg, _ := graph.NewBaseSubGraph("product", []byte(sdl), "")
							sg.BaseName = "products"
							sg.OwnershipTypes = map[string]struct{}{"Product": {}}
							return sg
						}(),
						IsBase: true,
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
							{ParentType: "Product", Field: "name"},
						},
						DependsOn: []int{},
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Review { id: ID! body: String author: User product: Product }
									extend type User @key(fields: "id") { id: ID! @external reviews: [Review] }
									extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] }`
							sg, _ := graph.NewSubGraph("review", []byte(sdl), "")
							return sg
						}(),
						Selections: []*planner.Selection{
							{
								ParentType: "Product",
								Field:      "reviews",
								SubSelections: []*planner.Selection{
									{ParentType: "Review", Field: "body"},
									{
										ParentType: "Review",
										Field:      "author",
										SubSelections: []*planner.Selection{
											{ParentType: "User", Field: "id"},
										},
									},
								},
							},
						},
						DependsOn: []int{0},
					},
					{
						ID: 2,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query { me: User } type User @key(fields: "id") { id: ID! username: String }`
							sg, _ := graph.NewSubGraph("user", []byte(sdl), "")
							return sg
						}(),
						Selections: []*planner.Selection{
							{ParentType: "User", Field: "username"},
						},
						DependsOn: []int{1},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Product",
						Field:      "products",
						SubSelections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
							{ParentType: "Product", Field: "name"},
							{
								ParentType: "Product",
								Field:      "reviews",
								SubSelections: []*planner.Selection{
									{ParentType: "Review", Field: "body"},
									{
										ParentType: "Review",
										Field:      "author",
										SubSelections: []*planner.Selection{
											{ParentType: "User", Field: "username"},
										},
									},
								},
							},
						},
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
				cmpopts.IgnoreFields(planner.Step{}, "Done"),
			}

			if diff := cmp.Diff(got, tt.want, ignores...); diff != "" {
				t.Errorf("Planner.Plan() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
