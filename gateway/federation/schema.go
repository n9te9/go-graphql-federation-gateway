package federation

import "github.com/n9te9/goliteql/schema"

type SuperGraph struct {
	Schema    *schema.Schema
	SubGraphs []*SubGraph
}

type SubGraph struct {
	Name         string
	Schema       *schema.Schema
	SDL          string
	Host         string
	IsIntegrated bool
}

func NewSuperGraph(root *schema.Schema, subGraphs []*SubGraph) *SuperGraph {
	return &SuperGraph{
		Schema:    root,
		SubGraphs: subGraphs,
	}
}

func NewSuperGraphFromBytes(src []byte) (*SuperGraph, error) {
	schema, err := schema.NewParser(schema.NewLexer()).Parse(src)
	if err != nil {
		return nil, err
	}

	return &SuperGraph{
		Schema:    schema,
		SubGraphs: make([]*SubGraph, 0),
	}, nil
}

func (sg *SuperGraph) Merge() error {
	for _, subGraph := range sg.SubGraphs {
		if !subGraph.IsIntegrated {
			sg.registerExtentions(subGraph.Schema)
			newSchema, err := sg.Schema.Merge()
			if err != nil {
				return err
			}

			sg.Schema = newSchema
			subGraph.IsIntegrated = true
		}
	}

	return nil
}

func (sg *SuperGraph) registerExtentions(subGraphSchema *schema.Schema) {
	sg.Schema.Definition.Extentions = append(sg.Schema.Definition.Extentions, subGraphSchema.Definition.Extentions...)
	for _, operation := range sg.Schema.Operations {
		operation.Extentions = append(operation.Extentions, subGraphSchema.Operations...)
	}

	for _, typeDefinition := range sg.Schema.Types {
		typeDefinition.Extentions = append(typeDefinition.Extentions, subGraphSchema.Types...)
	}

	for _, interfaceDefinition := range sg.Schema.Interfaces {
		interfaceDefinition.Extentions = append(interfaceDefinition.Extentions, subGraphSchema.Interfaces...)
	}

	for _, unionDefinition := range sg.Schema.Unions {
		unionDefinition.Extentions = append(unionDefinition.Extentions, subGraphSchema.Unions...)
	}

	for _, enumDefinition := range sg.Schema.Enums {
		enumDefinition.Extentions = append(enumDefinition.Extentions, subGraphSchema.Enums...)
	}

	for _, inputDefinition := range sg.Schema.Inputs {
		inputDefinition.Extentions = append(inputDefinition.Extentions, subGraphSchema.Inputs...)
	}

	for _, directiveDefinition := range sg.Schema.Directives {
		directiveDefinition.Extentions = append(directiveDefinition.Extentions, subGraphSchema.Directives...)
	}
}

func NewSubGraph(name string, src []byte, host string) (*SubGraph, error) {
	schema, err := schema.NewParser(schema.NewLexer()).Parse(src)
	if err != nil {
		return nil, err
	}

	return &SubGraph{
		Name:         name,
		Schema:       schema,
		Host:         host,
		IsIntegrated: false,
	}, nil
}
