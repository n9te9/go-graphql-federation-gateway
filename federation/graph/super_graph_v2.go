package graph

import (
	"fmt"
	"sort"

	"github.com/n9te9/graphql-parser/ast"
)

// SuperGraphV2 represents an aggregated super graph composed of multiple subgraphs.
type SuperGraphV2 struct {
	SubGraphs            []*SubGraphV2                       // List of subgraphs
	Schema               *ast.Document                       // Composed schema
	Ownership            map[string][]*SubGraphV2            // Field ownership map (e.g., "Product.id" -> [SubGraph])
	Graph                *WeightedDirectedGraph              // Weighted directed graph for Dijkstra-based plan optimization
	DirectiveDefinitions map[string]*ast.DirectiveDefinition // Custom directive definitions merged from @composeDirective

	// Tag metadata
	TypeTags  map[string][]string            // typeName -> merged tags
	FieldTags map[string]map[string][]string // typeName -> fieldName -> merged tags
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

	// Merge and validate custom directive definitions from @composeDirective
	if err := sg.mergeComposeDirectiveDefinitions(); err != nil {
		return nil, err
	}

	// Build ownership map
	if err := sg.buildOwnershipMap(); err != nil {
		return nil, err
	}

	// Build weighted directed graph for Dijkstra-based query plan optimization.
	// This is computed once at startup to avoid per-request overhead.
	sg.Graph = BuildGraph(subGraphs)

	// Build tag metadata from all subgraphs.
	sg.buildTagMetadata()

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

	// Two-pass merge to ensure ObjectTypeExtensions find their base types regardless
	// of subgraph iteration order (Go maps are non-deterministic).
	//
	// Pass 1: merge all non-extension type definitions so every base type exists.
	for _, subGraph := range sg.SubGraphs {
		sg.mergeSchemaDeepPass1(subGraph.Schema)
	}

	// Pass 2: merge all ObjectTypeExtensions now that base types are present.
	for _, subGraph := range sg.SubGraphs {
		sg.mergeSchemaDeepPass2(subGraph.Schema)
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

// mergeSchemaDeepPass1 merges all non-extension type definitions.
// This is the first pass of the two-pass composition strategy.
// Types annotated with @inaccessible are skipped and excluded from the public schema.
func (sg *SuperGraphV2) mergeSchemaDeepPass1(newSchema *ast.Document) {
	for _, newDef := range newSchema.Definitions {
		switch newTypeDef := newDef.(type) {
		case *ast.ObjectTypeDefinition:
			// Skip @inaccessible types - they must not appear in the public schema
			if hasDirective(newTypeDef.Directives, "inaccessible") {
				continue
			}
			sg.mergeObjectTypeDefinitionDeep(newTypeDef)
		case *ast.InterfaceTypeDefinition:
			// Skip @inaccessible interface types
			if hasDirective(newTypeDef.Directives, "inaccessible") {
				continue
			}
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

// mergeSchemaDeepPass2 merges extension type definitions only.
// This is the second pass of the two-pass composition strategy, run after
// all base types have been merged so extensions can always find their target.
// Extensions annotated with @inaccessible are skipped.
func (sg *SuperGraphV2) mergeSchemaDeepPass2(newSchema *ast.Document) {
	for _, newDef := range newSchema.Definitions {
		switch newTypeDef := newDef.(type) {
		case *ast.ObjectTypeExtension:
			// Skip @inaccessible extension types
			if hasDirective(newTypeDef.Directives, "inaccessible") {
				continue
			}
			sg.mergeObjectTypeExtensionDeep(newTypeDef)
		case *ast.InterfaceTypeExtension:
			// Skip @inaccessible interface extensions
			if hasDirective(newTypeDef.Directives, "inaccessible") {
				continue
			}
			sg.mergeInterfaceTypeExtension(newTypeDef)
		}
	}
}

// mergeInterfaceTypeExtension merges an InterfaceTypeExtension into an existing InterfaceTypeDefinition.
func (sg *SuperGraphV2) mergeInterfaceTypeExtension(newExt *ast.InterfaceTypeExtension) {
	var existingDef *ast.InterfaceTypeDefinition
	for _, def := range sg.Schema.Definitions {
		if intDef, ok := def.(*ast.InterfaceTypeDefinition); ok {
			if intDef.Name.String() == newExt.Name.String() {
				existingDef = intDef
				break
			}
		}
	}

	if existingDef != nil {
		mergeIntoDefinition(&existingDef.Fields, &existingDef.Directives, newExt.Fields, newExt.Directives)
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
		mergeIntoDefinition(&existingDef.Fields, &existingDef.Directives, newDef.Fields, newDef.Directives)
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
		mergeIntoDefinition(&existingDef.Fields, &existingDef.Directives, newExt.Fields, newExt.Directives)
	}
}

// mergeIntoDefinition merges fields and directives into an existing type definition's field/directive slices.
// This consolidates the common merge body shared by all mergeXxxTypeYyy functions.
func mergeIntoDefinition(existingFields *[]*ast.FieldDefinition, existingDirectives *[]*ast.Directive, newFields []*ast.FieldDefinition, newDirectives []*ast.Directive) {
	*existingFields = mergeFields(*existingFields, copyFields(newFields))
	*existingDirectives = append(*existingDirectives, copyDirectives(newDirectives)...)
}

// copyFields creates a deep copy of a field definition list, excluding @inaccessible fields.
// @inaccessible fields must not appear in the public supergraph schema.
func copyFields(fields []*ast.FieldDefinition) []*ast.FieldDefinition {
	if fields == nil {
		return nil
	}
	copied := make([]*ast.FieldDefinition, 0, len(fields))
	for _, field := range fields {
		// Skip @inaccessible fields - they must not be exposed in the public schema
		if hasDirective(field.Directives, "inaccessible") {
			continue
		}
		copied = append(copied, &ast.FieldDefinition{
			Name:       field.Name,
			Arguments:  field.Arguments, // TODO: Implement deep copy if needed
			Type:       field.Type,
			Directives: copyDirectives(field.Directives),
		})
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

// mergeInterfaceTypeDefinition merges an InterfaceTypeDefinition using deep copy.
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
		mergeIntoDefinition(&existingDef.Fields, &existingDef.Directives, newDef.Fields, newDef.Directives)
	} else {
		// Deep copy to avoid mutating the source subgraph schema when Pass 2 merges extend fields
		copiedDef := &ast.InterfaceTypeDefinition{
			Name:       newDef.Name,
			Interfaces: newDef.Interfaces,
			Fields:     copyFields(newDef.Fields),
			Directives: copyDirectives(newDef.Directives),
		}
		sg.Schema.Definitions = append(sg.Schema.Definitions, copiedDef)
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

// mergeComposeDirectiveDefinitions validates and merges custom directive definitions
// listed via @composeDirective across all subgraphs into the super graph.
func (sg *SuperGraphV2) mergeComposeDirectiveDefinitions() error {
	sg.DirectiveDefinitions = make(map[string]*ast.DirectiveDefinition)

	for _, subGraph := range sg.SubGraphs {
		for name, directiveDef := range subGraph.DirectiveDefinitions {
			if existing, ok := sg.DirectiveDefinitions[name]; ok {
				// Already seen this directive - validate consistency
				if !isDirectiveDefinitionEqual(existing, directiveDef) {
					return fmt.Errorf(
						"inconsistent directive definition for '@%s' between subgraphs",
						name,
					)
				}
				// Consistent - skip
				continue
			}
			// New directive definition
			sg.DirectiveDefinitions[name] = directiveDef
		}
	}

	return nil
}

// isDirectiveDefinitionEqual checks whether two DirectiveDefinition nodes are equivalent
// by comparing their argument names/types and applicable locations.
func isDirectiveDefinitionEqual(a, b *ast.DirectiveDefinition) bool {
	if a.Name.String() != b.Name.String() {
		return false
	}

	if len(a.Arguments) != len(b.Arguments) {
		return false
	}

	for i := range a.Arguments {
		if a.Arguments[i].Name.String() != b.Arguments[i].Name.String() {
			return false
		}
		if !isTypeEqual(a.Arguments[i].Type, b.Arguments[i].Type) {
			return false
		}
	}

	if len(a.Locations) != len(b.Locations) {
		return false
	}

	locSet := make(map[string]bool)
	for _, loc := range a.Locations {
		locSet[loc.String()] = true
	}
	for _, loc := range b.Locations {
		if !locSet[loc.String()] {
			return false
		}
	}

	return true
}

// isTypeEqual compares two ast.Type values by their string representation.
func isTypeEqual(a, b ast.Type) bool {
	return a.String() == b.String()
}

// buildOwnershipMap constructs the ownership map.
// It determines which subgraphs can resolve each field in the composed schema.
// It handles both ObjectTypeDefinition and InterfaceTypeDefinition (for @interfaceObject entities).
func (sg *SuperGraphV2) buildOwnershipMap() error {
	// Traverse all type definitions in the composed schema
	for _, def := range sg.Schema.Definitions {
		var typeName string
		var fields []*ast.FieldDefinition

		switch typeDef := def.(type) {
		case *ast.ObjectTypeDefinition:
			typeName = typeDef.Name.String()
			fields = typeDef.Fields
		case *ast.InterfaceTypeDefinition:
			typeName = typeDef.Name.String()
			fields = typeDef.Fields
		default:
			continue
		}

		// Traverse all fields of the type
		for _, field := range fields {
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
// Handles ObjectTypeDefinition, ObjectTypeExtension, InterfaceTypeDefinition, and InterfaceTypeExtension.
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
		// Check InterfaceTypeDefinition (for @interfaceObject entities)
		if intfDef, ok := def.(*ast.InterfaceTypeDefinition); ok {
			if intfDef.Name.String() == typeName {
				foundType = true
				for _, field := range intfDef.Fields {
					if field.Name.String() == fieldName {
						if hasDirective(field.Directives, "external") {
							return false
						}
						return true
					}
				}
				return false
			}
		}
	}

	// If definition not found, check extension types
	if !foundType {
		for _, def := range subGraph.Schema.Definitions {
			// Check ObjectTypeExtension
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
			// Check InterfaceTypeExtension (for @interfaceObject entity extensions)
			if intfExt, ok := def.(*ast.InterfaceTypeExtension); ok {
				if intfExt.Name.String() == typeName {
					for _, field := range intfExt.Fields {
						if field.Name.String() == fieldName {
							if hasDirective(field.Directives, "external") {
								return false
							}
							return true
						}
					}
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

// buildTagMetadata collects and merges @tag directives from all subgraphs into TypeTags and FieldTags.
func (sg *SuperGraphV2) buildTagMetadata() {
	sg.TypeTags = make(map[string][]string)
	sg.FieldTags = make(map[string]map[string][]string)

	for _, subGraph := range sg.SubGraphs {
		for typeName, entity := range subGraph.GetEntities() {
			// Merge type-level tags
			if len(entity.Tags) > 0 {
				sg.TypeTags[typeName] = mergeUniqueTags(sg.TypeTags[typeName], entity.Tags)
			}

			// Merge field-level tags
			if sg.FieldTags[typeName] == nil {
				sg.FieldTags[typeName] = make(map[string][]string)
			}
			for fieldName, field := range entity.Fields {
				if len(field.Tags) > 0 {
					sg.FieldTags[typeName][fieldName] = mergeUniqueTags(
						sg.FieldTags[typeName][fieldName],
						field.Tags,
					)
				}
			}
		}
	}
}

// mergeUniqueTags merges two tag slices, deduplicating and sorting for consistency.
func mergeUniqueTags(existing, newTags []string) []string {
	tagSet := make(map[string]bool)
	for _, tag := range existing {
		tagSet[tag] = true
	}
	for _, tag := range newTags {
		tagSet[tag] = true
	}

	result := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

// GetTypeTags returns the merged @tag names for the given type.
func (sg *SuperGraphV2) GetTypeTags(typeName string) []string {
	return sg.TypeTags[typeName]
}

// GetFieldTags returns the merged @tag names for the given field.
func (sg *SuperGraphV2) GetFieldTags(typeName, fieldName string) []string {
	if fieldMap, ok := sg.FieldTags[typeName]; ok {
		return fieldMap[fieldName]
	}
	return nil
}

// HasTypeTag reports whether the given type carries the specified tag.
func (sg *SuperGraphV2) HasTypeTag(typeName, tag string) bool {
	for _, t := range sg.GetTypeTags(typeName) {
		if t == tag {
			return true
		}
	}
	return false
}

// HasFieldTag reports whether the given field carries the specified tag.
func (sg *SuperGraphV2) HasFieldTag(typeName, fieldName, tag string) bool {
	for _, t := range sg.GetFieldTags(typeName, fieldName) {
		if t == tag {
			return true
		}
	}
	return false
}

// IsFieldInaccessible returns true if the given field is marked @inaccessible in any subgraph.
// This can be used to provide descriptive error messages when a client queries a hidden field.
func (sg *SuperGraphV2) IsFieldInaccessible(typeName, fieldName string) bool {
	for _, subGraph := range sg.SubGraphs {
		if entity, ok := subGraph.GetEntity(typeName); ok {
			if field, ok := entity.Fields[fieldName]; ok {
				if field.IsInaccessible() {
					return true
				}
			}
		}
	}
	return false
}
