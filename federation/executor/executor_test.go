package executor_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

type testRoundTripper func(req *http.Request) (*http.Response, error)

func (t testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t(req)
}

func TestExecutor_Execute(t *testing.T) {
	tests := []struct {
		name       string
		plan       *planner.Plan
		variables  map[string]any
		httpClient *http.Client
		superGraph *graph.SuperGraph
		want       map[string]any
		wantErr    error
	}{
		{
			name: "happy case: execute simple plan",
			superGraph: func() *graph.SuperGraph {
				sdl1 := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String price: Int }`
				sg1, _ := graph.NewSubGraph("product", []byte(sdl1), "http://product.example.com")

				sdl2 := `extend type Product @key(fields: "upc") { upc: String! @external width: Int height: Int price: Int @external }`
				sg2, _ := graph.NewSubGraph("inventory", []byte(sdl2), "http://inventory.example.com")

				sg, err := graph.NewSuperGraph([]*graph.SubGraph{sg1, sg2})
				if err != nil {
					t.Fatal(err)
				}
				return sg
			}(),
			httpClient: &http.Client{
				Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
					reqBodyBytes, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					var reqBodyMap map[string]any
					if err := json.Unmarshal(reqBodyBytes, &reqBodyMap); err != nil {
						return nil, err
					}

					switch req.Host {
					case "product.example.com":
						queryStr, _ := reqBodyMap["query"].(string)
						if !strings.Contains(queryStr, "products") {
							return nil, fmt.Errorf("unexpected query: %s", queryStr)
						}

						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(strings.NewReader(`{"data": {"products": [{"upc": "1", "name": "A"},{"upc": "2", "name": "B"}]}}`)),
						}, nil

					case "inventory.example.com":
						vars, _ := reqBodyMap["variables"].(map[string]any)
						reps, _ := vars["representations"].([]any)

						if len(reps) != 2 {
							return nil, fmt.Errorf("expected 2 representations, got %d", len(reps))
						}

						rep1 := reps[0].(map[string]any)
						if rep1["upc"] != "1" || rep1["__typename"] != "Product" {
							return nil, fmt.Errorf("unexpected representation 1: %v", rep1)
						}
						if _, hasName := rep1["name"]; hasName {
							return nil, fmt.Errorf("representation should not contain 'name'")
						}

						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(strings.NewReader(`{"data": {"_entities": [{"width": 10.0, "height": 20.0}, null]}}`)),
						}, nil
					}

					return nil, fmt.Errorf("not found")
				}),
			},
			plan: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						RootFields: []*planner.Selection{
							{
								ParentType: "Query",
								Field:      "products",
								SubSelections: []*planner.Selection{
									{ParentType: "Product", Field: "upc"},
									{ParentType: "Product", Field: "name"},
								},
							},
						},
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String price: Int }`
							sg, err := graph.NewSubGraph("product", []byte(sdl), "http://product.example.com")
							if err != nil {
								t.Fatal(err)
							}
							return sg
						}(),
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
							{ParentType: "Product", Field: "name"},
						},
						DependsOn: []int{},
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `extend type Product @key(fields: "upc") { upc: String! @external width: Int height: Int price: Int @external }`
							sg, err := graph.NewSubGraph("inventory", []byte(sdl), "http://inventory.example.com")
							if err != nil {
								t.Fatal(err)
							}
							return sg
						}(),
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "width"},
							{ParentType: "Product", Field: "height"},
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
			want: map[string]any{
				"data": map[string]any{
					"products": []any{
						map[string]any{
							"upc":    "1",
							"name":   "A",
							"width":  10.0,
							"height": 20.0,
						},
						map[string]any{
							"upc":  "2",
							"name": "B",
						},
					},
				},
				"errors": []any{},
			},
			wantErr: nil,
		},
		{
			name: "Complex case: Nested lists (Product -> Review -> User)",
			superGraph: func() *graph.SuperGraph {
				sdl1 := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String }`
				sg1, _ := graph.NewSubGraph("product", []byte(sdl1), "http://product.example.com")

				sdl2 := `type Review { id: ID! body: String author: User product: Product }
                                    extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] }`
				sg2, _ := graph.NewSubGraph("review", []byte(sdl2), "http://review.example.com")

				sdl3 := `type Query { me: User } type User @key(fields: "id") { id: ID! username: String }`
				sg3, _ := graph.NewSubGraph("user", []byte(sdl3), "http://user.example.com")

				sg, err := graph.NewSuperGraph([]*graph.SubGraph{sg1, sg2, sg3})
				if err != nil {
					t.Fatal(err)
				}
				return sg
			}(),
			httpClient: &http.Client{
				Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
					reqBodyBytes, _ := io.ReadAll(req.Body)
					reqBody := string(reqBodyBytes)

					switch req.Host {
					case "product.example.com":
						return &http.Response{
							StatusCode: 200,
							Body: io.NopCloser(strings.NewReader(`{
                                "data": { "products": [
                                    {"upc": "1"},
                                    {"upc": "2"}
                                ]}
                            }`)),
						}, nil

					case "review.example.com":
						if !strings.Contains(reqBody, `"upc":"1"`) {
							return nil, fmt.Errorf("missing upc 1 in review req")
						}

						return &http.Response{
							StatusCode: 200,
							Body: io.NopCloser(strings.NewReader(`{
                                "data": { "_entities": [
                                    { 
                                        "reviews": [
                                            {"body": "Love it", "author": {"id": "user-A"}},
                                            {"body": "Meh",     "author": {"id": "user-B"}}
                                        ]
                                    },
                                    { 
                                        "reviews": [] 
                                    }
                                ]}
                            }`)),
						}, nil
					case "user.example.com":
						if !strings.Contains(reqBody, `"id":"user-A"`) || !strings.Contains(reqBody, `"id":"user-B"`) {
							return nil, fmt.Errorf("missing user ids in user req: %s", reqBody)
						}

						return &http.Response{
							StatusCode: 200,
							Body: io.NopCloser(strings.NewReader(`{
                                "data": { "_entities": [
                                    {"username": "Alice"},
                                    {"username": "Bob"}
                                ]}
                            }`)),
						}, nil
					}
					return nil, fmt.Errorf("unknown host: %s", req.Host)
				}),
			},
			plan: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query { products: [Product] } type Product @key(fields: "upc") { upc: String! name: String }`
							sg, _ := graph.NewSubGraph("product", []byte(sdl), "http://product.example.com")
							sg.OwnershipTypes = map[string]struct{}{"Product": {}}
							return sg
						}(),
						// 【修正ポイント】RootFieldsにSubSelectionsを追加
						RootFields: []*planner.Selection{
							{
								ParentType: "Query",
								Field:      "products",
								SubSelections: []*planner.Selection{
									{ParentType: "Product", Field: "upc"},
								},
							},
						},
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
						},
						DependsOn: nil,
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Review { id: ID! body: String author: User product: Product }
                                    extend type Product @key(fields: "upc") { upc: String! @external reviews: [Review] }`
							sg, _ := graph.NewSubGraph("review", []byte(sdl), "http://review.example.com")
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
							sg, _ := graph.NewSubGraph("user", []byte(sdl), "http://user.example.com")
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
						ParentType: "Query",
						Field:      "products",
						SubSelections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
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
			want: map[string]any{
				"data": map[string]any{
					"products": []any{
						map[string]any{
							"upc": "1",
							"reviews": []any{
								map[string]any{
									"body": "Love it",
									"author": map[string]any{
										"username": "Alice",
									},
								},
								map[string]any{
									"body": "Meh",
									"author": map[string]any{
										"username": "Bob",
									},
								},
							},
						},
						map[string]any{
							"upc":     "2",
							"reviews": []any{},
						},
					},
				},
				"errors": []any{},
			},
			wantErr: nil,
		},
		{
			name: "Alias & Root Level Parallelism: product p1 and p2 with reviews",
			superGraph: func() *graph.SuperGraph {
				sdl1 := `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0",
        import: ["@key", "@shareable"])

type Query {
  product(id: ID!): Product
}

type Product @key(fields: "id") {
  id: ID!
  name: String! @shareable
  price: Int! @shareable
}
`
				sg1, _ := graph.NewSubGraph("product", []byte(sdl1), "http://product.example.com")

				sdl2 := `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0",
        import: ["@key", "@external"])

type Review {
  id: ID!
  body: String!
  authorName: String!
  product: Product
}

type Product @key(fields: "id") {
  id: ID! @external
  reviews: [Review]
}
`
				sg2, _ := graph.NewSubGraph("review", []byte(sdl2), "http://review.example.com")

				sg, err := graph.NewSuperGraph([]*graph.SubGraph{sg1, sg2})
				if err != nil {
					t.Fatal(err)
				}
				return sg
			}(),
			httpClient: &http.Client{
				Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
					reqBodyBytes, _ := io.ReadAll(req.Body)
					reqBody := string(reqBodyBytes)

					switch req.Host {
					case "product.example.com":
						return &http.Response{
							StatusCode: 200,
							Body: io.NopCloser(strings.NewReader(`{
                        "data": {
                            "p1": { "id": "p1", "name": "Product p1" },
                            "p2": { "id": "p2", "name": "Product p2" }
                        }
                    }`)),
						}, nil

					case "review.example.com":
						if !strings.Contains(reqBody, `"id":"p1"`) || !strings.Contains(reqBody, `"id":"p2"`) {
							return nil, fmt.Errorf("missing representations for p1 or p2: %s", reqBody)
						}

						return &http.Response{
							StatusCode: 200,
							Body: io.NopCloser(strings.NewReader(`{
                        "data": {
                            "_entities": [
                                { "reviews": [{ "body": "Great p1" }] },
                                { "reviews": [{ "body": "Great p2" }] }
                            ]
                        }
                    }`)),
						}, nil
					}
					return nil, fmt.Errorf("unknown host: %s", req.Host)
				}),
			},
			plan: func() *planner.Plan {
				sdl1 := `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0",
        import: ["@key", "@shareable"])

type Query {
  product(id: ID!): Product
}

type Product @key(fields: "id") {
  id: ID!
  name: String! @shareable
  price: Int! @shareable
}`
				sg1, _ := graph.NewSubGraph("product", []byte(sdl1), "http://product.example.com")

				sdl2 := `extend schema
  @link(url: "https://specs.apollo.dev/federation/v2.0",
        import: ["@key", "@external"])

type Review {
  id: ID!
  body: String!
  authorName: String!
  product: Product
}

type Product @key(fields: "id") {
  id: ID! @external
  reviews: [Review]
}
`
				sg2, _ := graph.NewSubGraph("review", []byte(sdl2), "http://review.example.com")

				sg, err := graph.NewSuperGraph([]*graph.SubGraph{sg1, sg2})
				if err != nil {
					t.Fatal(err)
				}
				planner := planner.NewPlanner(sg, planner.PlannerOption{})
				query := "query MultiProducts { p1: product(id: \"p1\") { name reviews { body } } p2: product(id: \"p2\") { name reviews { body } } }"

				doc := parser.New(lexer.New(query)).ParseDocument()
				p, err := planner.Plan(doc, map[string]any{})
				if err != nil {
					t.Fatal(err)
				}

				return p
			}(),
			want: map[string]any{
				"data": map[string]any{
					"p1": map[string]any{
						"name": "Product p1",
						"reviews": []any{
							map[string]any{"body": "Great p1"},
						},
					},
					"p2": map[string]any{
						"name": "Product p2",
						"reviews": []any{
							map[string]any{"body": "Great p2"},
						},
					},
				},
				"errors": []any{},
			},
			variables: map[string]any{},
			wantErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			e := executor.NewExecutor(tt.httpClient, tt.superGraph, executor.ExecutorOption{})
			got := e.Execute(t.Context(), tt.plan, tt.variables)

			if d := cmp.Diff(tt.want, got); d != "" {
				t.Fatalf("Executor.Execute() diff: %s", d)
			}
		})
	}
}
