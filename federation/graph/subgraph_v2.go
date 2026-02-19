package graph

import (
	"fmt"
	"strings"

	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// EntityKey represents the @key directive information of an Entity.
type EntityKey struct {
	FieldSet   string // Field set specified in @key (e.g., "id")
	Resolvable bool   // Resolvable parameter of @key directive
}

// OverrideMetadata represents the @override directive information.
type OverrideMetadata struct {
	From string // The source subgraph name (e.g., "products")
}

// Field represents field information of an Entity.
type Field struct {
	Name        string   // Field name
	Type        ast.Type // Field type
	Requires    []string // Fields specified in @requires directive
	Provides    []string // Fields specified in @provides directive
	isShareable bool     // Whether @shareable directive is present

	// Federation v2 directives
	Override       *OverrideMetadata // @override(from: "products")
	isInaccessible bool              // @inaccessible
	Tags           []string          // @tag(name: "public")
}

// Entity represents an ObjectType with @key directive.
type Entity struct {
	Keys        []EntityKey       // Key information of the Entity
	isExtension bool              // Whether defined as an extension
	Fields      map[string]*Field // Field map with field name as key

	// Federation v2 directives
	isInterfaceObject bool // @interfaceObject
}

// SubGraphV2 represents a subgraph information.
type SubGraphV2 struct {
	Name     string             // Subgraph name (e.g., "product")
	Host     string             // Host (e.g., "product.example.com")
	Schema   *ast.Document      // Schema AST
	entities map[string]*Entity // Entity map with entity name as key

	// Federation v2 directives
	ComposeDirectives []string // @composeDirective directives
}

// NewSubGraphV2 initializes a SubGraphV2 by parsing the schema and extracting entities.
// It analyzes @key, @requires, @provides, @shareable, and @external directives.
func NewSubGraphV2(name string, src []byte, host string) (*SubGraphV2, error) {
	// Parse schema and obtain AST
	l := lexer.New(string(src))
	p := parser.New(l)
	doc := p.ParseDocument()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse error: %v", p.Errors())
	}

	// Initialize SubGraph structure
	sg := &SubGraphV2{
		Name:              name,
		Host:              host,
		Schema:            doc,
		entities:          make(map[string]*Entity),
		ComposeDirectives: extractSchemaComposeDirectives(doc),
	}

	// Traverse all type definitions
	for _, def := range doc.Definitions {
		// Process ObjectTypeDefinition
		if objType, ok := def.(*ast.ObjectTypeDefinition); ok {
			if isEntity(objType.Directives) {
				entity := &Entity{
					Keys:              parseEntityKeys(objType.Directives),
					isExtension:       false,
					Fields:            make(map[string]*Field),
					isInterfaceObject: hasDirective(objType.Directives, "interfaceObject"),
				}

				// Traverse all fields
				for _, field := range objType.Fields {
					entity.Fields[field.Name.String()] = parseField(field)
				}

				sg.entities[objType.Name.String()] = entity
			}
		}

		// Process ObjectTypeExtension
		if objExt, ok := def.(*ast.ObjectTypeExtension); ok {
			if isEntity(objExt.Directives) {
				entity := &Entity{
					Keys:              parseEntityKeys(objExt.Directives),
					isExtension:       true,
					Fields:            make(map[string]*Field),
					isInterfaceObject: hasDirective(objExt.Directives, "interfaceObject"),
				}

				// Traverse all fields
				for _, field := range objExt.Fields {
					entity.Fields[field.Name.String()] = parseField(field)
				}

				sg.entities[objExt.Name.String()] = entity
			}
		}
	}

	return sg, nil
}

// GetEntities returns the entities map.
func (sg *SubGraphV2) GetEntities() map[string]*Entity {
	return sg.entities
}

// GetEntity returns the Entity with the specified name.
func (sg *SubGraphV2) GetEntity(name string) (*Entity, bool) {
	entity, ok := sg.entities[name]
	return entity, ok
}

// isEntity checks if @key directive exists.
func isEntity(directives []*ast.Directive) bool {
	for _, d := range directives {
		if d.Name == "key" {
			return true
		}
	}
	return false
}

// parseEntityKeys parses EntityKey list from @key directives.
func parseEntityKeys(directives []*ast.Directive) []EntityKey {
	var keys []EntityKey

	for _, d := range directives {
		if d.Name == "key" {
			key := EntityKey{
				Resolvable: true, // Default is true
			}

			// Parse arguments
			for _, arg := range d.Arguments {
				switch arg.Name.String() {
				case "fields":
					// Get fields value (remove quotes)
					fieldSet := strings.Trim(arg.Value.String(), "\"")
					key.FieldSet = fieldSet
				case "resolvable":
					// Get resolvable value
					if arg.Value.String() == "false" {
						key.Resolvable = false
					}
				}
			}

			keys = append(keys, key)
		}
	}

	return keys
}

// parseField creates a Field structure from field definition.
func parseField(field *ast.FieldDefinition) *Field {
	f := &Field{
		Name:           field.Name.String(),
		Type:           field.Type,
		Requires:       []string{},
		Provides:       []string{},
		isShareable:    false,
		isInaccessible: false,
		Tags:           []string{},
	}

	// Parse directives
	for _, d := range field.Directives {
		switch d.Name {
		case "requires":
			// Parse fields argument of @requires directive
			if len(d.Arguments) > 0 {
				fieldsVal := strings.Trim(d.Arguments[0].Value.String(), "\"")
				f.Requires = strings.Fields(fieldsVal)
			}
		case "provides":
			// Parse fields argument of @provides directive
			if len(d.Arguments) > 0 {
				fieldsVal := strings.Trim(d.Arguments[0].Value.String(), "\"")
				f.Provides = strings.Fields(fieldsVal)
			}
		case "shareable":
			f.isShareable = true
		case "override":
			// Parse from argument of @override directive
			for _, arg := range d.Arguments {
				if arg.Name.String() == "from" {
					from := strings.Trim(arg.Value.String(), "\"")
					f.Override = &OverrideMetadata{From: from}
				}
			}
		case "inaccessible":
			f.isInaccessible = true
		case "tag":
			// Parse name argument of @tag directive
			for _, arg := range d.Arguments {
				if arg.Name.String() == "name" {
					tagName := strings.Trim(arg.Value.String(), "\"")
					f.Tags = append(f.Tags, tagName)
				}
			}
		}
	}

	return f
}

// IsShareable returns whether the field has @shareable directive.
func (f *Field) IsShareable() bool {
	return f.isShareable
}

// IsExtension returns whether the Entity is defined as an extension.
func (e *Entity) IsExtension() bool {
	return e.isExtension
}

// IsResolvable returns whether the Entity has at least one resolvable key.
// If all keys have resolvable: false, this returns false.
func (e *Entity) IsResolvable() bool {
	for _, key := range e.Keys {
		if key.Resolvable {
			return true
		}
	}
	return false
}

// IsInterfaceObject returns whether the Entity has @interfaceObject directive.
func (e *Entity) IsInterfaceObject() bool {
	return e.isInterfaceObject
}

// IsInaccessible returns whether the field has @inaccessible directive.
func (f *Field) IsInaccessible() bool {
	return f.isInaccessible
}

// GetTags returns the tags of the field.
func (f *Field) GetTags() []string {
	return f.Tags
}

// GetOverride returns the override metadata of the field.
func (f *Field) GetOverride() *OverrideMetadata {
	return f.Override
}

// extractSchemaComposeDirectives extracts @composeDirective from schema definition.
func extractSchemaComposeDirectives(doc *ast.Document) []string {
	var directives []string
	for _, def := range doc.Definitions {
		if schemaDef, ok := def.(*ast.SchemaDefinition); ok {
			for _, d := range schemaDef.Directives {
				if d.Name == "composeDirective" {
					for _, arg := range d.Arguments {
						if arg.Name.String() == "name" {
							name := strings.Trim(arg.Value.String(), "\"")
							directives = append(directives, name)
						}
					}
				}
			}
		}
	}
	return directives
}

// GetComposeDirectives returns the compose directives of the subgraph.
func (sg *SubGraphV2) GetComposeDirectives() []string {
	return sg.ComposeDirectives
}
