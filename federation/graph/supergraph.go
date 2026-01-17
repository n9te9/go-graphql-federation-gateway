package graph

import (
	"fmt"

	"github.com/n9te9/goliteql/query"
	"github.com/n9te9/goliteql/schema"
)

type SuperGraph struct {
	Schema       *schema.Schema
	SubGraphs    []*SubGraph
	SDL          string
	OwnershipMap map[string]*ownership
}

func NewSuperGraph(allSchemaSrc []byte, subGraphs []*SubGraph) (*SuperGraph, error) {
	root, err := schema.NewParser(schema.NewLexer()).Parse(allSchemaSrc)
	if err != nil {
		return nil, err
	}

	superGraph := &SuperGraph{
		Schema:       root,
		SubGraphs:    subGraphs,
		OwnershipMap: newOwnershipMapForSuperGraph(root),
	}

	for _, sg := range subGraphs {
		if err := superGraph.Merge(sg); err != nil {
			return nil, err
		}
	}

	return superGraph, nil
}

func NewSuperGraphFromBytes(src []byte) (*SuperGraph, error) {
	root, err := schema.NewParser(schema.NewLexer()).Parse(src)
	if err != nil {
		return nil, err
	}

	return &SuperGraph{
		Schema:       root,
		SDL:          string(src),
		SubGraphs:    make([]*SubGraph, 0),
		OwnershipMap: newOwnershipMapForSuperGraph(root),
	}, nil
}

func (sg *SuperGraph) Merge(subGraph *SubGraph) error {
	sg.SDL += "\n" + subGraph.SDL
	newSchema, err := schema.NewParser(schema.NewLexer()).Parse([]byte(sg.SDL))
	if err != nil {
		return err
	}

	sg.Schema = newSchema
	if err := sg.UpdateOwnershipMap(subGraph); err != nil {
		return err
	}

	sg.SubGraphs = append(sg.SubGraphs, subGraph)

	return nil
}

func (sg *SuperGraph) UpdateOwnershipMap(subGraph *SubGraph) error {
	subGraphOwnershipMap := subGraph.OwnershipMap()

	for k, v := range subGraphOwnershipMap {
		if _, exists := sg.OwnershipMap[k]; exists {
			return fmt.Errorf("ownership conflict for field %s", k)
		}

		sg.OwnershipMap[k] = v
	}

	return nil
}

func (sg *SuperGraph) GetSubGraphByKey(key string) *SubGraph {
	for _, subGraph := range sg.SubGraphs {
		if _, exist := subGraph.ownershipMap[key]; exist {
			return subGraph
		}
	}

	return nil
}

func (sg *SuperGraph) MustGetSubGraphByKey(key string) *SubGraph {
	subgraph := sg.GetSubGraphByKey(key)
	if subgraph == nil {
		panic("SubGraph is nil")
	}

	return subgraph
}

func (sg *SuperGraph) GetOperation(doc *query.Document) *query.Operation {
	if q := doc.Operations.GetQuery(); q != nil {
		return q
	}

	if m := doc.Operations.GetMutation(); m != nil {
		return m
	}

	if s := doc.Operations.GetSubscription(); s != nil {
		return s
	}

	return nil
}
