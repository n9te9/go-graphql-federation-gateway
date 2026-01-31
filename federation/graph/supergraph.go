package graph

import (
	"errors"

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
		SubGraphs:    make([]*SubGraph, 0, len(subGraphs)),
		OwnershipMap: make(map[string]*ownership),
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
	newSchema, err := schema.NewParser(schema.NewLexer()).Parse([]byte(subGraph.SDL))
	if err != nil {
		return err
	}

	if sg.Schema == nil {
		sg.Schema = newSchema
		return sg.UpdateOwnershipMap(subGraph)
	}

	sg.mergeSchema(newSchema)
	if err := sg.UpdateOwnershipMap(subGraph); err != nil {
		return err
	}

	sg.SubGraphs = append(sg.SubGraphs, subGraph)

	return nil
}

func (sg *SuperGraph) mergeSchema(newSchema *schema.Schema) {
	sg.Schema.Types = append(sg.Schema.Types, newSchema.Types...)
	for _, ext := range sg.Schema.Indexes.TypeIndex {
		schemaIndex, ok := newSchema.Indexes.TypeIndex[string(ext.Name)]
		if ok {
			ext.Fields = append(ext.Fields, schemaIndex.Fields...)
		}
	}
}

func (sg *SuperGraph) UpdateOwnershipMap(subGraph *SubGraph) error {
	subGraphOwnershipMap := subGraph.OwnershipFieldMap()

	for k, v := range subGraphOwnershipMap {
		_, exists := sg.OwnershipMap[k]
		if exists {
			var ok bool
			for _, existingSubGraph := range sg.SubGraphs {
				existingSubGraph.OwnershipFieldMap()
				if _, exist := existingSubGraph.ownershipFieldMap[k]; exist {
					ok = true
					break
				}
			}

			if !ok {
				return errors.New("ownership conflict for field " + k)
			}
		}

		sg.OwnershipMap[k] = v
	}

	return nil
}

func (sg *SuperGraph) GetSubGraphByKey(key string) *SubGraph {
	for _, subGraph := range sg.SubGraphs {
		if _, exist := subGraph.ownershipFieldMap[key]; exist {
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
