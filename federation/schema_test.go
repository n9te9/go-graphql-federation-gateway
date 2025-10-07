package federation_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/n9te9/federation-gateway/federation"
	"github.com/n9te9/goliteql/schema"
)

func TestSuperGraph_Merge(t *testing.T) {
	tests := []struct {
		name           string
		superGraph     *federation.SuperGraph
		mergedSubGraph *federation.SubGraph
		wantErr        error
	}{
		{
			name: "Happy case: Merge valid  extend type SubGraph",
			superGraph: func() *federation.SuperGraph {
				sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`

				l := schema.NewLexer()
				p := schema.NewParser(l)
				rootSchema, err := p.Parse([]byte(sdl))
				if err != nil {
					t.Fatalf("failed to parse root schema: %v", err)
				}

				return federation.NewSuperGraph(rootSchema, nil)
			}(),
			mergedSubGraph: func() *federation.SubGraph {
				sdl := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					price: Int @external
				}`

				sg, err := federation.NewSubGraph("hogehoge", []byte(sdl), "")
				if err != nil {
					t.Fatal(err)
				}

				return sg
			}(),
		},
		{
			name: "Error case: Conflict extend field SubGraph",
			superGraph: func() *federation.SuperGraph {
				sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`

				l := schema.NewLexer()
				p := schema.NewParser(l)
				rootSchema, err := p.Parse([]byte(sdl))
				if err != nil {
					t.Fatalf("failed to parse root schema: %v", err)
				}
				superGraph := federation.NewSuperGraph(rootSchema, nil)
				return superGraph
			}(),
			mergedSubGraph: func() *federation.SubGraph {
				sdl := `extend type Product {
					upc: String!
					name: String
					price: Int
				}`

				sg, err := federation.NewSubGraph("hogehoge", []byte(sdl), "")
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
		mergedSuperGraph *federation.SuperGraph
		key              string
		want             *federation.SubGraph
		wantErr          error
	}{
		{
			name: "Happy case: Merge valid  extend type SubGraph",
			mergedSuperGraph: func() *federation.SuperGraph {
				sdl := `type Query {
					products: [Product]
				}
				
				type Product {
					upc: String!
					name: String
					price: Int
				}`

				l := schema.NewLexer()
				p := schema.NewParser(l)
				rootSchema, err := p.Parse([]byte(sdl))
				if err != nil {
					t.Fatalf("failed to parse root schema: %v", err)
				}

				superGraph := federation.NewSuperGraph(rootSchema, nil)

				subgraphSDL := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					price: Int @external
				}`

				sg, err := federation.NewSubGraph("hogehoge", []byte(subgraphSDL), "")
				if err != nil {
					t.Fatal(err)
				}

				if err := superGraph.Merge(sg); err != nil {
					t.Fatal(err)
				}

				return superGraph
			}(),
			want: func() *federation.SubGraph {
				sdl := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					price: Int @external
				}`

				sg, err := federation.NewSubGraph("hogehoge", []byte(sdl), "")
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
			subGraph := tt.mergedSuperGraph.GetSubGraphByKey(tt.key)
			fmt.Println(subGraph)
			fmt.Println(tt.want)
		})
	}
}
