package graph

import (
	"fmt"

	"github.com/n9te9/graphql-parser/ast"
)

// SuperGraphV2 represents an aggregated super graph composed of multiple subgraphs.
type SuperGraphV2 struct {
	SubGraphs []*SubGraphV2            // List of subgraphs
	Schema    *ast.Document            // Composed schema
	Ownership map[string][]*SubGraphV2 // Field ownership map (e.g., "Product.id" -> [SubGraph])
}

// NewSuperGraphV2 creates a super graph from a list of SubGraphV2 instances.
func NewSuperGraphV2(subGraphs []*SubGraphV2) (*SuperGraphV2, error) {
	sg := &SuperGraphV2{
		SubGraphs: subGraphs,
		Ownership: make(map[string][]*SubGraphV2),
	}

	// Schema Composition - compose schemas from all subgraphs
	if err := sg.composeSchema(); err != nil {
		return nil, err
	}

	// Build ownership map
	if err := sg.buildOwnershipMap(); err != nil {
		return nil, err
	}

	return sg, nil
}

// composeSchema composes schemas from all subgraphs.
func (sg *SuperGraphV2) composeSchema() error {
	if len(sg.SubGraphs) == 0 {
		return fmt.Errorf("no subgraphs to compose")
	}

	// Initialize schema
	sg.Schema = &ast.Document{
		Definitions: make([]ast.Definition, 0),
	}

	// Merge schemas from all subgraphs (using deep copy)
	for _, subGraph := range sg.SubGraphs {
		sg.mergeSchemaDeep(subGraph.Schema)
	}

	return nil
}

// mergeSchemaDeep merges a new schema into the existing schema using deep copy.
func (sg *SuperGraphV2) mergeSchemaDeep(newSchema *ast.Document) {
	for _, newDef := range newSchema.Definitions {
		switch newTypeDef := newDef.(type) {
		case *ast.ObjectTypeDefinition:
			sg.mergeObjectTypeDefinitionDeep(newTypeDef)
		case *ast.ObjectTypeExtension:
			sg.mergeObjectTypeExtensionDeep(newTypeDef)
		case *ast.InterfaceTypeDefinition:
			sg.mergeInterfaceTypeDefinition(newTypeDef)
		case *ast.InputObjectTypeDefinition:
			sg.mergeInputObjectTypeDefinition(newTypeDef)
		case *ast.EnumTypeDefinition:
			sg.mergeEnumTypeDefinition(newTypeDef)
		case *ast.ScalarTypeDefinition:
			sg.mergeScalarTypeDefinition(newTypeDef)
		case *ast.UnionTypeDefinition:
			sg.mergeUnionTypeDefinition(newTypeDef)
		case *ast.DirectiveDefinition:
			sg.mergeDirectiveDefinition(newTypeDef)
		}
	}
}

// mergeObjectTypeDefinitionDeep merges an ObjectTypeDefinition using deep copy.
func (sg *SuperGraphV2) mergeObjectTypeDefinitionDeep(newDef *ast.ObjectTypeDefinition) {
	// Find existing definition
	var existingDef *ast.ObjectTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if objDef, ok := def.(*ast.ObjectTypeDefinition); ok {
			if objDef.Name.String() == newDef.Name.String() {
				existingDef = objDef
				break
			}
		}
	}

	if existingDef != nil {
		// Copy and merge fields (avoid duplicates)
		newFields := copyFields(newDef.Fields)
		existingDef.Fields = mergeFields(existingDef.Fields, newFields)
		// Also copy directives
		existingDef.Directives = append(existingDef.Directives, copyDirectives(newDef.Directives)...)
	} else {
		// Create a new definition (with copied fields)
		copiedDef := &ast.ObjectTypeDefinition{
			Name:       newDef.Name,
			Interfaces: newDef.Interfaces,
			Fields:     copyFields(newDef.Fields),
			Directives: copyDirectives(newDef.Directives),
		}
		sg.Schema.Definitions = append(sg.Schema.Definitions, copiedDef)
	}
}

// mergeObjectTypeExtensionDeep merges an ObjectTypeExtension into an ObjectTypeDefinition using deep copy.
func (sg *SuperGraphV2) mergeObjectTypeExtensionDeep(newExt *ast.ObjectTypeExtension) {
	// Find the corresponding ObjectTypeDefinition
	var existingDef *ast.ObjectTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if objDef, ok := def.(*ast.ObjectTypeDefinition); ok {
			if objDef.Name.String() == newExt.Name.String() {
				existingDef = objDef
				break
			}
		}
	}

	if existingDef != nil {
		// Copy and merge fields (avoid duplicates)
		newFields := copyFields(newExt.Fields)
		existingDef.Fields = mergeFields(existingDef.Fields, newFields)
		// Also copy directives
		existingDef.Directives = append(existingDef.Directives, copyDirectives(newExt.Directives)...)
	}
}

// copyFields creates a deep copy of a field definition list.
func copyFields(fields []*ast.FieldDefinition) []*ast.FieldDefinition {
	if fields == nil {
		return nil
	}
	copied := make([]*ast.FieldDefinition, len(fields))
	for i, field := range fields {
		copied[i] = &ast.FieldDefinition{
			Name:       field.Name,
			Arguments:  field.Arguments, // TODO: Implement deep copy if needed
			Type:       field.Type,
			Directives: copyDirectives(field.Directives),
		}
	}
	return copied
}

// copyDirectives creates a deep copy of a directive list.
func copyDirectives(directives []*ast.Directive) []*ast.Directive {
	if directives == nil {
		return nil
	}
	copied := make([]*ast.Directive, len(directives))
	for i, dir := range directives {
		copied[i] = &ast.Directive{
			Name:      dir.Name,
			Arguments: dir.Arguments, // TODO: Implement deep copy if needed
		}
	}
	return copied
}

// mergeFields merges field lists and removes duplicates.
func mergeFields(existing, new []*ast.FieldDefinition) []*ast.FieldDefinition {
	fieldMap := make(map[string]*ast.FieldDefinition)

	// Add existing fields to the map
	for _, field := range existing {
		fieldMap[field.Name.String()] = field
	}

	// Add new fields if they don't already exist
	for _, field := range new {
		if _, exists := fieldMap[field.Name.String()]; !exists {
			fieldMap[field.Name.String()] = field
		}
	}

	// Convert map back to slice
	result := make([]*ast.FieldDefinition, 0, len(fieldMap))
	for _, field := range fieldMap {
		result = append(result, field)
	}

	return result
}

// mergeInterfaceTypeDefinition merges an InterfaceTypeDefinition.
func (sg *SuperGraphV2) mergeInterfaceTypeDefinition(newDef *ast.InterfaceTypeDefinition) {
	var existingDef *ast.InterfaceTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if intDef, ok := def.(*ast.InterfaceTypeDefinition); ok {
			if intDef.Name.String() == newDef.Name.String() {
				existingDef = intDef
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
}

// mergeInputObjectTypeDefinition merges an InputObjectTypeDefinition.
func (sg *SuperGraphV2) mergeInputObjectTypeDefinition(newDef *ast.InputObjectTypeDefinition) {
	var existingDef *ast.InputObjectTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if inputDef, ok := def.(*ast.InputObjectTypeDefinition); ok {
			if inputDef.Name.String() == newDef.Name.String() {
				existingDef = inputDef
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
}

// mergeEnumTypeDefinition merges an EnumTypeDefinition.
func (sg *SuperGraphV2) mergeEnumTypeDefinition(newDef *ast.EnumTypeDefinition) {
	var existingDef *ast.EnumTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if enumDef, ok := def.(*ast.EnumTypeDefinition); ok {
			if enumDef.Name.String() == newDef.Name.String() {
				existingDef = enumDef
				break
			}
		}
	}

	if existingDef != nil {
		existingDef.Values = append(existingDef.Values, newDef.Values...)
		existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
	} else {
		sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
	}
}

// mergeScalarTypeDefinition merges a ScalarTypeDefinition.
func (sg *SuperGraphV2) mergeScalarTypeDefinition(newDef *ast.ScalarTypeDefinition) {
	var existingDef *ast.ScalarTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if scalarDef, ok := def.(*ast.ScalarTypeDefinition); ok {
			if scalarDef.Name.String() == newDef.Name.String() {
				existingDef = scalarDef
				break
			}
		}
	}

	if existingDef == nil {
		sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
	}
}

// mergeUnionTypeDefinition merges a UnionTypeDefinition.
func (sg *SuperGraphV2) mergeUnionTypeDefinition(newDef *ast.UnionTypeDefinition) {
	var existingDef *ast.UnionTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if unionDef, ok := def.(*ast.UnionTypeDefinition); ok {
			if unionDef.Name.String() == newDef.Name.String() {
				existingDef = unionDef
				break
			}
		}
	}

	if existingDef != nil {
		existingDef.Types = append(existingDef.Types, newDef.Types...)
		existingDef.Directives = append(existingDef.Directives, newDef.Directives...)
	} else {
		sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
	}
}

// mergeDirectiveDefinition merges a DirectiveDefinition.
func (sg *SuperGraphV2) mergeDirectiveDefinition(newDef *ast.DirectiveDefinition) {
	var existingDef *ast.DirectiveDefinition
	for _, def := range sg.Schema.Definitions {
		if dirDef, ok := def.(*ast.DirectiveDefinition); ok {
			if dirDef.Name.String() == newDef.Name.String() {
				existingDef = dirDef
				break
			}
		}
	}

	if existingDef == nil {
		sg.Schema.Definitions = append(sg.Schema.Definitions, newDef)
	}
}

// buildOwnershipMap constructs the ownership map.
// It determines which subgraphs can resolve each field in the composed schema.
func (sg *SuperGraphV2) buildOwnershipMap() error {
	// Traverse all type definitions in the composed schema
	for _, def := range sg.Schema.Definitions {
		objDef, ok := def.(*ast.ObjectTypeDefinition)
		if !ok {
			continue
		}

		typeName := objDef.Name.String()

		// Traverse all fields of the type
		for _, field := range objDef.Fields {
			fieldName := field.Name.String()
			key := fmt.Sprintf("%s.%s", typeName, fieldName)

			// Check for @override directive
			var overrideFrom string
			var overrideSubGraph *SubGraphV2

			for _, subGraph := range sg.SubGraphs {
				if entity, exists := subGraph.GetEntity(typeName); exists {
					if entityField, ok := entity.Fields[fieldName]; ok {
						if override := entityField.GetOverride(); override != nil {
							overrideFrom = override.From
							overrideSubGraph = subGraph
							break
						}
					}
				}
			}

			// Traverse all subgraphs to find those that can resolve this field
			for _, subGraph := range sg.SubGraphs {
				// Skip the original owner if @override is present
				if overrideFrom != "" && subGraph.Name == overrideFrom {
					continue
				}

				if sg.canResolveField(subGraph, typeName, fieldName) {
					sg.Ownership[key] = append(sg.Ownership[key], subGraph)
				}
			}

			// Ensure the override subgraph is in the ownership list
			if overrideSubGraph != nil {
				found := false
				for _, owner := range sg.Ownership[key] {
					if owner.Name == overrideSubGraph.Name {
						found = true
						break
					}
				}
				if !found {
					sg.Ownership[key] = append(sg.Ownership[key], overrideSubGraph)
				}
			}
		}
	}

	return nil
}

// canResolveField checks if the specified subgraph can resolve the specified field.
// It returns false if the field has an @external directive.
func (sg *SuperGraphV2) canResolveField(subGraph *SubGraphV2, typeName, fieldName string) bool {
	foundType := false
	// Search for the corresponding type in the subgraph's schema
	for _, def := range subGraph.Schema.Definitions {
		// Check ObjectTypeDefinition
		if objDef, ok := def.(*ast.ObjectTypeDefinition); ok {
			if objDef.Name.String() == typeName {
				foundType = true
				for _, field := range objDef.Fields {
					if field.Name.String() == fieldName {
						// Cannot resolve if @external directive exists
						if hasDirective(field.Directives, "external") {
							return false
						}
						return true
					}
				}
				// Cannot resolve if field not found
				return false
			}
		}
	}

	// If ObjectTypeDefinition not found, check ObjectTypeExtension
	if !foundType {
		for _, def := range subGraph.Schema.Definitions {
			if objExt, ok := def.(*ast.ObjectTypeExtension); ok {
				if objExt.Name.String() == typeName {
					for _, field := range objExt.Fields {
						if field.Name.String() == fieldName {
							// Cannot resolve if @external directive exists
							if hasDirective(field.Directives, "external") {
								return false
							}
							return true
						}
					}
					// Cannot resolve if field not found
					return false
				}
			}
		}
	}

	return false
}

// hasDirective checks if a directive with the specified name exists.
func hasDirective(directives []*ast.Directive, name string) bool {
	for _, d := range directives {
		if d.Name == name {
			return true
		}
	}
	return false
}

// GetSubGraphsForField returns the list of subgraphs that can resolve the specified field.
func (sg *SuperGraphV2) GetSubGraphsForField(typeName, fieldName string) []*SubGraphV2 {
	key := fmt.Sprintf("%s.%s", typeName, fieldName)
	return sg.Ownership[key]
}

// GetEntityOwnerSubGraph returns the subgraph that owns the entity (defines it with @key directive, not extends it).
// Filters out subgraphs with @key(resolvable: false) - these are stubs that cannot resolve entities.
// For entities defined in multiple resolvable subgraphs, it returns the first non-extension.
// Returns nil if the type is not an entity or has no resolvable owners.
func (sg *SuperGraphV2) GetEntityOwnerSubGraph(typeName string) *SubGraphV2 {
	// First pass: look for non-extension definitions with resolvable keys
	for _, subGraph := range sg.SubGraphs {
		if entity, exists := subGraph.GetEntity(typeName); exists && !entity.IsExtension() && entity.IsResolvable() {
			return subGraph
		}
	}

	// Second pass: if only extensions exist, return the first resolvable one
	for _, subGraph := range sg.SubGraphs {
		if entity, exists := subGraph.GetEntity(typeName); exists && entity.IsResolvable() {
			return subGraph
		}
	}

	return nil
}

// IsEntityType checks if a type is an entity (has @key directive in any subgraph).
func (sg *SuperGraphV2) IsEntityType(typeName string) bool {
	return sg.GetEntityOwnerSubGraph(typeName) != nil
}

// GetFieldOwnerSubGraph returns the subgraph that owns a specific field.
// It considers @override directives to determine the correct owner.
// Returns the first subgraph in the ownership list, or nil if none found.
func (sg *SuperGraphV2) GetFieldOwnerSubGraph(typeName, fieldName string) *SubGraphV2 {
	key := fmt.Sprintf("%s.%s", typeName, fieldName)
	owners := sg.Ownership[key]
	if len(owners) > 0 {
		return owners[0]
	}
	return nil
}
