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

func NewSuperGraph(subGraphs []*SubGraph) (*SuperGraph, error) {
	superGraph := &SuperGraph{
		Schema:       nil,
		SubGraphs:    subGraphs,
		OwnershipMap: make(map[string]*ownership),
	}

	for _, sg := range subGraphs {
		if err := superGraph.UpdateOwnershipMap(sg); err != nil {
			return nil, err
		}
	}

	for _, sg := range subGraphs {
		if err := superGraph.updateSuperGraphSchema(sg); err != nil {
			return nil, err
		}
	}

	return superGraph, nil
}

func (sg *SuperGraph) updateSuperGraphSchema(subGraph *SubGraph) error {
	if sg.Schema == nil {
		schema := *subGraph.Schema
		sg.Schema = &schema
	} else {
		sg.mergeSchema(subGraph.Schema)
	}

	return nil
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
	for _, d := range newDoc.Definitions {
		switch newDef := d.(type) {
		case *ast.ObjectTypeExtension:
			var existingDef *ast.ObjectTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.ObjectTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Fields = append(existingDef.Fields, newDef.Fields...)
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			}
		case *ast.InterfaceTypeExtension:
			var existingDef *ast.InterfaceTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.InterfaceTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Fields = append(existingDef.Fields, newDef.Fields...)
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			}
		case *ast.UnionTypeExtension:
			var existingDef *ast.UnionTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.UnionTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
				existingDef.Types = append(existingDef.Types, newDef.Types...)
			}
		case *ast.InputObjectTypeExtension:
			var existingDef *ast.InputObjectTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.InputObjectTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Fields = append(existingDef.Fields, newDef.Fields...)
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			}
		case *ast.ScalarTypeExtension:
			var existingDef *ast.ScalarTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.ScalarTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			}
		case *ast.EnumTypeExtension:
			var existingDef *ast.EnumTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.EnumTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Values = append(existingDef.Values, newDef.Values...)
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			}
		case *ast.InputObjectTypeDefinition:
			var existingDef *ast.InputObjectTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.InputObjectTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Fields = append(existingDef.Fields, newDef.Fields...)
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			} else {
				sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
			}
		case *ast.UnionTypeDefinition:
			var existingDef *ast.UnionTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.UnionTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
				existingDef.Types = append(existingDef.Types, newDef.Types...)
			} else {
				sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
			}
		case *ast.InterfaceTypeDefinition:
			var existingDef *ast.InterfaceTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.InterfaceTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Fields = append(existingDef.Fields, newDef.Fields...)
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			} else {
				sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
			}
		case *ast.ObjectTypeDefinition:
			var existingDef *ast.ObjectTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.ObjectTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef != nil {
				existingDef.Fields = append(existingDef.Fields, newDef.Fields...)
				existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
			} else {
				sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
			}
		case *ast.DirectiveDefinition:
			var existingDef *ast.DirectiveDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.DirectiveDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef == nil {
				sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
			}
		case *ast.ScalarTypeDefinition:
			var existingDef *ast.ScalarTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.ScalarTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef == nil {
				sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
			}
		case *ast.EnumTypeDefinition:
			var existingDef *ast.EnumTypeDefinition
			for _, def := range sg.Schema.Definitions {
				if ed, ok := def.(*ast.EnumTypeDefinition); ok {
					if ed.Name.String() == newDef.Name.String() {
						existingDef = ed
						break
					}
				}
			}

			if existingDef == nil {
				sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
			}
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
