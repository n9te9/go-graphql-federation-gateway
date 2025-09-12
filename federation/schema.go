package federation

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/n9te9/goliteql/query"
	"github.com/n9te9/goliteql/schema"
)

type SuperGraph struct {
	Schema       *schema.Schema
	SubGraphs    []*SubGraph
	SDL          string
	OwnershipMap map[string]schema.ExtendDefinition
}

type SubGraph struct {
	Name         string
	Schema       *schema.Schema
	SDL          string
	Host         string
	IsIntegrated bool

	ownershipMap    map[string]schema.ExtendDefinition
	uniqueKeyFields map[*schema.TypeDefinition][]string
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

func (sg *SuperGraph) Merge(subGraph *SubGraph) error {
	sg.SDL += "\n" + subGraph.SDL
	newSchema, err := schema.NewParser(schema.NewLexer()).Parse([]byte(sg.SDL))
	if err != nil {
		return err
	}

	sg.Schema = newSchema

	// initialize ownership map
	subGraphOwnershipMap := subGraph.OwnershipMap()
	for k, v := range subGraphOwnershipMap {
		if _, exists := sg.OwnershipMap[k]; exists {
			return fmt.Errorf("ownership conflict for field %s", k)
		}

		sg.OwnershipMap[k] = v
	}

	return nil
}

func (sg *SuperGraph) Execute(ctx context.Context, doc *query.Document, variables map[string]interface{}) {

}

type Plan struct {
	Steps []*Step
}

type Step struct {
	SubGraph  *SubGraph
	DependsOn []*Step

	Status StepStatus
	Err    error
}

type StepStatus int

const (
	Pending StepStatus = iota
	Running
	Completed
	Failed
)

func NewSubGraph(name string, src []byte, host string) (*SubGraph, error) {
	schema, err := schema.NewParser(schema.NewLexer()).Parse(src)
	if err != nil {
		return nil, err
	}

	return &SubGraph{
		Name:            name,
		Schema:          schema,
		Host:            host,
		IsIntegrated:    false,
		SDL:             string(src),
		ownershipMap:    newOwnershipMap(schema),
		uniqueKeyFields: newUniqueKeyFields(schema),
	}, nil
}

func newOwnershipMap(s *schema.Schema) map[string]schema.ExtendDefinition {
	ownershipMap := make(map[string]schema.ExtendDefinition)
	for _, ext := range s.Extends {
		keys := getOwnershipMapKeys(ext)
		for k := range keys {
			ownershipMap[k] = ext
		}
	}
	return ownershipMap
}

func (sg *SubGraph) OwnershipMap() map[string]schema.ExtendDefinition {
	return sg.ownershipMap
}

func getOwnershipMapKeys(ext schema.ExtendDefinition) map[string]struct{} {
	ret := make(map[string]struct{})
	switch e := ext.(type) {
	case *schema.TypeDefinition:
		for _, field := range e.Fields {
			if field.Directives.Get([]byte("external")) == nil {
				key := fmt.Sprintf("%s.%s", e.Name, field.Name)
				ret[key] = struct{}{}
			}
		}
	case *schema.InputDefinition:
		for _, field := range e.Fields {
			if field.Directives.Get([]byte("external")) == nil {
				key := fmt.Sprintf("%s.%s", e.Name, field.Name)
				ret[key] = struct{}{}
			}
		}
	case *schema.EnumDefinition:
		for _, field := range e.Values {
			if field.Directives.Get([]byte("external")) == nil {
				key := fmt.Sprintf("%s.%s", e.Name, field.Name)
				ret[key] = struct{}{}
			}
		}
	case *schema.InterfaceDefinition:
		for _, field := range e.Fields {
			if field.Directives.Get([]byte("external")) == nil {
				key := fmt.Sprintf("%s.%s", e.Name, field.Name)
				ret[key] = struct{}{}
			}
		}
	case *schema.UnionDefinition:
		for _, t := range e.Types {
			key := fmt.Sprintf("%s.%s", e.Name, t)
			ret[key] = struct{}{}
		}
	case *schema.ScalarDefinition:
		key := string(e.Name)
		ret[key] = struct{}{}
	}

	return ret
}

func newUniqueKeyFields(s *schema.Schema) map[*schema.TypeDefinition][]string {
	ret := make(map[*schema.TypeDefinition][]string)
	for _, ext := range s.Extends {
		typeDefinition, ok := ext.(*schema.TypeDefinition)
		if ok {
			ret[typeDefinition] = getObjectUniqueKeyFields(typeDefinition)
		}
	}

	return ret
}

func getObjectUniqueKeyFields(t *schema.TypeDefinition) []string {
	directives := schema.Directives(t.Directives)
	if keyDirective := directives.Get([]byte("key")); keyDirective != nil {
		for _, arg := range keyDirective.Arguments {
			if bytes.Equal(arg.Name, []byte("fields")) {
				v := bytes.Trim(arg.Value, `"`)
				return strings.Split(string(v), " ")
			}
		}
	}

	return []string{}
}
