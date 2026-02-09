package graph

import (
	"fmt"
	"strings"

	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

type ownership struct {
	Source ast.Definition
}

type SubGraph struct {
	Name   string
	Schema *ast.Document
	SDL    string
	Host   string

	OwnershipTypes    map[string]struct{}
	ownershipFieldMap map[string]*ownership
	requiredFields    map[string]map[string]struct{}
}

func NewSubGraph(name string, src []byte, host string) (*SubGraph, error) {
	l := lexer.New(string(src))
	p := parser.New(l)
	doc := p.ParseDocument()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse error: %v", p.Errors())
	}

	ownershipTypes := make(map[string]struct{})
	for _, def := range doc.Definitions {
		if td, ok := def.(*ast.ObjectTypeDefinition); ok {
			ownershipTypes[td.Name.String()] = struct{}{}
		}
	}

	return &SubGraph{
		Name:              name,
		Schema:            doc,
		Host:              host,
		SDL:               string(src),
		OwnershipTypes:    ownershipTypes,
		ownershipFieldMap: newOwnershipMap(doc),
		requiredFields:    newRequiredFields(doc),
	}, nil
}

func (sg *SubGraph) RequiredFields() map[string]map[string]struct{} {
	return sg.requiredFields
}

func newOwnershipMapForSuperGraph(doc *ast.Document) map[string]*ownership {
	ownershipMap := make(map[string]*ownership)

	for _, def := range doc.Definitions {
		if typ, ok := def.(*ast.ObjectTypeDefinition); ok {
			for _, f := range typ.Fields {
				ownershipMap[fmt.Sprintf("%s.%s", typ.Name.String(), f.Name.String())] = &ownership{Source: typ}
			}
		}
	}

	return ownershipMap
}

func newOwnershipMap(doc *ast.Document) map[string]*ownership {
	ownershipMap := make(map[string]*ownership)
	for _, def := range doc.Definitions {
		if ext, ok := def.(*ast.ObjectTypeExtension); ok {
			keys := getOwnershipMapKeys(ext)
			for k := range keys {
				ownershipMap[k] = &ownership{Source: ext}
			}
		}
	}

	for _, def := range doc.Definitions {
		if typ, ok := def.(*ast.ObjectTypeDefinition); ok {
			for _, f := range typ.Fields {
				key := fmt.Sprintf("%s.%s", typ.Name.String(), f.Name.String())
				_, exists := ownershipMap[key]
				d := getDirective(f.Directives, "external")

				if !exists && d == nil {
					ownershipMap[key] = &ownership{Source: typ}
				}
			}
		}
	}

	return ownershipMap
}

func (sg *SubGraph) OwnershipFieldMap() map[string]*ownership {
	return sg.ownershipFieldMap
}

func getDirective(directives []*ast.Directive, name string) *ast.Directive {
	for _, d := range directives {
		if d.Name == name {
			return d
		}
	}
	return nil
}

func getOwnershipMapKeys(def ast.Definition) map[string]struct{} {
	ret := make(map[string]struct{})
	switch e := def.(type) {
	case *ast.ObjectTypeDefinition:
		for _, field := range e.Fields {
			if getDirective(field.Directives, "external") == nil {
				key := fmt.Sprintf("%s.%s", e.Name.String(), field.Name.String())
				ret[key] = struct{}{}
			}
		}
	case *ast.ObjectTypeExtension:
		for _, field := range e.Fields {
			if getDirective(field.Directives, "external") == nil {
				key := fmt.Sprintf("%s.%s", e.Name.String(), field.Name.String())
				ret[key] = struct{}{}
			}
		}
	case *ast.InputObjectTypeDefinition:
		for _, field := range e.Fields {
			key := fmt.Sprintf("%s.%s", e.Name.String(), field.Name.String())
			ret[key] = struct{}{}
		}
	case *ast.EnumTypeDefinition:
		for _, val := range e.Values {
			key := fmt.Sprintf("%s.%s", e.Name.String(), val.Name.String())
			ret[key] = struct{}{}
		}
	case *ast.InterfaceTypeDefinition:
		for _, field := range e.Fields {
			key := fmt.Sprintf("%s.%s", e.Name.String(), field.Name.String())
			ret[key] = struct{}{}
		}
	case *ast.UnionTypeDefinition:
		for _, t := range e.Types {
			key := fmt.Sprintf("%s.%s", e.Name.String(), t.String())
			ret[key] = struct{}{}
		}
	case *ast.ScalarTypeDefinition:
		key := e.Name.String()
		ret[key] = struct{}{}
	}
	return ret
}

func newRequiredFields(doc *ast.Document) map[string]map[string]struct{} {
	ret := make(map[string]map[string]struct{})
	for _, def := range doc.Definitions {
		if typeExtension, ok := def.(*ast.ObjectTypeExtension); ok {
			for _, field := range typeExtension.Fields {
				if d := getDirective(field.Directives, "requires"); d != nil {
					if len(d.Arguments) > 0 {
						// Assuming the first argument is "fields" and it's a string
						fieldsVal := d.Arguments[0].Value.String()
						// Remove quotes if present
						fieldsVal = strings.Trim(fieldsVal, "\"")
						fields := strings.Split(fieldsVal, " ")
						if ret[typeExtension.Name.String()] == nil {
							ret[typeExtension.Name.String()] = make(map[string]struct{})
						}
						for _, f := range fields {
							ret[typeExtension.Name.String()][f] = struct{}{}
						}
					}
				}
			}
		}
		if typ, ok := def.(*ast.ObjectTypeDefinition); ok {
			for _, field := range typ.Fields {
				if d := getDirective(field.Directives, "requires"); d != nil {
					if len(d.Arguments) > 0 {
						fieldsVal := d.Arguments[0].Value.String()
						fieldsVal = strings.Trim(fieldsVal, "\"")
						fields := strings.Split(fieldsVal, " ")
						if ret[typ.Name.String()] == nil {
							ret[typ.Name.String()] = make(map[string]struct{})
						}
						for _, f := range fields {
							ret[typ.Name.String()][f] = struct{}{}
						}
					}
				}
			}
		}
	}
	return ret
}
