package planner_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
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
				sg1, err := graph.NewBaseSubGraph("aaaaaaaaa", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				subgraphSDL := `extend type Product @key(fields: "upc") {
					upc: String! @external
					width: Int
					height: Int
					price: Int @external
				}`

				sg2, err := graph.NewSubGraph("hogehoge", []byte(subgraphSDL), "")
				if err != nil {
					t.Fatal(err)
				}

				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", sdl, subgraphSDL)), []*graph.SubGraph{sg1, sg2})

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
								ParentType:    "Product",
								Field:         "upc",
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "name",
								SubSelections: []*planner.Selection{},
							},
						},
						IsBase:    true,
						DependsOn: nil,
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
								ParentType:    "Product",
								Field:         "width",
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "height",
								SubSelections: []*planner.Selection{},
							},
						},
						DependsOn: []int{0},
						Err:       nil,
						Done:      make(chan struct{}),
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Query",
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
							extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] }`
				userSDL := `type Query { me: User } type User @key(fields: "id") { id: ID! username: String }`

				sg1, _ := graph.NewBaseSubGraph("product", []byte(productSDL), "")
				sg2, _ := graph.NewSubGraph("review", []byte(reviewSDL), "")
				sg3, _ := graph.NewSubGraph("user", []byte(userSDL), "")

				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n%s\n", productSDL, reviewSDL, userSDL)), []*graph.SubGraph{sg1, sg2, sg3})

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
							{
								ParentType:    "Product",
								Field:         "upc",
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "name",
								SubSelections: []*planner.Selection{},
							},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Review { id: ID! body: String author: User product: Product }
									extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] }`
							sg, _ := graph.NewSubGraph("review", []byte(sdl), "")
							return sg
						}(),
						Selections: []*planner.Selection{
							{
								ParentType: "Product",
								Field:      "reviews",
								SubSelections: []*planner.Selection{
									{
										ParentType:    "Review",
										Field:         "body",
										SubSelections: []*planner.Selection{},
									},
									{
										ParentType: "Review",
										Field:      "author",
										SubSelections: []*planner.Selection{
											{
												ParentType: "User",
												Field:      "id",
											},
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
							{
								ParentType:    "User",
								Field:         "username",
								SubSelections: []*planner.Selection{},
							},
						},
						DependsOn: []int{1},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Query",
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
		{
			name: "HappyCase: Plan query with @requires directive (injection of unselected field)",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`
            query {
                products {
                    name
                    shippingEstimate
                }
            }
        `))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				inventorySDL := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String weight: Int price: Int }`
				shippingSDL := `extend type Product @key(fields: "upc") { upc: String! @external weight: Int @external shippingEstimate: Int @requires(fields: "weight price") }`

				sg1, _ := graph.NewBaseSubGraph("inventory", []byte(inventorySDL), "")
				sg2, _ := graph.NewSubGraph("shipping", []byte(shippingSDL), "")

				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", inventorySDL, shippingSDL)), []*graph.SubGraph{sg1, sg2})

				return superGraph
			}(),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String weight: Int price: Int }`
							sg, _ := graph.NewBaseSubGraph("inventory", []byte(sdl), "")
							sg.BaseName = "products"
							sg.OwnershipTypes = map[string]struct{}{"Product": {}}
							return sg
						}(),
						IsBase: true,
						Selections: []*planner.Selection{
							{
								ParentType:    "Product",
								Field:         "upc",
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "name",
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "weight",
								SubSelections: []*planner.Selection{},
							},
							{
								ParentType:    "Product",
								Field:         "price",
								SubSelections: []*planner.Selection{},
							},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `extend type Product @key(fields: "upc") { upc: String! @external weight: Int @external shippingEstimate: Int @requires(fields: "weight price") }`
							sg, _ := graph.NewSubGraph("shipping", []byte(sdl), "")
							return sg
						}(),
						Selections: []*planner.Selection{
							{
								ParentType:    "Product",
								Field:         "shippingEstimate",
								SubSelections: []*planner.Selection{},
							},
						},
						DependsOn: []int{0},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Query",
						Field:      "products",
						SubSelections: []*planner.Selection{
							{ParentType: "Product", Field: "name"},
							{ParentType: "Product", Field: "shippingEstimate"},
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
				cmpopts.SortSlices(func(i, j int) bool { return i < j }),
				cmpopts.SortSlices(func(i, j *planner.Selection) bool { return i.Field < j.Field }),
				cmpopts.EquateEmpty(),
			}

			if diff := cmp.Diff(got, tt.want, ignores...); diff != "" {
				t.Errorf("Planner.Plan() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
