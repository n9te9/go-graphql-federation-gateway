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

	OwnershipTypes    map[string]struct{}
	ownershipFieldMap map[string]*ownership
	requiredFields    map[string]map[string]struct{}
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
		requiredFields:    newRequiredFields(schema),
	}, nil
}

func (sg *SubGraph) RequiredFields() map[string]map[string]struct{} {
	return sg.requiredFields
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

	for _, typ := range s.Types {
		for _, f := range typ.Fields {
			key := fmt.Sprintf("%s.%s", typ.Name, f.Name)
			_, exists := ownershipMap[key]
			d := f.Directives.Get([]byte("external"))

			if !exists && d == nil {
				ownershipMap[key] = &ownership{Source: typ}
			}
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

func newRequiredFields(s *schema.Schema) map[string]map[string]struct{} {
	ret := make(map[string]map[string]struct{})
	for _, ext := range s.Extends {
		typeDefinition, ok := ext.(*schema.TypeDefinition)
		if ok {
			requiredFields := make(map[string]struct{})
			for _, field := range typeDefinition.Fields {
				directives := schema.Directives(field.Directives)
				if nonNullDirective := directives.Get([]byte("requires")); nonNullDirective != nil {
					for _, arg := range nonNullDirective.Arguments {
						v := bytes.Trim(arg.Value, `"`)
						fields := strings.Split(string(v), " ")
						for _, field := range fields {
							requiredFields[field] = struct{}{}
						}
					}
				}
			}
			if len(requiredFields) > 0 {
				ret[string(typeDefinition.Name)] = requiredFields
			}
		}
	}

	for _, typ := range s.Types {
		requiredFields := make(map[string]struct{})
		for _, field := range typ.Fields {
			directives := schema.Directives(field.Directives)
			if nonNullDirective := directives.Get([]byte("requires")); nonNullDirective != nil {
				for _, arg := range nonNullDirective.Arguments {
					v := bytes.Trim(arg.Value, `"`)
					fields := strings.Split(string(v), " ")
					for _, field := range fields {
						requiredFields[field] = struct{}{}
					}
				}
			}
		}
		if len(requiredFields) > 0 {
			ret[string(typ.Name)] = requiredFields
		}
	}

	return ret
}
