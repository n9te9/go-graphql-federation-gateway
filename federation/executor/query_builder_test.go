package executor_test

import (
	"testing"

	"github.com/n9te9/federation-gateway/federation/executor"
	"github.com/n9te9/federation-gateway/federation/graph"
	"github.com/n9te9/federation-gateway/federation/planner"
)

func TestQueryBuilder_Build(t *testing.T) {
	tests := []struct {
		name    string
		step    *planner.Step
		query   string
		want    string
		wantErr error
	}{
		{
			name: "Happy case: Build base simple query",
			step: &planner.Step{
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
			},
			query: `query {
	products {
		upc
		name
	}
}`,
			want: `query {
	products {
		upc
		name
	}
}`,
		}, {
			name: "Happy case: Build entities simple query",
			step: &planner.Step{
				SubGraph: func() *graph.SubGraph {
					sdl := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					height: Int
					price: Int @external
				}`
					sg, err := graph.NewSubGraph("aaaaaaaaa", []byte(sdl), "")
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
			},
			query: `query {
	products {
		upc
		name
		weight
		height
	}
}`,
			want: `query ($representations: [_Any!]!) {
	_entities(representations: $representations) {
		... on Product {
			weight
			height
		}
	}
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := executor.NewQueryBuilder()
			got, _, err := qb.Build(tt.step, nil)
			if err != nil && tt.wantErr == nil {
				t.Errorf("QueryBuilder.Build() unexpected error: %v", err)
				return
			}
			if err == nil && tt.wantErr != nil {
				t.Errorf("QueryBuilder.Build() expected error: %v, got nil", tt.wantErr)
				return
			}
			if err != nil && tt.wantErr != nil && err.Error() != tt.wantErr.Error() {
				t.Errorf("QueryBuilder.Build() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("QueryBuilder.Build()\n %v, want\n %v", got, tt.want)
			}
		})
	}
}
