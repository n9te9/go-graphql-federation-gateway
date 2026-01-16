package federation

import "github.com/n9te9/federation-gateway/federation/graph"

type Schema struct {
	SuperGraph *graph.SuperGraph
}

func NewSchema(superGraph *graph.SuperGraph, name, rootHost string) *Schema {
	return &Schema{
		SuperGraph: superGraph,
	}
}
