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
		sdl        string
		want       map[string]any
		wantErr    error
	}{
		{
			name: "happy case: execute simple plan",
			sdl: `type Query {
					products: [Product]
				}

				type Product @key(fields: "upc") {
					upc: String!
					name: String
					price: Int
                    width: Int
                    height: Int
				}`,
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
						ID:         0,
						RootFields: []string{"products"},
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
						Done:      make(chan struct{}),
						Err:       nil,
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
						Err:       nil,
						Done:      make(chan struct{}),
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
							"upc":  "1",
							"name": "A",
						},
						map[string]any{
							"upc":  "2",
							"name": "B",
						},
					},
				},
				"errors": []string{`Post "http://inventory.example.com": representation should not contain 'name'`},
			},
			wantErr: nil,
		},
		{
			name: "Complex case: Nested lists (Product -> Review -> User)",
			sdl: `type Query {
					products: [Product]
				}
					
				type Product @key(fields: "upc") {
					upc: String!
					name: String
					reviews: [Review]
				}
					
				type Review {
					id: ID!
					body: String
					author: User
					product: Product
				}
					
				type User @key(fields: "id") {
					id: ID!
					username: String
				}`,
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
						Selections: []*planner.Selection{
							{ParentType: "Product", Field: "upc"},
						},
						DependsOn: nil,
						Done:      make(chan struct{}),
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
						Done:      make(chan struct{}),
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
						Done:      make(chan struct{}),
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			superGraph, err := graph.NewSuperGraphFromBytes([]byte(tt.sdl))
			if err != nil {
				t.Fatalf("Failed to create SuperGraph: %v", err)
			}

			e := executor.NewExecutor(tt.httpClient, superGraph)
			got := e.Execute(t.Context(), tt.plan, tt.variables)

			if d := cmp.Diff(tt.want, got); d != "" {
				t.Fatalf("Executor.Execute() diff: %s", d)
			}
		})
	}
}

func TestBuildPath(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []executor.Path
	}{
		{
			name: "build path for simple field",
			v: map[string]any{
				"products": []any{
					map[string]any{
						"upc":  "1",
						"name": "A",
					},
				},
			},
			want: []executor.Path{
				{
					{FieldName: "products", Index: &[]int{0}[0]},
					{FieldName: "upc", Index: nil},
				},
				{
					{FieldName: "products", Index: &[]int{0}[0]},
					{FieldName: "name", Index: nil},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := executor.BuildPaths(tt.v)
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) %d != len(want) %d", len(got), len(tt.want))
			}
		})
	}
}
