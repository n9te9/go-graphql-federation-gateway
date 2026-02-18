package executor_test

import (
	"strings"
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/token"
)

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		name              string
		step              *planner.StepV2
		representations   []map[string]interface{}
		variables         map[string]interface{}
		expectedQueryPart string // Part of the expected query
		expectError       bool
		checkVariableDef  bool // Whether to check for variable definition
	}{
		{
			name: "Simple root query",
			step: &planner.StepV2{
				ID:       1,
				StepType: planner.StepTypeQuery,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "product"},
						Arguments: []*ast.Argument{
							{
								Name: &ast.Name{Value: "id"},
								Value: &ast.StringValue{
									Token: token.Token{Type: token.STRING, Literal: "1"},
									Value: "1",
								},
							},
						},
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "id"},
							},
							&ast.Field{
								Name: &ast.Name{Value: "name"},
							},
						},
					},
				},
			},
			representations:   nil,
			variables:         map[string]interface{}{},
			expectedQueryPart: "product",
			expectError:       false,
			checkVariableDef:  false,
		},
		{
			name: "Root query with variable",
			step: &planner.StepV2{
				ID:       1,
				StepType: planner.StepTypeQuery,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "product"},
						Arguments: []*ast.Argument{
							{
								Name: &ast.Name{Value: "id"},
								Value: &ast.Variable{
									Name: "productId",
								},
							},
						},
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "id"},
							},
							&ast.Field{
								Name: &ast.Name{Value: "name"},
							},
						},
					},
				},
			},
			representations:   nil,
			variables:         map[string]interface{}{"productId": "p1"},
			expectedQueryPart: "$productId",
			expectError:       false,
			checkVariableDef:  true,
		},
		{
			name: "Root query with multiple variables",
			step: &planner.StepV2{
				ID:       1,
				StepType: planner.StepTypeQuery,
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "product"},
						Arguments: []*ast.Argument{
							{
								Name: &ast.Name{Value: "id"},
								Value: &ast.Variable{
									Name: "productId",
								},
							},
							{
								Name: &ast.Name{Value: "includeDetails"},
								Value: &ast.Variable{
									Name: "withDetails",
								},
							},
						},
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "id"},
							},
							&ast.Field{
								Name: &ast.Name{Value: "name"},
							},
						},
					},
				},
			},
			representations:   nil,
			variables:         map[string]interface{}{"productId": "p1", "withDetails": true},
			expectedQueryPart: "$productId",
			expectError:       false,
			checkVariableDef:  true,
		},
		{
			name: "Entity query with representations",
			step: &planner.StepV2{
				ID:         2,
				StepType:   planner.StepTypeEntity,
				ParentType: "Product",
				SelectionSet: []ast.Selection{
					&ast.Field{
						Name: &ast.Name{Value: "reviews"},
						SelectionSet: []ast.Selection{
							&ast.Field{
								Name: &ast.Name{Value: "body"},
							},
							&ast.Field{
								Name: &ast.Name{Value: "rating"},
							},
						},
					},
				},
			},
			representations: []map[string]interface{}{
				{
					"__typename": "Product",
					"id":         "1",
				},
			},
			variables:         map[string]interface{}{},
			expectedQueryPart: "_entities",
			expectError:       false,
			checkVariableDef:  true, // _entities always has $representations
		},
	}

	qb := executor.NewQueryBuilderV2(nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, variables, err := qb.Build(tt.step, tt.representations, tt.variables, "query")

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !strings.Contains(query, tt.expectedQueryPart) {
				t.Errorf("Expected query to contain %q but got:\n%s", tt.expectedQueryPart, query)
			}

			// Check for variable definition
			if tt.checkVariableDef {
				if !strings.Contains(query, "query (") && !strings.Contains(query, "query(") {
					t.Errorf("Expected query to have variable definition but got:\n%s", query)
				}
			}

			// Verify variables
			if tt.step.StepType == planner.StepTypeEntity && tt.representations != nil {
				if _, ok := variables["representations"]; !ok {
					t.Errorf("Expected variables to contain 'representations'")
				}
			}
		})
	}
}
