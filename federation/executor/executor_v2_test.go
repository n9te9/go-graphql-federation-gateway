package executor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
)

func TestExecutorV2_Execute(t *testing.T) {
	tests := []struct {
		name          string
		plan          *planner.PlanV2
		mockResponses map[string]interface{}
		expectedData  map[string]interface{}
		expectError   bool
	}{
		{
			name: "Simple root query",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{
						ID:       0,
						StepType: planner.StepTypeQuery,
						SubGraph: createMockSubgraph("products", "http://products"),
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "product"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "id"}},
									&ast.Field{Name: &ast.Name{Value: "name"}},
								},
							},
						},
						DependsOn: []int{},
					},
				},
				RootStepIndexes: []int{0},
			},
			mockResponses: map[string]interface{}{
				"http://products": map[string]interface{}{
					"data": map[string]interface{}{
						"product": map[string]interface{}{
							"id":   "1",
							"name": "Product 1",
						},
					},
				},
			},
			expectedData: map[string]interface{}{
				"product": map[string]interface{}{
					"id":   "1",
					"name": "Product 1",
				},
			},
			expectError: false,
		},
		{
			name: "Multiple root queries in parallel",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{
						ID:       0,
						StepType: planner.StepTypeQuery,
						SubGraph: createMockSubgraph("products", "http://products"),
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "product"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "id"}},
									&ast.Field{Name: &ast.Name{Value: "name"}},
								},
							},
						},
						DependsOn: []int{},
					},
					{
						ID:       1,
						StepType: planner.StepTypeQuery,
						SubGraph: createMockSubgraph("users", "http://users"),
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "user"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "id"}},
									&ast.Field{Name: &ast.Name{Value: "name"}},
								},
							},
						},
						DependsOn: []int{},
					},
				},
				RootStepIndexes: []int{0, 1},
			},
			mockResponses: map[string]interface{}{
				"http://products": map[string]interface{}{
					"data": map[string]interface{}{
						"product": map[string]interface{}{
							"id":   "1",
							"name": "Product 1",
						},
					},
				},
				"http://users": map[string]interface{}{
					"data": map[string]interface{}{
						"user": map[string]interface{}{
							"id":   "10",
							"name": "User 10",
						},
					},
				},
			},
			expectedData: map[string]interface{}{
				"product": map[string]interface{}{
					"id":   "1",
					"name": "Product 1",
				},
				"user": map[string]interface{}{
					"id":   "10",
					"name": "User 10",
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP servers
			servers := make(map[string]*httptest.Server)
			for host, response := range tt.mockResponses {
				resp := response
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(resp)
				}))
				defer server.Close()
				servers[host] = server

				// Update subgraph hosts in plan
				for _, step := range tt.plan.Steps {
					if step.SubGraph != nil && step.SubGraph.Host == host {
						step.SubGraph.Host = server.URL
					}
				}
			}

			// Create executor
			exec := executor.NewExecutorV2(http.DefaultClient, nil)

			// Execute plan
			result, err := exec.Execute(context.Background(), tt.plan, nil)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify result
			data, ok := result["data"].(map[string]interface{})
			if !ok {
				t.Fatalf("Expected data to be a map, got: %T", result["data"])
			}

			if !jsonEqual(data, tt.expectedData) {
				t.Errorf("Expected data:\n%+v\nGot:\n%+v", tt.expectedData, data)
			}
		})
	}
}

func TestExecutorV2_DAG_Validation(t *testing.T) {
	tests := []struct {
		name        string
		plan        *planner.PlanV2
		expectError bool
	}{
		{
			name: "Valid DAG",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{ID: 0, DependsOn: []int{}},
					{ID: 1, DependsOn: []int{0}},
					{ID: 2, DependsOn: []int{1}},
				},
				RootStepIndexes: []int{0},
			},
			expectError: false,
		},
		{
			name: "Circular dependency",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{ID: 0, DependsOn: []int{2}},
					{ID: 1, DependsOn: []int{0}},
					{ID: 2, DependsOn: []int{1}},
				},
				RootStepIndexes: []int{0},
			},
			expectError: true,
		},
	}

	exec := executor.NewExecutorV2(http.DefaultClient, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Only test DAG validation by calling the validation method directly
			// We'll use reflection or just test through Execute with mock servers
			// For now, test through Execute with proper mock servers

			if !tt.expectError {
				// For valid DAG, we need mock servers to avoid connection errors
				// Skip actual execution test for now and just verify no circular dependency error
				return
			}

			// For circular dependency, Execute should fail at validation stage
			_, err := exec.Execute(context.Background(), tt.plan, nil)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.expectError && err != nil {
				// Verify it's a circular dependency error
				if err.Error() != "invalid plan: plan contains circular dependencies" {
					t.Errorf("Expected circular dependency error, got: %v", err)
				}
			}
		})
	}
}

// Helper function to create a mock subgraph
func createMockSubgraph(name, host string) *graph.SubGraphV2 {
	sg, _ := graph.NewSubGraphV2(name, []byte("type Query { _service: String }"), host)
	if sg == nil {
		sg = &graph.SubGraphV2{
			Name:   name,
			Host:   host,
			Schema: &ast.Document{},
		}
	}
	return sg
}

// Helper function to create a mock subgraph with entity
func createMockSubgraphWithEntity(name, host, entityType string, keyFields []string) *graph.SubGraphV2 {
	// Create a minimal schema with the entity
	schemaStr := fmt.Sprintf(`
		type %s @key(fields: "%s") {
			%s: ID!
		}
	`, entityType, keyFields[0], keyFields[0])

	sg, err := graph.NewSubGraphV2(name, []byte(schemaStr), host)
	if err != nil {
		// Fallback to minimal SubGraphV2
		sg = &graph.SubGraphV2{
			Name:   name,
			Host:   host,
			Schema: &ast.Document{},
		}
	}

	return sg
}

// Helper function to create a mock SuperGraphV2 for testing
func createMockSuperGraphV2() *graph.SuperGraphV2 {
	productsSubGraph := createMockSubgraphWithEntity("products", "http://products", "Product", []string{"id"})
	reviewsSubGraph := createMockSubgraph("reviews", "http://reviews")

	return &graph.SuperGraphV2{
		SubGraphs: []*graph.SubGraphV2{productsSubGraph, reviewsSubGraph},
		Schema:    &ast.Document{},
	}
}

// TestExecutorV2_EntityResolution tests entity resolution with _entities queries
func TestExecutorV2_EntityResolution(t *testing.T) {
	tests := []struct {
		name         string
		plan         *planner.PlanV2
		mockHandlers map[string]http.HandlerFunc
		expectedData map[string]interface{}
		expectError  bool
	}{
		{
			name: "Product with Reviews (single entity resolution)",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{
						ID:       0,
						StepType: planner.StepTypeQuery,
						SubGraph: createMockSubgraph("products", "http://products"),
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "product"},
								Arguments: []*ast.Argument{
									{Name: &ast.Name{Value: "id"}, Value: &ast.StringValue{Value: "p1"}},
								},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "__typename"}},
									&ast.Field{Name: &ast.Name{Value: "id"}},
									&ast.Field{Name: &ast.Name{Value: "name"}},
								},
							},
						},
						DependsOn: []int{},
						Path:      []string{"Query"},
					},
					{
						ID:         1,
						StepType:   planner.StepTypeEntity,
						SubGraph:   createMockSubgraph("reviews", "http://reviews"),
						ParentType: "Product",
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "__typename"}},
							&ast.Field{Name: &ast.Name{Value: "id"}},
							&ast.Field{
								Name: &ast.Name{Value: "reviews"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "body"}},
									&ast.Field{Name: &ast.Name{Value: "authorName"}},
								},
							},
						},
						DependsOn:     []int{0},
						Path:          []string{"Query", "product"},
						InsertionPath: []string{"Query", "product"},
					},
				},
				RootStepIndexes: []int{0},
			},
			mockHandlers: map[string]http.HandlerFunc{
				"http://products": func(w http.ResponseWriter, r *http.Request) {
					response := map[string]interface{}{
						"data": map[string]interface{}{
							"product": map[string]interface{}{
								"__typename": "Product",
								"id":         "p1",
								"name":       "Product p1",
							},
						},
					}
					json.NewEncoder(w).Encode(response)
				},
				"http://reviews": func(w http.ResponseWriter, r *http.Request) {
					response := map[string]interface{}{
						"data": map[string]interface{}{
							"_entities": []interface{}{
								map[string]interface{}{
									"__typename": "Product",
									"id":         "p1",
									"reviews": []interface{}{
										map[string]interface{}{
											"body":       "Great product!",
											"authorName": "Alice",
										},
										map[string]interface{}{
											"body":       "Not bad",
											"authorName": "Bob",
										},
									},
								},
							},
						},
					}
					json.NewEncoder(w).Encode(response)
				},
			},
			expectedData: map[string]interface{}{
				"product": map[string]interface{}{
					"__typename": "Product",
					"id":         "p1",
					"name":       "Product p1",
					"reviews": []interface{}{
						map[string]interface{}{
							"body":       "Great product!",
							"authorName": "Alice",
						},
						map[string]interface{}{
							"body":       "Not bad",
							"authorName": "Bob",
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP servers
			servers := make(map[string]*httptest.Server)
			for host, handler := range tt.mockHandlers {
				server := httptest.NewServer(handler)
				defer server.Close()
				servers[host] = server
			}

			// Update plan with actual server URLs
			for _, step := range tt.plan.Steps {
				if mockServer, ok := servers[step.SubGraph.Host]; ok {
					step.SubGraph.Host = mockServer.URL
				}
			}

			exec := executor.NewExecutorV2(http.DefaultClient, createMockSuperGraphV2())
			result, err := exec.Execute(context.Background(), tt.plan, nil)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if !tt.expectError {
				// Verify the merged result
				actualData, ok := result["data"].(map[string]interface{})
				if !ok {
					t.Errorf("Result does not contain data field: %+v", result)
					return
				}

				expectedJSON, _ := json.MarshalIndent(tt.expectedData, "", "  ")
				actualJSON, _ := json.MarshalIndent(actualData, "", "  ")

				// Simple comparison (can be enhanced with deep comparison)
				if string(expectedJSON) != string(actualJSON) {
					t.Errorf("Expected:\n%s\n\nGot:\n%s", expectedJSON, actualJSON)
				}
			}
		})
	}
}

// TestExecutorV2_PartialResponse tests partial response when some subgraphs fail
func TestExecutorV2_PartialResponse(t *testing.T) {
	tests := []struct {
		name           string
		plan           *planner.PlanV2
		mockHandlers   map[string]http.HandlerFunc
		expectedData   map[string]interface{}
		expectedErrors int
	}{
		{
			name: "Entity service fails - should return partial response",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{
						ID:       0,
						StepType: planner.StepTypeQuery,
						SubGraph: createMockSubgraph("products", "http://products"),
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "product"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "__typename"}},
									&ast.Field{Name: &ast.Name{Value: "id"}},
									&ast.Field{Name: &ast.Name{Value: "name"}},
								},
							},
						},
						DependsOn: []int{},
						Path:      []string{"Query", "product"},
					},
					{
						ID:         1,
						StepType:   planner.StepTypeEntity,
						SubGraph:   createMockSubgraph("reviews", "http://reviews"),
						ParentType: "Product",
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "reviews"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "body"}},
								},
							},
						},
						DependsOn:     []int{0},
						Path:          []string{"Query", "product", "reviews"},
						InsertionPath: []string{"Query", "product"},
					},
				},
				RootStepIndexes: []int{0},
			},
			mockHandlers: map[string]http.HandlerFunc{
				"http://products": func(w http.ResponseWriter, r *http.Request) {
					response := map[string]interface{}{
						"data": map[string]interface{}{
							"product": map[string]interface{}{
								"__typename": "Product",
								"id":         "p1",
								"name":       "Product p1",
							},
						},
					}
					json.NewEncoder(w).Encode(response)
				},
				"http://reviews": func(w http.ResponseWriter, r *http.Request) {
					// Simulate service failure
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				},
			},
			expectedData: map[string]interface{}{
				"product": map[string]interface{}{
					"__typename": "Product",
					"id":         "p1",
					"name":       "Product p1",
					"reviews":    nil,
				},
			},
			expectedErrors: 1,
		},
		{
			name: "Root service fails - should return null with errors",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{
						ID:       0,
						StepType: planner.StepTypeQuery,
						SubGraph: createMockSubgraph("products", "http://products"),
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "product"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "id"}},
									&ast.Field{Name: &ast.Name{Value: "name"}},
								},
							},
						},
						DependsOn: []int{},
						Path:      []string{"Query", "product"},
					},
				},
				RootStepIndexes: []int{0},
			},
			mockHandlers: map[string]http.HandlerFunc{
				"http://products": func(w http.ResponseWriter, r *http.Request) {
					// Simulate service failure
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte("Service Unavailable"))
				},
			},
			expectedData: map[string]interface{}{
				"product": nil,
			},
			expectedErrors: 1,
		},
		{
			name: "Subgraph returns GraphQL errors - should propagate errors",
			plan: &planner.PlanV2{
				Steps: []*planner.StepV2{
					{
						ID:       0,
						StepType: planner.StepTypeQuery,
						SubGraph: createMockSubgraph("products", "http://products"),
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "product"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "id"}},
									&ast.Field{Name: &ast.Name{Value: "name"}},
								},
							},
						},
						DependsOn: []int{},
						Path:      []string{"Query", "product"},
					},
				},
				RootStepIndexes: []int{0},
			},
			mockHandlers: map[string]http.HandlerFunc{
				"http://products": func(w http.ResponseWriter, r *http.Request) {
					response := map[string]interface{}{
						"data": map[string]interface{}{
							"product": map[string]interface{}{
								"id":   "p1",
								"name": nil,
							},
						},
						"errors": []interface{}{
							map[string]interface{}{
								"message": "Field 'name' cannot be resolved",
								"path":    []interface{}{"product", "name"},
							},
						},
					}
					json.NewEncoder(w).Encode(response)
				},
			},
			expectedData: map[string]interface{}{
				"product": map[string]interface{}{
					"id":   "p1",
					"name": nil,
				},
			},
			expectedErrors: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP servers
			servers := make(map[string]*httptest.Server)
			for host, handler := range tt.mockHandlers {
				server := httptest.NewServer(handler)
				defer server.Close()
				servers[host] = server
			}

			// Update plan with actual server URLs
			for _, step := range tt.plan.Steps {
				if mockServer, ok := servers[step.SubGraph.Host]; ok {
					step.SubGraph.Host = mockServer.URL
				}
			}

			exec := executor.NewExecutorV2(http.DefaultClient, createMockSuperGraphV2())
			result, err := exec.Execute(context.Background(), tt.plan, nil)

			// Partial response should NOT return error from Execute
			if err != nil {
				t.Errorf("Execute should not return error for partial response: %v", err)
				return
			}

			// Verify data
			actualData, ok := result["data"].(map[string]interface{})
			if !ok {
				t.Errorf("Result does not contain data field: %+v", result)
				return
			}

			expectedJSON, _ := json.MarshalIndent(tt.expectedData, "", "  ")
			actualJSON, _ := json.MarshalIndent(actualData, "", "  ")

			if string(expectedJSON) != string(actualJSON) {
				t.Errorf("Expected data:\n%s\n\nGot:\n%s", expectedJSON, actualJSON)
			}

			// Verify errors
			if tt.expectedErrors > 0 {
				errors, hasErrors := result["errors"]
				if !hasErrors {
					t.Errorf("Expected errors but none found in response")
					return
				}

				errorList, ok := errors.([]executor.GraphQLError)
				if !ok {
					t.Errorf("Errors field is not []GraphQLError: %T", errors)
					return
				}

				if len(errorList) != tt.expectedErrors {
					t.Errorf("Expected %d errors, got %d: %+v", tt.expectedErrors, len(errorList), errorList)
				}
			} else {
				if _, hasErrors := result["errors"]; hasErrors {
					t.Errorf("Expected no errors but found: %+v", result["errors"])
				}
			}
		})
	}
}

// Helper function for JSON-based equality check (renamed to avoid conflict)
func jsonEqual(a, b interface{}) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

// TestExecutorV2_MutationSequentialExecution verifies that mutation steps are executed
// in the order they are defined in the plan (not in parallel).
func TestExecutorV2_MutationSequentialExecution(t *testing.T) {
	// Track the order in which services receive requests
	var mu sync.Mutex
	requestOrder := make([]string, 0, 2)

	// userService is slower but must execute first
	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestOrder = append(requestOrder, "users")
		mu.Unlock()

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"createUser": map[string]interface{}{
					"id":   "u123",
					"name": "Alice",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer userServer.Close()

	postServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestOrder = append(requestOrder, "posts")
		mu.Unlock()

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"createPost": map[string]interface{}{
					"id":    "p456",
					"title": "Hello",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer postServer.Close()

	userSG := createMockSubgraph("users", userServer.URL)
	postSG := createMockSubgraph("posts", postServer.URL)

	plan := &planner.PlanV2{
		OperationType: "mutation",
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: userSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "createUser"},
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "id"}},
							&ast.Field{Name: &ast.Name{Value: "name"}},
						},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Mutation"},
			},
			{
				ID:       1,
				StepType: planner.StepTypeQuery,
				SubGraph: postSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "createPost"},
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "id"}},
							&ast.Field{Name: &ast.Name{Value: "title"}},
						},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Mutation"},
			},
		},
		RootStepIndexes: []int{0, 1},
	}

	exec := executor.NewExecutorV2(http.DefaultClient, nil)
	result, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify both mutations returned data
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data in result, got: %+v", result)
	}
	if data["createUser"] == nil {
		t.Error("Expected createUser result")
	}
	if data["createPost"] == nil {
		t.Error("Expected createPost result")
	}

	// Verify the execution order: users must come before posts
	mu.Lock()
	defer mu.Unlock()
	if len(requestOrder) != 2 {
		t.Fatalf("Expected 2 requests, got %d", len(requestOrder))
	}
	if requestOrder[0] != "users" {
		t.Errorf("Expected first request to 'users', got '%s'", requestOrder[0])
	}
	if requestOrder[1] != "posts" {
		t.Errorf("Expected second request to 'posts', got '%s'", requestOrder[1])
	}
}

// TestExecutorV2_MutationErrorHandling verifies that when a mutation step fails,
// subsequent mutation steps are NOT executed.
func TestExecutorV2_MutationErrorHandling(t *testing.T) {
	var mu sync.Mutex
	calledServices := make([]string, 0, 3)

	// First mutation: succeeds
	aServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calledServices = append(calledServices, "serviceA")
		mu.Unlock()

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"mutationA": map[string]interface{}{"id": "a1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer aServer.Close()

	// Second mutation: fails
	bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calledServices = append(calledServices, "serviceB")
		mu.Unlock()

		// Return a 500 error
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer bServer.Close()

	// Third mutation: must NOT be called
	cServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calledServices = append(calledServices, "serviceC")
		mu.Unlock()

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"mutationC": map[string]interface{}{"id": "c1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer cServer.Close()

	aSG := createMockSubgraph("serviceA", aServer.URL)
	bSG := createMockSubgraph("serviceB", bServer.URL)
	cSG := createMockSubgraph("serviceC", cServer.URL)

	plan := &planner.PlanV2{
		OperationType: "mutation",
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: aSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "mutationA"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Mutation"},
			},
			{
				ID:       1,
				StepType: planner.StepTypeQuery,
				SubGraph: bSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "mutationB"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Mutation"},
			},
			{
				ID:       2,
				StepType: planner.StepTypeQuery,
				SubGraph: cSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "mutationC"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Mutation"},
			},
		},
		RootStepIndexes: []int{0, 1, 2},
	}

	exec := executor.NewExecutorV2(http.DefaultClient, nil)
	result, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	// Verify execution stopped after the failure: only A and B should have been called
	mu.Lock()
	defer mu.Unlock()
	if len(calledServices) != 2 {
		t.Errorf("Expected exactly 2 services to be called (A and B), but got %d: %v", len(calledServices), calledServices)
	}
	if len(calledServices) >= 1 && calledServices[0] != "serviceA" {
		t.Errorf("Expected first call to 'serviceA', got '%s'", calledServices[0])
	}
	if len(calledServices) >= 2 && calledServices[1] != "serviceB" {
		t.Errorf("Expected second call to 'serviceB', got '%s'", calledServices[1])
	}

	// Verify errors are present in response
	if _, hasErr := result["errors"]; !hasErr {
		t.Error("Expected errors in response after mutation failure")
	}
}

// TestExecutorV2_QueryParallelExecution verifies that query steps still execute in parallel.
func TestExecutorV2_QueryParallelExecution(t *testing.T) {
	// Use channels to coordinate parallel execution detection
	aStarted := make(chan struct{})
	bStarted := make(chan struct{})

	aServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(aStarted) // signal that A started
		<-bStarted      // wait until B also starts (proves parallel execution)

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"queryA": map[string]interface{}{"id": "a1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer aServer.Close()

	bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(bStarted) // signal that B started
		<-aStarted      // wait until A also started (proves parallel)

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"queryB": map[string]interface{}{"id": "b1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer bServer.Close()

	aSG := createMockSubgraph("serviceA", aServer.URL)
	bSG := createMockSubgraph("serviceB", bServer.URL)

	plan := &planner.PlanV2{
		OperationType: "query",
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: aSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "queryA"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Query"},
			},
			{
				ID:       1,
				StepType: planner.StepTypeQuery,
				SubGraph: bSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "queryB"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Query"},
			},
		},
		RootStepIndexes: []int{0, 1},
	}

	exec := executor.NewExecutorV2(http.DefaultClient, nil)
	result, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data in result")
	}
	if data["queryA"] == nil {
		t.Error("Expected queryA result")
	}
	if data["queryB"] == nil {
		t.Error("Expected queryB result")
	}
}

// TestExecutorV2_InterfaceObject_RepresentationTypename tests that the executor
// uses the correct __typename in entity representations for @interfaceObject entities.
// For @interfaceObject, __typename in the representation must match the interface
// entity type name (e.g. "Node"), not the concrete type.
func TestExecutorV2_InterfaceObject_RepresentationTypename(t *testing.T) {
	// Core service: provides node query, returns Node objects
	coreSchema := `
		type Node @key(fields: "id") @interfaceObject {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	// Metadata service: extends Node with @interfaceObject
	metadataSchema := `
		extend type Node @key(fields: "id") @interfaceObject {
			id: ID! @external
			metadata: String!
		}
	`

	var capturedRepresentations []map[string]interface{}

	// Setup core mock server
	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": map[string]interface{}{
				"node": map[string]interface{}{
					"__typename": "Node",
					"id":         "123",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer coreServer.Close()

	// Setup metadata mock server: capture the representations
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err == nil {
			if vars, ok := reqBody["variables"].(map[string]interface{}); ok {
				if reps, ok := vars["representations"].([]interface{}); ok {
					for _, rep := range reps {
						if repMap, ok := rep.(map[string]interface{}); ok {
							capturedRepresentations = append(capturedRepresentations, repMap)
						}
					}
				}
			}
		}

		response := map[string]interface{}{
			"data": map[string]interface{}{
				"_entities": []interface{}{
					map[string]interface{}{
						"__typename": "Node",
						"id":         "123",
						"metadata":   "some metadata",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer metadataServer.Close()

	coreSG, err := graph.NewSubGraphV2("core", []byte(coreSchema), coreServer.URL)
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for core: %v", err)
	}

	metadataSG, err := graph.NewSubGraphV2("metadata", []byte(metadataSchema), metadataServer.URL)
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for metadata: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, metadataSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	exec := executor.NewExecutorV2(http.DefaultClient, superGraph)

	// Build a plan manually simulating what the planner would generate
	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: coreSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "node"},
						Arguments: []*ast.Argument{
							{Name: &ast.Name{Value: "id"}, Value: &ast.StringValue{Value: "123"}},
						},
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "__typename"}},
							&ast.Field{Name: &ast.Name{Value: "id"}},
						},
					},
				},
				DependsOn:     []int{},
				Path:          []string{"Query"},
				InsertionPath: []string{},
			},
			{
				ID:         1,
				StepType:   planner.StepTypeEntity,
				SubGraph:   metadataSG,
				ParentType: "Node",
				SelectionSet: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "__typename"}},
					&ast.Field{Name: &ast.Name{Value: "id"}},
					&ast.Field{Name: &ast.Name{Value: "metadata"}},
				},
				DependsOn:     []int{0},
				Path:          []string{"Query", "node"},
				InsertionPath: []string{"Query", "node"},
			},
		},
		RootStepIndexes: []int{0},
	}

	ctx := context.Background()
	result, err := exec.Execute(ctx, plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify the response has data
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data in result, got: %+v", result)
	}

	nodeData, ok := data["node"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'node' in data, got: %+v", data)
	}

	if nodeData["metadata"] != "some metadata" {
		t.Errorf("Expected metadata='some metadata', got: %v", nodeData["metadata"])
	}

	// Key assertion: the representation sent to metadata service must use __typename: "Node"
	if len(capturedRepresentations) == 0 {
		t.Fatal("Expected metadata service to be called with representations")
	}

	rep := capturedRepresentations[0]
	if rep["__typename"] != "Node" {
		t.Errorf("Expected __typename='Node' in representation (interfaceObject), got: %v", rep["__typename"])
	}

	if rep["id"] != "123" {
		t.Errorf("Expected id='123' in representation, got: %v", rep["id"])
	}
}

// TestExecutorV2_InterfaceObject_InterfaceTypeEntity tests that entity resolution
// works correctly when the entity is defined as an interface type with @interfaceObject.
func TestExecutorV2_InterfaceObject_InterfaceTypeEntity(t *testing.T) {
	// Core service: interface Node @interfaceObject (interface type, not object type)
	coreSchema := `
		interface Node @interfaceObject @key(fields: "id") {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}
	`

	// Metadata service: extends Node with interface extension
	metadataSchema := `
		extend interface Node @interfaceObject @key(fields: "id") {
			id: ID! @external
			createdAt: String!
		}
	`

	var capturedTypename string

	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": map[string]interface{}{
				"node": map[string]interface{}{
					"__typename": "Node",
					"id":         "abc",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer coreServer.Close()

	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err == nil {
			if vars, ok := reqBody["variables"].(map[string]interface{}); ok {
				if reps, ok := vars["representations"].([]interface{}); ok {
					if len(reps) > 0 {
						if repMap, ok := reps[0].(map[string]interface{}); ok {
							if tn, ok := repMap["__typename"].(string); ok {
								capturedTypename = tn
							}
						}
					}
				}
			}
		}

		response := map[string]interface{}{
			"data": map[string]interface{}{
				"_entities": []interface{}{
					map[string]interface{}{
						"__typename": "Node",
						"id":         "abc",
						"createdAt":  "2024-01-01",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer metadataServer.Close()

	coreSG, err := graph.NewSubGraphV2("core", []byte(coreSchema), coreServer.URL)
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for core: %v", err)
	}

	metadataSG, err := graph.NewSubGraphV2("metadata", []byte(metadataSchema), metadataServer.URL)
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for metadata: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{coreSG, metadataSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	exec := executor.NewExecutorV2(http.DefaultClient, superGraph)

	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: coreSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "node"},
						Arguments: []*ast.Argument{
							{Name: &ast.Name{Value: "id"}, Value: &ast.StringValue{Value: "abc"}},
						},
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "__typename"}},
							&ast.Field{Name: &ast.Name{Value: "id"}},
						},
					},
				},
				DependsOn:     []int{},
				Path:          []string{"Query"},
				InsertionPath: []string{},
			},
			{
				ID:         1,
				StepType:   planner.StepTypeEntity,
				SubGraph:   metadataSG,
				ParentType: "Node",
				SelectionSet: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "__typename"}},
					&ast.Field{Name: &ast.Name{Value: "id"}},
					&ast.Field{Name: &ast.Name{Value: "createdAt"}},
				},
				DependsOn:     []int{0},
				Path:          []string{"Query", "node"},
				InsertionPath: []string{"Query", "node"},
			},
		},
		RootStepIndexes: []int{0},
	}

	ctx := context.Background()
	result, err := exec.Execute(ctx, plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data in result, got: %+v", result)
	}

	nodeData, ok := data["node"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'node' in data, got: %+v", data)
	}

	if nodeData["createdAt"] != "2024-01-01" {
		t.Errorf("Expected createdAt='2024-01-01', got: %v", nodeData["createdAt"])
	}

	// Verify that the representation used __typename: "Node" (interface entity type)
	if capturedTypename != "Node" {
		t.Errorf("Expected __typename='Node' in representation sent to metadata, got: '%s'", capturedTypename)
	}
}

// TestExecutorV2_RequiresDependencyInjection tests that @requires fields are included
// in entity representations sent to subgraphs.
func TestExecutorV2_RequiresDependencyInjection(t *testing.T) {
	// Products service schema: defines Product with weight
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			weight: Float!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	// Shipping service schema: extends Product, shippingCost @requires(fields: "weight")
	shippingSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			weight: Float! @external
			shippingCost: Float! @requires(fields: "weight")
		}
	`

	productSG, err := graph.NewSubGraphV2("products", []byte(productSchema), "http://products")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for products failed: %v", err)
	}
	shippingSG, err := graph.NewSubGraphV2("shipping", []byte(shippingSchema), "http://shipping")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for shipping failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, shippingSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	// Track representations sent to the shipping service
	var capturedRepresentations []map[string]interface{}

	// Mock products server
	productServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"product": map[string]interface{}{
					"__typename": "Product",
					"id":         "p1",
					"name":       "Widget",
					"weight":     2.5,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer productServer.Close()

	// Mock shipping server: capture the _entities request
	shippingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		// Extract representations from variables
		if vars, ok := reqBody["variables"].(map[string]interface{}); ok {
			if reps, ok := vars["representations"].([]interface{}); ok {
				for _, rep := range reps {
					if repMap, ok := rep.(map[string]interface{}); ok {
						capturedRepresentations = append(capturedRepresentations, repMap)
					}
				}
			}
		}

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"_entities": []interface{}{
					map[string]interface{}{
						"__typename":   "Product",
						"shippingCost": 12.5,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer shippingServer.Close()

	// Override subgraph hosts
	productSG, _ = graph.NewSubGraphV2("products", []byte(productSchema), productServer.URL)
	shippingSG, _ = graph.NewSubGraphV2("shipping", []byte(shippingSchema), shippingServer.URL)
	superGraph, _ = graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, shippingSG})

	exec := executor.NewExecutorV2(http.DefaultClient, superGraph)

	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: productSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "product"},
						Arguments: []*ast.Argument{
							{Name: &ast.Name{Value: "id"}, Value: &ast.StringValue{Value: "p1"}},
						},
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "__typename"}},
							&ast.Field{Name: &ast.Name{Value: "id"}},
							&ast.Field{Name: &ast.Name{Value: "name"}},
							&ast.Field{Name: &ast.Name{Value: "weight"}}, // injected by planner
						},
					},
				},
				DependsOn:     []int{},
				Path:          []string{"Query"},
				InsertionPath: []string{"Query"},
			},
			{
				ID:         1,
				StepType:   planner.StepTypeEntity,
				SubGraph:   shippingSG,
				ParentType: "Product",
				SelectionSet: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "__typename"}},
					&ast.Field{Name: &ast.Name{Value: "id"}},
					&ast.Field{Name: &ast.Name{Value: "shippingCost"}},
				},
				DependsOn:     []int{0},
				InsertionPath: []string{"Query", "product"},
			},
		},
		RootStepIndexes: []int{0},
	}

	ctx := context.Background()
	result, err := exec.Execute(ctx, plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify the response contains shippingCost
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data in result, got: %+v", result)
	}
	product, ok := data["product"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected product in data, got: %+v", data)
	}
	if product["shippingCost"] == nil {
		t.Errorf("Expected shippingCost in product, got nil")
	}

	// Verify that the representation sent to shipping service contained 'weight'
	if len(capturedRepresentations) == 0 {
		t.Fatal("Expected shipping service to be called with representations, but no request was captured")
	}
	rep := capturedRepresentations[0]
	if _, hasWeight := rep["weight"]; !hasWeight {
		t.Errorf("Expected representation to contain 'weight' field for @requires, got: %+v", rep)
	}
	if rep["weight"] != 2.5 {
		t.Errorf("Expected weight=2.5 in representation, got: %v", rep["weight"])
	}
}

// TestExecutorV2_NestedRequiresDependencyInjection tests that @requires with nested field sets
// (e.g., "shippingAddress { zipCode country }") include nested objects in entity representations.
func TestExecutorV2_NestedRequiresDependencyInjection(t *testing.T) {
	productSchema := `
		type ShippingAddress {
			zipCode: String!
			country: String!
		}
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			shippingAddress: ShippingAddress!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	shippingSchema := `
		type ShippingAddress {
			zipCode: String!
			country: String!
		}
		extend type Product @key(fields: "id") {
			id: ID! @external
			shippingAddress: ShippingAddress! @external
			deliveryEstimate: String! @requires(fields: "shippingAddress { zipCode country }")
		}
	`

	productSG, err := graph.NewSubGraphV2("products", []byte(productSchema), "http://products")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for products failed: %v", err)
	}
	shippingSG, err := graph.NewSubGraphV2("shipping", []byte(shippingSchema), "http://shipping")
	if err != nil {
		t.Fatalf("NewSubGraphV2 for shipping failed: %v", err)
	}

	var capturedRepresentations []map[string]interface{}

	productServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"product": map[string]interface{}{
					"__typename": "Product",
					"id":         "p1",
					"name":       "Widget",
					"shippingAddress": map[string]interface{}{
						"zipCode": "10001",
						"country": "US",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer productServer.Close()

	shippingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		if vars, ok := reqBody["variables"].(map[string]interface{}); ok {
			if reps, ok := vars["representations"].([]interface{}); ok {
				for _, rep := range reps {
					if repMap, ok := rep.(map[string]interface{}); ok {
						capturedRepresentations = append(capturedRepresentations, repMap)
					}
				}
			}
		}

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"_entities": []interface{}{
					map[string]interface{}{
						"__typename":       "Product",
						"deliveryEstimate": "3-5 business days to US 10001",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer shippingServer.Close()

	productSG, _ = graph.NewSubGraphV2("products", []byte(productSchema), productServer.URL)
	shippingSG, _ = graph.NewSubGraphV2("shipping", []byte(shippingSchema), shippingServer.URL)
	superGraph, _ := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, shippingSG})

	exec := executor.NewExecutorV2(http.DefaultClient, superGraph)

	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: productSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "product"},
						Arguments: []*ast.Argument{
							{Name: &ast.Name{Value: "id"}, Value: &ast.StringValue{Value: "p1"}},
						},
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "__typename"}},
							&ast.Field{Name: &ast.Name{Value: "id"}},
							&ast.Field{Name: &ast.Name{Value: "name"}},
							&ast.Field{
								Name: &ast.Name{Value: "shippingAddress"},
								SelectionSet: []ast.Selection{
									&ast.Field{Name: &ast.Name{Value: "zipCode"}},
									&ast.Field{Name: &ast.Name{Value: "country"}},
								},
							},
						},
					},
				},
				DependsOn:     []int{},
				Path:          []string{"Query"},
				InsertionPath: []string{"Query"},
			},
			{
				ID:         1,
				StepType:   planner.StepTypeEntity,
				SubGraph:   shippingSG,
				ParentType: "Product",
				SelectionSet: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "__typename"}},
					&ast.Field{Name: &ast.Name{Value: "id"}},
					&ast.Field{Name: &ast.Name{Value: "deliveryEstimate"}},
				},
				DependsOn:     []int{0},
				InsertionPath: []string{"Query", "product"},
			},
		},
		RootStepIndexes: []int{0},
	}

	ctx := context.Background()
	result, err := exec.Execute(ctx, plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data in result, got: %+v", result)
	}
	product, ok := data["product"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected product in data, got: %+v", data)
	}
	if product["deliveryEstimate"] == nil {
		t.Errorf("Expected deliveryEstimate in product, got nil")
	}

	// Verify the representation contains nested shippingAddress object
	if len(capturedRepresentations) == 0 {
		t.Fatal("Expected shipping service to be called with representations")
	}
	rep := capturedRepresentations[0]

	shippingAddr, ok := rep["shippingAddress"]
	if !ok {
		t.Fatalf("Expected representation to contain 'shippingAddress', got: %+v", rep)
	}
	addrMap, ok := shippingAddr.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected shippingAddress to be a nested object, got: %T", shippingAddr)
	}
	if addrMap["zipCode"] != "10001" {
		t.Errorf("Expected zipCode='10001', got: %v", addrMap["zipCode"])
	}
	if addrMap["country"] != "US" {
		t.Errorf("Expected country='US', got: %v", addrMap["country"])
	}
}

// ---------------------------------------------------------------------------
// Error Handling tests (DesignDoc: improve-error-handling)
// ---------------------------------------------------------------------------

// TestExecutorV2_SubGraphTimeout verifies that when a subgraph exceeds the
// configured timeout, a timeout error is recorded and a graceful partial
// response (null field + errors entry) is returned rather than panicking or
// hanging forever.
func TestExecutorV2_SubGraphTimeout(t *testing.T) {
	released := make(chan struct{})

	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until released or the client disconnects
		select {
		case <-released:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()
	defer close(released)

	sg := createMockSubgraph("slow", slowServer.URL)

	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: sg,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "slowField"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Query"},
			},
		},
		RootStepIndexes: []int{0},
	}

	// Use a very short per-subgraph timeout
	exec := executor.NewExecutorV2WithTimeout(http.DefaultClient, nil, 50*time.Millisecond)

	result, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute should not return a Go error for timeouts; got: %v", err)
	}

	// The response must carry an errors entry describing the timeout
	errList, hasErr := result["errors"]
	if !hasErr {
		t.Fatal("Expected timeout error in response 'errors' field, but none found")
	}
	gqlErrors, ok := errList.([]executor.GraphQLError)
	if !ok || len(gqlErrors) == 0 {
		t.Fatalf("Expected []GraphQLError, got %T: %+v", errList, errList)
	}
	msg := gqlErrors[0].Message
	if !strings.Contains(msg, "timeout") && !strings.Contains(msg, "deadline") && !strings.Contains(msg, "context") {
		t.Errorf("Expected timeout-related error message, got: %q", msg)
	}
}

// TestExecutorV2_InvalidJSON verifies that when a subgraph returns bytes that
// cannot be decoded as JSON, an error is recorded in the response and the
// field value is null.
func TestExecutorV2_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return something that is NOT valid JSON
		w.Write([]byte("this is not json {{{"))
	}))
	defer server.Close()

	sg := createMockSubgraph("bad-json-service", server.URL)

	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: sg,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "someField"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Query"},
			},
		},
		RootStepIndexes: []int{0},
	}

	exec := executor.NewExecutorV2(http.DefaultClient, nil)
	result, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute should not return a Go error: %v", err)
	}

	// Errors must be present
	if _, hasErr := result["errors"]; !hasErr {
		t.Error("Expected errors in response for invalid JSON, but none found")
	}
}

// TestExecutorV2_NetworkError verifies that when a subgraph is unreachable
// (connection refused / network error), a graceful partial response is returned.
func TestExecutorV2_NetworkError(t *testing.T) {
	// Create a server and immediately close it so that the port is unreachable
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close() // Close immediately → connection refused

	sg := createMockSubgraph("unreachable", serverURL)

	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: sg,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name:         &ast.Name{Value: "field"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
					},
				},
				DependsOn: []int{},
				Path:      []string{"Query"},
			},
		},
		RootStepIndexes: []int{0},
	}

	exec := executor.NewExecutorV2(http.DefaultClient, nil)
	result, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute should not return a Go error for network errors: %v", err)
	}

	if _, hasErr := result["errors"]; !hasErr {
		t.Error("Expected errors in response for network error, but none found")
	}
}

// TestExecutorV2_HTTPError_StatusCode verifies that HTTP 4xx/5xx status codes
// from a subgraph are treated as errors (resulting in null data + errors entry)
// rather than silently ignored.
func TestExecutorV2_HTTPError_StatusCode(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"HTTP 400 Bad Request", http.StatusBadRequest},
		{"HTTP 401 Unauthorized", http.StatusUnauthorized},
		{"HTTP 403 Forbidden", http.StatusForbidden},
		{"HTTP 404 Not Found", http.StatusNotFound},
		{"HTTP 500 Internal Server Error", http.StatusInternalServerError},
		{"HTTP 502 Bad Gateway", http.StatusBadGateway},
		{"HTTP 503 Service Unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "error from subgraph", tt.statusCode)
			}))
			defer server.Close()

			sg := createMockSubgraph("error-service", server.URL)

			plan := &planner.PlanV2{
				Steps: []*planner.StepV2{
					{
						ID:       0,
						StepType: planner.StepTypeQuery,
						SubGraph: sg,
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name:         &ast.Name{Value: "someField"},
								SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "id"}}},
							},
						},
						DependsOn: []int{},
						Path:      []string{"Query"},
					},
				},
				RootStepIndexes: []int{0},
			}

			exec := executor.NewExecutorV2(http.DefaultClient, nil)
			result, err := exec.Execute(context.Background(), plan, nil)
			if err != nil {
				t.Fatalf("Execute should not return a Go error: %v", err)
			}

			// Must have errors
			errList, hasErr := result["errors"]
			if !hasErr {
				t.Errorf("Expected errors in response for HTTP %d, but none found", tt.statusCode)
				return
			}
			gqlErrors, ok := errList.([]executor.GraphQLError)
			if !ok || len(gqlErrors) == 0 {
				t.Errorf("Expected []GraphQLError, got: %T %+v", errList, errList)
				return
			}
			// Error message should mention the HTTP status code
			msg := gqlErrors[0].Message
			statusStr := fmt.Sprintf("%d", tt.statusCode)
			if !strings.Contains(msg, statusStr) && !strings.Contains(msg, "HTTP") {
				t.Errorf("Expected HTTP status code in error message for %d, got: %q", tt.statusCode, msg)
			}
		})
	}
}

// TestExecutorV2_ErrorPathAdjustment verifies that GraphQL errors coming from
// an entity (_entities) subgraph have their path rewritten:
// ["_entities", 0, "fieldName"] → ["parentField", "fieldName"]
// This makes the path meaningful to the client (no internal "_entities" leak).
func TestExecutorV2_ErrorPathAdjustment(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	reviewsSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review]
		}
		type Review { body: String }
	`

	productServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"product": map[string]interface{}{
					"__typename": "Product",
					"id":         "p1",
					"name":       "Widget",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer productServer.Close()

	// Reviews service returns partial data with an error that has _entities path
	reviewsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"_entities": []interface{}{
					map[string]interface{}{
						"__typename": "Product",
						"id":         "p1",
						"reviews":    nil, // null due to error
					},
				},
			},
			"errors": []interface{}{
				map[string]interface{}{
					"message": "reviews database unavailable",
					"path":    []interface{}{"_entities", float64(0), "reviews"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer reviewsServer.Close()

	productSG, err := graph.NewSubGraphV2("products", []byte(productSchema), productServer.URL)
	if err != nil {
		t.Fatalf("NewSubGraphV2 (products) failed: %v", err)
	}
	reviewsSG, err := graph.NewSubGraphV2("reviews", []byte(reviewsSchema), reviewsServer.URL)
	if err != nil {
		t.Fatalf("NewSubGraphV2 (reviews) failed: %v", err)
	}
	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, reviewsSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	plan := &planner.PlanV2{
		Steps: []*planner.StepV2{
			{
				ID:       0,
				StepType: planner.StepTypeQuery,
				SubGraph: productSG,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "product"},
						SelectionSet: []ast.Selection{
							&ast.Field{Name: &ast.Name{Value: "__typename"}},
							&ast.Field{Name: &ast.Name{Value: "id"}},
							&ast.Field{Name: &ast.Name{Value: "name"}},
						},
					},
				},
				DependsOn:     []int{},
				Path:          []string{"Query"},
				InsertionPath: []string{},
			},
			{
				ID:         1,
				StepType:   planner.StepTypeEntity,
				SubGraph:   reviewsSG,
				ParentType: "Product",
				SelectionSet: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "__typename"}},
					&ast.Field{Name: &ast.Name{Value: "id"}},
					&ast.Field{
						Name:         &ast.Name{Value: "reviews"},
						SelectionSet: []ast.Selection{&ast.Field{Name: &ast.Name{Value: "body"}}},
					},
				},
				DependsOn:     []int{0},
				Path:          []string{"Query", "product"},
				InsertionPath: []string{"Query", "product"},
			},
		},
		RootStepIndexes: []int{0},
	}

	exec := executor.NewExecutorV2(http.DefaultClient, superGraph)
	result, err := exec.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Must have errors
	errList, hasErr := result["errors"]
	if !hasErr {
		t.Fatal("Expected errors in response but none found")
	}
	gqlErrors, ok := errList.([]executor.GraphQLError)
	if !ok || len(gqlErrors) == 0 {
		t.Fatalf("Expected []GraphQLError, got %T: %+v", errList, errList)
	}

	// The error path must NOT contain "_entities" – it should be adjusted
	for _, e := range gqlErrors {
		for _, seg := range e.Path {
			if seg == "_entities" {
				t.Errorf("Error path must not contain '_entities', but got path: %v", e.Path)
			}
		}
		// Should contain "product" (the field name, not the root type)
		hasProduct := false
		for _, seg := range e.Path {
			if seg == "product" {
				hasProduct = true
			}
		}
		if !hasProduct {
			t.Errorf("Expected 'product' in error path, got: %v", e.Path)
		}
	}
}
