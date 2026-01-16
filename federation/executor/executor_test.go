package executor_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/n9te9/federation-gateway/federation/executor"
	"github.com/n9te9/federation-gateway/federation/graph"
	"github.com/n9te9/federation-gateway/federation/planner"
)

type testRoundTripper func(req *http.Request) (*http.Response, error)

func (t testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t(req)
}

func TestExecutor_Execute(t *testing.T) {
	tests := []struct {
		name       string
		plan       *planner.Plan
		httpClient *http.Client
		want       map[string]any
		wantErr    error
	}{
		{
			name: "happy case: execute simple plan",
			httpClient: &http.Client{
				Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
					switch req.Host {
					case "product.example.com":
						reqBody, err := io.ReadAll(req.Body)
						if err != nil {
							return nil, err
						}

						wantQuery := `{"query":"query {\n\t {\n\t\tupc\n\t\tname\n\t}\n}","variables":null}`

						if string(reqBody) != wantQuery {
							return nil, fmt.Errorf("want query: %s, got: %s", wantQuery, string(reqBody))
						}

						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(strings.NewReader(`{"data": {"products": [{"upc": "1", "name": "A"},{"upc": "2", "name": "B"}]}}`)),
						}, nil
					case "inventory.example.com":
						reqBody, err := io.ReadAll(req.Body)
						if err != nil {
							return nil, err
						}

						wantQuery := `{"query":"query ($representations: [_Any!]!) {\n\t_entities(representations: $representations) {\n\t\t... on Product {\n\t\t\tweight\n\t\t\theight\n\t\t}\n\t}\n}","variables":{"representations":[{"__typename":"Product","name":"A","upc":"1"},{"__typename":"Product","name":"B","upc":"2"}]}}`

						if string(reqBody) != wantQuery {
							return nil, fmt.Errorf("want query: %s, got: %s", wantQuery, string(reqBody))
						}

						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(strings.NewReader(`{"data": {"_entities": [{"weight": 10.0, "height": 20.0}, null]}}`)),
						}, nil
					}

					return nil, fmt.Errorf("not found")
				}),
			},
			plan: &planner.Plan{
				Steps: []*planner.Step{
					{
						ID: 0,
						SubGraph: func() *graph.SubGraph {
							sdl := `type Query {
					products: [Product]
				}
				
				type Product @key(fields: "upc") {
					upc: String!
					name: String
					price: Int
				}`
							sg, err := graph.NewBaseSubGraph("product", []byte(sdl), "http://product.example.com")
							if err != nil {
								t.Fatal(err)
							}

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
						DependsOn: []int{},
						Err:       nil,
						Done:      make(chan struct{}),
					},
					{
						ID: 1,
						SubGraph: func() *graph.SubGraph {
							sdl := `extend type Product @key(fields: "upc") {
								upc: String! @external
								weight: Int
								height: Int
								price: Int @external
							}`
							sg, err := graph.NewSubGraph("inventory", []byte(sdl), "http://inventory.example.com")
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
						DependsOn: []int{0},
						Err:       nil,
						Done:      make(chan struct{}),
					},
				},
			},
			want: map[string]any{
				"data": map[string]any{
					"products": []any{
						map[string]any{
							"upc":    "1",
							"name":   "A",
							"weight": 10.0,
							"height": 20.0,
						},
						map[string]any{
							"upc":  "2",
							"name": "B",
						},
					},
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := executor.NewExecutor(tt.httpClient)
			got, gotErr := e.Execute(t.Context(), tt.plan)

			if gotErr == nil && tt.wantErr != nil || tt.wantErr == nil && gotErr != nil {
				t.Fatalf("Executor.Execute() error = %v, wantErr %v", gotErr, tt.wantErr)
			}

			if gotErr != nil && tt.wantErr != nil {
				if gotErr.Error() != tt.wantErr.Error() {
					t.Fatalf("Executor.Execute() error = %v, wantErr %v", gotErr, tt.wantErr)
				}
			}

			if d := cmp.Diff(got, tt.want); d != "" {
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
			if d := cmp.Diff(got, tt.want); d != "" {
				t.Fatalf("BuildPath() diff: %s", d)
			}
		})
	}
}
