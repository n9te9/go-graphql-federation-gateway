package executor_test

import (
	"testing"

	"github.com/n9te9/federation-gateway/federation/executor"
	"github.com/n9te9/federation-gateway/federation/graph"
	"github.com/n9te9/federation-gateway/federation/planner"
)

func TestExecutor_Execute(t *testing.T) {
	tests := []struct {
		name    string
		plan    *planner.Plan
		wantErr error
	}{
		{
			name: "happy case: execute simple plan",
			plan: &planner.Plan{
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
						Status:    planner.Pending,
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
						DependsOn: []int{0},
						Status:    planner.Pending,
						Err:       nil,
						Done:      make(chan struct{}),
					},
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := executor.NewExecutor()
			gotErr := e.Execute(tt.plan)
			if gotErr == nil && tt.wantErr == nil {
				return
			}

			if gotErr == nil && tt.wantErr != nil || tt.wantErr == nil && gotErr != nil {
				t.Fatalf("Planner.Plan() error = %v, wantErr %v", gotErr, tt.wantErr)
			}

			if gotErr.Error() != tt.wantErr.Error() {
				t.Fatalf("Planner.Plan() error = %v, wantErr %v", gotErr, tt.wantErr)
			}
		})
	}
}
