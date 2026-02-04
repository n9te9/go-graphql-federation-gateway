package graph_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
)

func TestSuperGraph_Merge(t *testing.T) {
	tests := []struct {
		name           string
		superGraph     *graph.SuperGraph
		mergedSubGraph *graph.SubGraph
		wantErr        error
	}{
		{
			name: "Happy case: Merge valid  extend type SubGraph",
			superGraph: func() *graph.SuperGraph {
				sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`

				sg, err := graph.NewSubGraph("aaaaaaaaa", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				superGraph, err := graph.NewSuperGraphFromBytes([]byte(sdl))
				if err != nil {
					t.Fatalf("failed to parse root schema: %v", err)
				}
				superGraph.SubGraphs = append(superGraph.SubGraphs, sg)

				return superGraph
			}(),
			mergedSubGraph: func() *graph.SubGraph {
				sdl := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					price: Int @external
				}`

				sg, err := graph.NewSubGraph("hogehoge", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				return sg
			}(),
		},
		{
			name: "Error case: Conflict extend field SubGraph",
			superGraph: func() *graph.SuperGraph {
				sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`

				superGraph, err := graph.NewSuperGraphFromBytes([]byte(sdl))
				if err != nil {
					t.Fatalf("failed to parse root schema: %v", err)
				}

				return superGraph
			}(),
			mergedSubGraph: func() *graph.SubGraph {
				sdl := `extend type Product {
					upc: String!
					description: String
				}`

				sg, err := graph.NewSubGraph("hogehoge", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				return sg
			}(),
			wantErr: errors.New("ownership conflict for field Product.upc"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.superGraph.Merge(tt.mergedSubGraph)
			if err != nil && tt.wantErr == nil {
				t.Errorf("SuperGraph.Merge() unexpected error: %v", err)
				return
			}
			if err == nil && tt.wantErr != nil {
				t.Errorf("SuperGraph.Merge() expected error: %v, got nil", tt.wantErr)
				return
			}
			if err != nil && tt.wantErr != nil && err.Error() != tt.wantErr.Error() {
				t.Errorf("SuperGraph.Merge() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestSuperGraph_GetSubGraphByKey(t *testing.T) {
	tests := []struct {
		name             string
		mergedSuperGraph *graph.SuperGraph
		key              string
		want             *graph.SubGraph
		wantErr          error
	}{
		{
			name: "Happy case: Merge valid extend type SubGraph",
			mergedSuperGraph: func() *graph.SuperGraph {
				sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`
				sg, err := graph.NewSubGraph("aaaaaaaaa", []byte(sdl), "")
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
					weight: Int
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
			want: func() *graph.SubGraph {
				sdl := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					price: Int @external
				}`

				sg, err := graph.NewSubGraph("hogehoge", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				return sg
			}(),
			key: "Product.weight",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ignores := []cmp.Option{
				cmpopts.IgnoreUnexported(graph.SubGraph{}),
			}

			subGraph := tt.mergedSuperGraph.GetSubGraphByKey(tt.key)

			if diff := cmp.Diff(subGraph, tt.want, ignores...); diff != "" {
				t.Errorf("SuperGraph.GetSubGraphByKey() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
