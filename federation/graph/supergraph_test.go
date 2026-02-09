package graph

import (
	"testing"

	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

func TestSuperGraph_GetSubGraphByKey(t *testing.T) {
	tests := []struct {
		name    string
		src     []byte
		key     string
		want    *SubGraph
		wantErr bool
	}{
		{
			name: "Happy case: Merge valid extend type SubGraph",
			src: []byte(`
				type Query {
					product(upc: String!): Product
				}
				type Product {
					upc: String!
					name: String
				}
			`),
			key: "Product.weight",
			want: &SubGraph{
				Name: "hogehoge",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := lexer.New(string(tt.src))
			p := parser.New(l)
			doc := p.ParseDocument()
			if len(p.Errors()) > 0 {
				t.Fatal(p.Errors())
			}

			sg := &SuperGraph{
				Schema:       doc,
				SubGraphs:    []*SubGraph{},
				OwnershipMap: make(map[string]*ownership),
			}

			subgraphSDL := `extend type Product @key(fields: "upc") {
					upc: String! @external
					weight: Int
					price: Int @external
				}`
			subGraph, err := NewSubGraph("hogehoge", []byte(subgraphSDL), "")
			if err != nil {
				t.Fatal(err)
			}

			if err := sg.Merge(subGraph); (err != nil) != tt.wantErr {
				t.Errorf("SuperGraph.Merge() error = %v, wantErr %v", err, tt.wantErr)
			}

			got := sg.GetSubGraphByKey(tt.key)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("SuperGraph.GetSubGraphByKey() = %v, want %v", got, tt.want)
			}

			if got != nil && tt.want != nil && got.Name != tt.want.Name {
				t.Errorf("SuperGraph.GetSubGraphByKey() Name = %v, want %v", got.Name, tt.want.Name)
			}
		})
	}
}
