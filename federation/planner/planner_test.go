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
		variables  map[string]any
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
				sdl := `type Query { products: [Product] } type Product { upc: String! name: String price: Int }`
				sg1, _ := graph.NewSubGraph("aaaaaaaaa", []byte(sdl), "")
				subgraphSDL := `extend type Product @key(fields: "upc") { upc: String! @external width: Int height: Int price: Int @external }`
				sg2, _ := graph.NewSubGraph("hogehoge", []byte(subgraphSDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", sdl, subgraphSDL)), []*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query { products: [Product] } type Product { upc: String! name: String price: Int }`
							sg, _ := graph.NewSubGraph("aaaaaaaaa", []byte(sdl), "")
							return sg
						}(),
						RootFields:    []string{"products"},
						OperationType: "query",
						RootArguments: map[string]map[string]any{"products": {}},
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc", SubSelections: []*planner.Selection{}},
							{ParentType: "Product", Field: "name", SubSelections: []*planner.Selection{}},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `extend type Product @key(fields: "upc") { upc: String! @external width: Int height: Int price: Int @external }`
							sg, _ := graph.NewSubGraph("hogehoge", []byte(sdl), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "width", SubSelections: []*planner.Selection{}},
							{ParentType: "Product", Field: "height", SubSelections: []*planner.Selection{}},
						},
						DependsOn: []int{0},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Query",
						Field:      "products",
						SubSelections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
							{ParentType: "Product", Field: "name"},
							{ParentType: "Product", Field: "width"},
							{ParentType: "Product", Field: "height"},
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
                            extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] }
                            extend type User @key(fields: "id") { id: ID! @external }`
				userSDL := `type Query { me: User } type User @key(fields: "id") { id: ID! username: String }`
				sg1, _ := graph.NewSubGraph("product", []byte(productSDL), "")
				sg2, _ := graph.NewSubGraph("review", []byte(reviewSDL), "")
				sg3, _ := graph.NewSubGraph("user", []byte(userSDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n%s\n", productSDL, reviewSDL, userSDL)), []*graph.SubGraph{sg1, sg2, sg3})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("product", []byte(`type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String }`), "")
							return sg
						}(),
						RootFields:    []string{"products"},
						OperationType: "query",
						RootArguments: map[string]map[string]any{"products": {}},
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc", SubSelections: []*planner.Selection{}},
							{ParentType: "Product", Field: "name", SubSelections: []*planner.Selection{}},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("review", []byte(`type Review { id: ID! body: String author: User product: Product } extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] } extend type User @key(fields: "id") { id: ID! @external }`), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{
								ParentType: "Product",
								Field:      "reviews",
								SubSelections: []*planner.Selection{
									{ParentType: "Review", Field: "body", SubSelections: []*planner.Selection{}},
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
							sg, _ := graph.NewSubGraph("user", []byte(`type Query { me: User } type User @key(fields: "id") { id: ID! username: String }`), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{ParentType: "User", Field: "username", SubSelections: []*planner.Selection{}},
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
				doc, err := parser.Parse([]byte(`query { products { name shippingEstimate } }`))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				inventorySDL := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String weight: Int price: Int }`
				shippingSDL := `extend type Product @key(fields: "upc") { upc: String! @external weight: Int @external shippingEstimate: Int @requires(fields: "weight price") }`
				sg1, _ := graph.NewSubGraph("inventory", []byte(inventorySDL), "")
				sg2, _ := graph.NewSubGraph("shipping", []byte(shippingSDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", inventorySDL, shippingSDL)), []*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("inventory", []byte(`type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String weight: Int price: Int }`), "")
							return sg
						}(),
						RootFields:    []string{"products"},
						OperationType: "query",
						RootArguments: map[string]map[string]any{"products": {}},
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc", SubSelections: []*planner.Selection{}},
							{ParentType: "Product", Field: "name", SubSelections: []*planner.Selection{}},
							{ParentType: "Product", Field: "weight", SubSelections: []*planner.Selection{}},
							{ParentType: "Product", Field: "price", SubSelections: []*planner.Selection{}},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("shipping", []byte(`extend type Product @key(fields: "upc") { upc: String! @external weight: Int @external shippingEstimate: Int @requires(fields: "weight price") }`), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "shippingEstimate", SubSelections: []*planner.Selection{}},
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
		{
			name: "Happy case: Plan mutation query",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`mutation { addReview(body: "cool", upc: "1") { id body } }`))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				reviewSDL := `type Mutation { addReview(body: String, upc: String): Review } type Review { id: ID! body: String }`
				sg1, _ := graph.NewSubGraph("review", []byte(reviewSDL), "")
				inventorySDL := `type Query { topProducts: [Product] } type Product @key(fields: "upc") { upc: String! name: String }`
				sg2, _ := graph.NewSubGraph("inventory", []byte(inventorySDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", reviewSDL, inventorySDL)), []*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("review", []byte(`type Mutation { addReview(body: String, upc: String): Review } type Review { id: ID! body: String }`), "")
							return sg
						}(),
						RootFields:    []string{"addReview"},
						OperationType: "mutation",
						RootArguments: map[string]map[string]any{
							"addReview": {
								"body": []byte(`"cool"`),
								"upc":  []byte(`"1"`),
							},
						},
						Selections: []*planner.Selection{
							{ParentType: "Review", Field: "body"},
							{ParentType: "Review", Field: "id"},
						},
						DependsOn: nil,
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Mutation",
						Field:      "addReview",
						Arguments: map[string]any{
							"body": []byte(`"cool"`),
							"upc":  []byte(`"1"`),
						},
						SubSelections: []*planner.Selection{
							{ParentType: "Review", Field: "id"},
							{ParentType: "Review", Field: "body"},
						},
					},
				},
			},
		},
		{
			name: "Regression: Federation v2 Type Merging (No extend keyword) with @external",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`query { products { shippingEstimate } }`))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				inventorySDL := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! weight: Int }`
				shippingSDL := `type Product @key(fields: "upc") { upc: String! @external weight: Int @external shippingEstimate: Int @requires(fields: "weight") }`
				sg1, _ := graph.NewSubGraph("inventory", []byte(inventorySDL), "")
				sg2, _ := graph.NewSubGraph("shipping", []byte(shippingSDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", inventorySDL, shippingSDL)), []*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("inventory", []byte(`type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! weight: Int }`), "")
							return sg
						}(),
						RootFields:    []string{"products"},
						OperationType: "query",
						RootArguments: map[string]map[string]any{"products": {}},
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
							{ParentType: "Product", Field: "weight"},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("shipping", []byte(`type Product @key(fields: "upc") { upc: String! @external weight: Int @external shippingEstimate: Int @requires(fields: "weight") }`), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "shippingEstimate"},
						},
						DependsOn: []int{0},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Query",
						Field:      "products",
						SubSelections: []*planner.Selection{
							{ParentType: "Product", Field: "shippingEstimate"},
						},
					},
				},
			},
		},
		{
			name: "Complex case: Mutation requiring entity resolution (Review -> User)",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`mutation { addReview(body: "Excellent!") { body author { username } } }`))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				reviewSDL := `type Mutation { addReview(body: String): Review } type Review { id: ID! body: String author: User } extend type User @key(fields: "id") { id: ID! @external }`
				userSDL := `type User @key(fields: "id") { id: ID! username: String }`
				sg1, _ := graph.NewSubGraph("review", []byte(reviewSDL), "")
				sg2, _ := graph.NewSubGraph("user", []byte(userSDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", reviewSDL, userSDL)), []*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("review", []byte(`type Mutation { addReview(body: String): Review } type Review { id: ID! body: String author: User } extend type User @key(fields: "id") { id: ID! @external }`), "")
							return sg
						}(),
						RootFields:    []string{"addReview"},
						OperationType: "mutation",
						RootArguments: map[string]map[string]any{
							"addReview": {
								"body": []uint8(`"Excellent!"`),
							},
						},
						Selections: []*planner.Selection{
							{ParentType: "Review", Field: "body"},
							{
								ParentType: "Review",
								Field:      "author",
								SubSelections: []*planner.Selection{
									{ParentType: "User", Field: "id"},
								},
							},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("user", []byte(`type User @key(fields: "id") { id: ID! username: String }`), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{ParentType: "User", Field: "username"},
						},
						DependsOn: []int{0},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Mutation",
						Field:      "addReview",
						Arguments: map[string]any{
							"body": []uint8(`"Excellent!"`),
						},
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
		{
			name: "Complex case: Mutation with deep nested resolution (PostComment -> Product)",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`mutation { postComment(text: "Where is this?") { text product { name } } }`))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				socialSDL := `type Mutation { postComment(text: String): Comment } type Comment { id: ID! text: String product: Product } extend type Product @key(fields: "upc") { upc: String! @external }`
				inventorySDL := `type Product @key(fields: "upc") { upc: String! name: String }`
				sg1, _ := graph.NewSubGraph("social", []byte(socialSDL), "")
				sg2, _ := graph.NewSubGraph("inventory", []byte(inventorySDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", socialSDL, inventorySDL)), []*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("social", []byte(`type Mutation { postComment(text: String): Comment } type Comment { id: ID! text: String product: Product } extend type Product @key(fields: "upc") { upc: String! @external }`), "")
							return sg
						}(),
						RootFields:    []string{"postComment"},
						OperationType: "mutation",
						RootArguments: map[string]map[string]any{
							"postComment": {
								"text": []uint8(`"Where is this?"`),
							},
						},
						Selections: []*planner.Selection{
							{ParentType: "Comment", Field: "text"},
							{
								ParentType: "Comment",
								Field:      "product",
								SubSelections: []*planner.Selection{
									{ParentType: "Product", Field: "upc"},
								},
							},
						},
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("inventory", []byte(`type Product @key(fields: "upc") { upc: String! name: String }`), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "name"},
						},
						DependsOn: []int{0},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Mutation",
						Field:      "postComment",
						Arguments: map[string]any{
							"text": []uint8(`"Where is this?"`),
						},
						SubSelections: []*planner.Selection{
							{ParentType: "Comment", Field: "text"},
							{
								ParentType: "Comment",
								Field:      "product",
								SubSelections: []*planner.Selection{
									{ParentType: "Product", Field: "name"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Complex case: Multiple mutations with cross-subgraph dependency",
			doc: func() *query.Document {
				lexer := query.NewLexer()
				parser := query.NewParser(lexer)
				doc, err := parser.Parse([]byte(`mutation { createProduct(name: "New Item") { upc } addReview(upc: "item-1", body: "Good") { id product { name } } }`))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}(),
			superGraph: func() *graph.SuperGraph {
				inventorySDL := `type Mutation { createProduct(name: String): Product } type Product @key(fields: "upc") { upc: String! name: String }`
				reviewSDL := `type Mutation { addReview(upc: String, body: String): Review } type Review { id: ID! body: String product: Product } extend type Product @key(fields: "upc") { upc: String! @external }`
				sg1, _ := graph.NewSubGraph("inventory", []byte(inventorySDL), "")
				sg2, _ := graph.NewSubGraph("review", []byte(reviewSDL), "")
				superGraph, _ := graph.NewSuperGraph([]byte(fmt.Sprintf("%s\n%s\n", inventorySDL, reviewSDL)), []*graph.SubGraph{sg1, sg2})
				return superGraph
			}(),
			variables: make(map[string]any),
			want: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("inventory", []byte(`type Mutation { createProduct(name: String): Product } type Product @key(fields: "upc") { upc: String! name: String }`), "")
							return sg
						}(),
						RootFields:    []string{"createProduct"},
						OperationType: "mutation",
						RootArguments: map[string]map[string]any{
							"createProduct": {
								"name": []uint8(`"New Item"`),
							},
						},
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
						},
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("review", []byte(`type Mutation { addReview(upc: String, body: String): Review } type Review { id: ID! body: String product: Product } extend type Product @key(fields: "upc") { upc: String! @external }`), "")
							return sg
						}(),
						RootFields:    []string{"addReview"},
						OperationType: "mutation",
						RootArguments: map[string]map[string]any{
							"addReview": {
								"body": []uint8(`"Good"`),
								"upc":  []uint8(`"item-1"`),
							},
						},
						Selections: []*planner.Selection{
							{ParentType: "Review", Field: "id"},
							{
								ParentType: "Review",
								Field:      "product",
								SubSelections: []*planner.Selection{
									{ParentType: "Product", Field: "upc"},
								},
							},
						},
					},
					{
						ID: 2,
						SubGraph: func() *graph.SubGraph {
							sg, _ := graph.NewSubGraph("inventory", []byte(`type Mutation { createProduct(name: String): Product } type Product @key(fields: "upc") { upc: String! name: String }`), "")
							return sg
						}(),
						RootFields:    nil,
						OperationType: "",
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "name"},
						},
						DependsOn: []int{1},
					},
				},
				RootSelections: []*planner.Selection{
					{
						ParentType: "Mutation",
						Field:      "createProduct",
						SubSelections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
						},
						Arguments: map[string]any{"name": []uint8(`"New Item"`)},
					},
					{
						ParentType: "Mutation",
						Field:      "addReview",
						Arguments:  map[string]any{"body": []uint8(`"Good"`), "upc": []uint8(`"item-1"`)},
						SubSelections: []*planner.Selection{
							{ParentType: "Review", Field: "id"},
							{
								ParentType: "Review",
								Field:      "product",
								SubSelections: []*planner.Selection{
									{ParentType: "Product", Field: "name"},
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
			got, err := p.Plan(tt.doc, tt.variables)
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
				cmpopts.SortSlices(func(i, j int) bool { return i < j }),
				cmpopts.SortSlices(func(i, j *planner.Selection) bool { return i.Field < j.Field }),
				cmpopts.EquateEmpty(),
			}

			if diff := cmp.Diff(got, tt.want, ignores...); diff != "" {
				t.Errorf("Planner.Plan() mismatch (+want -got):\n%s", diff)
			}
		})
	}
}
