package graph

import (
	"errors"
	"fmt"

	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

type SuperGraph struct {
	Schema       *ast.Document
	SubGraphs    []*SubGraph
	SDL          string
	OwnershipMap map[string]*ownership
}

func NewSuperGraph(allSchemaSrc []byte, subGraphs []*SubGraph) (*SuperGraph, error) {
	l := lexer.New(string(allSchemaSrc))
	p := parser.New(l)
	doc := p.ParseDocument()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse error: %v", p.Errors())
	}

	superGraph := &SuperGraph{
		Schema:       doc,
		SubGraphs:    subGraphs,
		OwnershipMap: make(map[string]*ownership),
	}

	for _, sg := range subGraphs {
		if err := superGraph.UpdateOwnershipMap(sg); err != nil {
			return nil, err
		}
	}

	return superGraph, nil
}

func NewSuperGraphFromBytes(src []byte) (*SuperGraph, error) {
	l := lexer.New(string(src))
	p := parser.New(l)
	doc := p.ParseDocument()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse error: %v", p.Errors())
	}

	return &SuperGraph{
		Schema:       doc,
		SDL:          string(src),
		SubGraphs:    make([]*SubGraph, 0),
		OwnershipMap: newOwnershipMapForSuperGraph(doc),
	}, nil
}

func (sg *SuperGraph) Merge(subGraph *SubGraph) error {
	l := lexer.New(subGraph.SDL)
	p := parser.New(l)
	newDoc := p.ParseDocument()
	if len(p.Errors()) > 0 {
		return fmt.Errorf("parse error: %v", p.Errors())
	}

	if sg.Schema == nil {
		sg.Schema = newDoc
	} else {
		sg.mergeSchema(newDoc)
	}

	if err := sg.UpdateOwnershipMap(subGraph); err != nil {
		return err
	}

	sg.SubGraphs = append(sg.SubGraphs, subGraph)

	return nil
}

func (sg *SuperGraph) mergeSchema(newDoc *ast.Document) {
	sg.Schema.Definitions = append(sg.Schema.Definitions, newDoc.Definitions...)
}

func (sg *SuperGraph) UpdateOwnershipMap(subGraph *SubGraph) error {
	subGraphOwnershipMap := subGraph.OwnershipFieldMap()

	for k, v := range subGraphOwnershipMap {
		_, exists := sg.OwnershipMap[k]
		if exists {
			var ok bool
			for _, existingSubGraph := range sg.SubGraphs {
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
	if _, ok := sg.OwnershipMap[key]; !ok {
		return nil
	}

	for _, subGraph := range sg.SubGraphs {
		if _, ok := subGraph.ownershipFieldMap[key]; ok {
			return subGraph
		}
	}

	return nil
}

func (sg *SuperGraph) MustGetSubGraphByKey(key string) *SubGraph {
	subGraph := sg.GetSubGraphByKey(key)
	if subGraph == nil {
		panic("subgraph not found for key: " + key)
	}

	return subGraph
}

func (sg *SuperGraph) GetOperation(doc *ast.Document) *ast.OperationDefinition {
	for _, def := range doc.Definitions {
		if op, ok := def.(*ast.OperationDefinition); ok {
			return op
		}
	}
	return nil
}
