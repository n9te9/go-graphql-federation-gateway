package graph

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/n9te9/goliteql/schema"
)

type ownership struct {
	Source schema.ExtendDefinition
}

type SubGraph struct {
	Name   string
	Schema *schema.Schema
	SDL    string
	Host   string

	BaseName string

	OwnershipTypes    map[string]struct{}
	ownershipFieldMap map[string]*ownership
	uniqueKeyFields   map[*schema.TypeDefinition][]string
}

func NewSubGraph(name string, src []byte, host string) (*SubGraph, error) {
	schema, err := schema.NewParser(schema.NewLexer()).Parse(src)
	if err != nil {
		return nil, err
	}

	ownershipTypes := make(map[string]struct{})
	for _, typ := range schema.Types {
		ownershipTypes[string(typ.Name)] = struct{}{}
	}

	return &SubGraph{
		Name:              name,
		Schema:            schema,
		Host:              host,
		SDL:               string(src),
		OwnershipTypes:    ownershipTypes,
		ownershipFieldMap: newOwnershipMap(schema),
		uniqueKeyFields:   newUniqueKeyFields(schema),
	}, nil
}

func NewBaseSubGraph(name string, src []byte, host string) (*SubGraph, error) {
	schema, err := schema.NewParser(schema.NewLexer()).Parse(src)
	if err != nil {
		return nil, err
	}

	return &SubGraph{
		Name:              name,
		Schema:            schema,
		Host:              host,
		SDL:               string(src),
		ownershipFieldMap: newOwnershipMapForSuperGraph(schema),
		uniqueKeyFields:   newUniqueKeyFields(schema),
	}, nil
}

func (s *SubGraph) Run() error {
	return nil
}

func newOwnershipMapForSuperGraph(s *schema.Schema) map[string]*ownership {
	ownershipMap := make(map[string]*ownership)

	for _, typ := range s.Types {
		for _, f := range typ.Fields {
			ownershipMap[fmt.Sprintf("%s.%s", typ.Name, f.Name)] = &ownership{Source: typ}
		}
	}

	return ownershipMap
}

func newOwnershipMap(s *schema.Schema) map[string]*ownership {
	ownershipMap := make(map[string]*ownership)
	for _, ext := range s.Extends {
		keys := getOwnershipMapKeys(ext)
		for k := range keys {
			ownershipMap[k] = &ownership{Source: ext}
		}
	}

	return ownershipMap
}

func (sg *SubGraph) OwnershipFieldMap() map[string]*ownership {
	return sg.ownershipFieldMap
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
