package planner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/token"
)

// StepType indicates the type of a step.
type StepType int

const (
	// StepTypeQuery represents a step that resolves root fields of a query.
	StepTypeQuery StepType = iota
	// StepTypeEntity represents a step that resolves fields of an entity.
	StepTypeEntity
)

// StepV2 represents a unit of request to a service.
type StepV2 struct {
	ID            int               // Step ID
	SubGraph      *graph.SubGraphV2 // Subgraph responsible for this step
	StepType      StepType          // Type of the step
	ParentType    string            // Parent type name
	SelectionSet  []ast.Selection   // Selected fields
	Path          []string          // Path to the field
	DependsOn     []int             // List of dependent step IDs
	InsertionPath []string          // Path to insert results (for entity resolution)
}

// PlanV2 represents a query execution plan.
type PlanV2 struct {
	Steps            []*StepV2     // List of execution steps
	RootStepIndexes  []int         // Indexes of root steps
	OriginalDocument *ast.Document // Original query document
	OperationType    string        // Operation type (query, mutation, subscription)
}

// PlannerV2 generates query execution plans.
type PlannerV2 struct {
	SuperGraph *graph.SuperGraphV2 // Super graph
}

// NewPlannerV2 creates a new PlannerV2 instance.
func NewPlannerV2(superGraph *graph.SuperGraphV2) *PlannerV2 {
	return &PlannerV2{
		SuperGraph: superGraph,
	}
}

// Plan generates an execution plan from a query document.
// Following V1's walkRoot/walkResolver pattern: builds new SelectionSets instead of modifying AST.
func (p *PlannerV2) Plan(doc *ast.Document, variables map[string]any) (*PlanV2, error) {
	// Get the operation
	op := p.getOperation(doc)
	if op == nil {
		return nil, errors.New("no operation found")
	}
	if len(op.SelectionSet) == 0 {
		return nil, errors.New("empty selection")
	}

	// Collect fragment definitions from the document
	fragmentDefs := p.collectFragmentDefinitions(doc)

	// Determine root type name
	rootTypeName, err := p.getRootTypeName(op)
	if err != nil {
		return nil, err
	}

	// Initialize plan
	plan := &PlanV2{
		Steps:            make([]*StepV2, 0),
		RootStepIndexes:  make([]int, 0),
		OriginalDocument: doc,
		OperationType:    string(op.Operation),
	}

	// Step ID counter
	nextStepID := 0

	// Expand fragments in the root SelectionSet
	expandedSelections := p.expandFragmentsInSelections(op.SelectionSet, fragmentDefs)

	// Group root fields by responsible subgraph
	rootFieldsBySubGraph := make(map[*graph.SubGraphV2][]ast.Selection)

	for _, selection := range expandedSelections {
		field, ok := selection.(*ast.Field)
		if !ok {
			continue
		}

		fieldName := field.Name.String()

		// Skip meta fields like __typename, __schema, __type
		if fieldName == "__typename" || fieldName == "__schema" || fieldName == "__type" {
			continue
		}

		// Get responsible subgraph from ownership map
		subGraphs := p.SuperGraph.GetSubGraphsForField(rootTypeName, fieldName)
		if len(subGraphs) == 0 {
			return nil, fmt.Errorf("no subgraph found for field %s.%s", rootTypeName, fieldName)
		}

		// Use the first subgraph (for @shareable fields there may be multiple, but use the first one for now)
		subGraph := subGraphs[0]
		rootFieldsBySubGraph[subGraph] = append(rootFieldsBySubGraph[subGraph], selection)
	}

	// Create root steps with filtered SelectionSets
	for subGraph, selections := range rootFieldsBySubGraph {
		// Build SelectionSet containing only fields owned by this subgraph
		filteredSelections := p.buildStepSelections(selections, subGraph, rootTypeName, fragmentDefs)

		step := &StepV2{
			ID:           nextStepID,
			SubGraph:     subGraph,
			StepType:     StepTypeQuery,
			ParentType:   rootTypeName,
			SelectionSet: filteredSelections,
			Path:         []string{rootTypeName},
			DependsOn:    []int{},
		}

		plan.Steps = append(plan.Steps, step)
		plan.RootStepIndexes = append(plan.RootStepIndexes, nextStepID)
		nextStepID++
	}

	// Find and create entity steps for boundary fields
	// Process each root step to find boundary fields
	// Key fields will be injected during entity step creation in findAndBuildEntitySteps()
	for _, rootStepIdx := range plan.RootStepIndexes {
		rootStep := plan.Steps[rootStepIdx]

		// Find boundary fields in the original selections (not filtered)
		originalSelections := rootFieldsBySubGraph[rootStep.SubGraph]
		p.findAndBuildEntitySteps(originalSelections, rootStep, plan, &nextStepID, rootStep.ParentType, rootStep.Path, fragmentDefs)
	}

	return plan, nil
}

// collectFragmentDefinitions extracts all fragment definitions from the document
func (p *PlannerV2) collectFragmentDefinitions(doc *ast.Document) map[string]*ast.FragmentDefinition {
	fragments := make(map[string]*ast.FragmentDefinition)
	for _, def := range doc.Definitions {
		if fragDef, ok := def.(*ast.FragmentDefinition); ok {
			fragments[fragDef.Name.String()] = fragDef
		}
	}
	return fragments
}

// expandFragmentsInSelections expands all fragment spreads and inline fragments in selections
func (p *PlannerV2) expandFragmentsInSelections(selections []ast.Selection, fragmentDefs map[string]*ast.FragmentDefinition) []ast.Selection {
	result := make([]ast.Selection, 0)

	for _, selection := range selections {
		switch sel := selection.(type) {
		case *ast.Field:
			// For fields, recursively expand child selections
			if len(sel.SelectionSet) > 0 {
				newField := &ast.Field{
					Alias:      sel.Alias,
					Name:       sel.Name,
					Arguments:  sel.Arguments,
					Directives: sel.Directives,
				}
				newField.SelectionSet = p.expandFragmentsInSelections(sel.SelectionSet, fragmentDefs)
				result = append(result, newField)
			} else {
				result = append(result, sel)
			}

		case *ast.InlineFragment:
			// Expand inline fragment - just inline its selections
			expandedSelections := p.expandFragmentsInSelections(sel.SelectionSet, fragmentDefs)
			result = append(result, expandedSelections...)

		case *ast.FragmentSpread:
			// Expand fragment spread by looking up the fragment definition
			fragName := sel.Name.String()
			fragDef, ok := fragmentDefs[fragName]
			if !ok {
				// Fragment not found, skip it
				continue
			}

			// Recursively expand the fragment's selections
			expandedSelections := p.expandFragmentsInSelections(fragDef.SelectionSet, fragmentDefs)
			result = append(result, expandedSelections...)

		default:
			// Unknown selection type, include as-is
			result = append(result, sel)
		}
	}

	return result
}

// buildStepSelections builds a new SelectionSet containing only fields owned by the given subgraph.
// This follows V1's walkRoot pattern: builds new selections instead of modifying existing ones.
func (p *PlannerV2) buildStepSelections(selections []ast.Selection, subGraph *graph.SubGraphV2, parentType string, fragmentDefs map[string]*ast.FragmentDefinition) []ast.Selection {
	result := make([]ast.Selection, 0)
	hasTypename := false

	for _, selection := range selections {
		switch sel := selection.(type) {
		case *ast.Field:
			fieldName := sel.Name.String()

			// Track if __typename is explicitly requested
			if fieldName == "__typename" {
				hasTypename = true
				newField := &ast.Field{
					Name: &ast.Name{
						Token: token.Token{Type: token.IDENT, Literal: "__typename"},
						Value: "__typename",
					},
				}
				result = append(result, newField)
				continue
			}

			// Check if this field is owned by the current subgraph
			subGraphs := p.SuperGraph.GetSubGraphsForField(parentType, fieldName)
			if len(subGraphs) == 0 || subGraphs[0].Name != subGraph.Name {
				// Not owned by this subgraph, skip it
				continue
			}

			// Get field type to process child selections
			fieldType, err := p.getFieldTypeName(parentType, fieldName)
			if err != nil {
				// If we can't determine the type, include the field without processing children
				fieldType = ""
			}

			// Build new field with filtered child selections
			newField := &ast.Field{
				Alias:      sel.Alias,
				Name:       sel.Name,
				Arguments:  sel.Arguments,
				Directives: sel.Directives,
			}

			// Recursively process child selections
			if len(sel.SelectionSet) > 0 && fieldType != "" {
				childSelections := p.buildStepSelections(sel.SelectionSet, subGraph, fieldType, fragmentDefs)

				// If no child selections were included but original had children, add __typename
				if len(childSelections) == 0 {
					childSelections = append(childSelections, &ast.Field{
						Name: &ast.Name{
							Token: token.Token{Type: token.IDENT, Literal: "__typename"},
							Value: "__typename",
						},
					})
				}

				newField.SelectionSet = childSelections
			}

			result = append(result, newField)

		case *ast.InlineFragment:
			// Expand inline fragment selections
			typeCondition := sel.TypeCondition.Name.String()
			expandedSelections := p.buildStepSelections(sel.SelectionSet, subGraph, typeCondition, fragmentDefs)
			result = append(result, expandedSelections...)

		case *ast.FragmentSpread:
			// Expand fragment spread by looking up the fragment definition
			fragName := sel.Name.String()
			fragDef, ok := fragmentDefs[fragName]
			if !ok {
				// Fragment not found, skip it
				continue
			}

			// Extract selections from the fragment definition
			typeCondition := fragDef.TypeCondition.Name.String()
			expandedSelections := p.buildStepSelections(fragDef.SelectionSet, subGraph, typeCondition, fragmentDefs)
			result = append(result, expandedSelections...)
		}
	}

	// Auto-inject __typename if not explicitly requested
	// This is needed for entity key field extraction
	// But skip for root operation types (Query, Mutation, Subscription)
	isRootType := parentType == "Query" || parentType == "Mutation" || parentType == "Subscription"
	if !hasTypename && !isRootType && len(result) > 0 {
		typenameField := &ast.Field{
			Name: &ast.Name{
				Token: token.Token{Type: token.IDENT, Literal: "__typename"},
				Value: "__typename",
			},
		}
		result = append([]ast.Selection{typenameField}, result...)
	}

	return result
}

// findAndBuildEntitySteps finds boundary fields and creates entity resolution steps.
// This recursively processes the original selections to find fields owned by different subgraphs.
func (p *PlannerV2) findAndBuildEntitySteps(
	selections []ast.Selection,
	parentStep *StepV2,
	plan *PlanV2,
	nextStepID *int,
	parentType string,
	currentPath []string,
	fragmentDefs map[string]*ast.FragmentDefinition,
) {
	entityStepsByKey := make(map[string]*StepV2)

	for _, selection := range selections {
		field, ok := selection.(*ast.Field)
		if !ok {
			continue
		}

		fieldName := field.Name.String()
		if fieldName == "__typename" {
			continue
		}

		// Get field type
		fieldType, err := p.getFieldTypeName(parentType, fieldName)
		if err != nil {
			continue
		}

		// Determine the field identifier (use alias if present, otherwise fieldName)
		fieldIdentifier := fieldName
		if field.Alias != nil && field.Alias.String() != "" {
			fieldIdentifier = field.Alias.String()
		}

		// Build path for this field (use alias for path to support multiple queries with same field)
		fieldPath := append(append([]string{}, currentPath...), fieldIdentifier)

		// Check who owns this field
		subGraphs := p.SuperGraph.GetSubGraphsForField(parentType, fieldName)
		if len(subGraphs) == 0 {
			continue
		}
		fieldSubGraph := subGraphs[0]

		// Check if the field returns an entity type
		// If so, we need to check which subgraph owns that entity (has @key)
		entityOwnerSubGraph := p.SuperGraph.GetEntityOwnerSubGraph(fieldType)

		// Determine if this is a boundary field:
		// 1. Field is owned by a different subgraph, OR
		// 2. Field returns an entity type owned by a different subgraph
		isBoundaryField := false
		targetSubGraph := fieldSubGraph

		if fieldSubGraph.Name != parentStep.SubGraph.Name {
			// Case 1: Field is owned by a different subgraph
			isBoundaryField = true
		} else if entityOwnerSubGraph != nil && entityOwnerSubGraph.Name != parentStep.SubGraph.Name {
			// Case 2: Field returns an entity type owned by a different subgraph
			isBoundaryField = true
			targetSubGraph = entityOwnerSubGraph
		}

		// If this field is owned by the parent step's subgraph, recursively process its children
		if !isBoundaryField {
			// Same subgraph - recursively process children to find nested boundary fields
			if len(field.SelectionSet) > 0 {
				p.findAndBuildEntitySteps(field.SelectionSet, parentStep, plan, nextStepID, fieldType, fieldPath, fragmentDefs)
			}
		} else {
			// Different subgraph - this is a boundary field, create entity step
			// Determine the entity type to resolve:
			// Check if parent type is extended in the target subgraph
			var entityTypeToResolve string
			_, parentIsExtendedInTarget := targetSubGraph.GetEntity(parentType)
			if parentIsExtendedInTarget {
				// Parent type is extended in target subgraph (e.g., Customer extended in accounts service)
				// Resolve the parent entity
				entityTypeToResolve = parentType
			} else {
				// Field returns an entity that's defined in target subgraph (e.g., Review.product → Product)
				// Resolve the field type entity
				entityTypeToResolve = fieldType
			}

			// Check if this is a nested entity (field type owned by same subgraph as target)
			isNestedEntity := (entityOwnerSubGraph != nil && entityOwnerSubGraph.Name == targetSubGraph.Name)
			// The stepKey should identify a unique entity resolution step, based on:
			// - Target subgraph
			// - Entity type
			// - Parent step ID
			// - Insertion path (not including individual child field names)
			// Use currentPath (insertion path) + fieldName (boundary field) as the key
			boundaryFieldPath := append(append([]string{}, currentPath...), fieldName)
			stepKey := fmt.Sprintf("%s:%s:%d:%s", targetSubGraph.Name, entityTypeToResolve, parentStep.ID, strings.Join(boundaryFieldPath, "."))

			existingStep, exists := entityStepsByKey[stepKey]
			if exists {
				// Merge selections into existing step
				existingStep.SelectionSet = p.mergeSelections(existingStep.SelectionSet, []ast.Selection{selection}, targetSubGraph, entityTypeToResolve, fragmentDefs)
			} else {
				// Build selections for this entity step
				var entitySelections []ast.Selection
				var insertionPath []string

				// Two cases:
				// 1. Entity extension (Customer.accounts): include boundary field
				//    _entities([{__typename: "Customer", id: "1"}]) { ... on Customer { accounts { ... } } }
				// 2. Entity reference (Review.product): include only children of boundary field
				//    _entities([{__typename: "Product", id: "..."}]) { ... on Product { name, price } }
				if entityTypeToResolve == parentType {
					// Extension: include the full boundary field
					entitySelections = p.buildEntityStepSelections([]ast.Selection{selection}, targetSubGraph, parentType, parentStep, entityTypeToResolve, fragmentDefs)
					// InsertionPath points to the parent entity (e.g., [Query, customer])
					insertionPath = currentPath
				} else {
					// Reference: include only the children of the boundary field
					entitySelections = p.buildEntityStepSelections(field.SelectionSet, targetSubGraph, entityTypeToResolve, parentStep, entityTypeToResolve, fragmentDefs)
					// InsertionPath includes the boundary field (e.g., [Query, product, reviews, product])
					insertionPath = append(currentPath, fieldName)
				}

				// Create new entity step
				newStep := &StepV2{
					ID:            *nextStepID,
					SubGraph:      targetSubGraph,
					StepType:      StepTypeEntity,
					ParentType:    entityTypeToResolve, // Type from which to extract representation
					SelectionSet:  entitySelections,
					Path:          fieldPath,
					DependsOn:     []int{parentStep.ID},
					InsertionPath: insertionPath,
				}
				plan.Steps = append(plan.Steps, newStep)
				entityStepsByKey[stepKey] = newStep
				*nextStepID++

				// Inject key fields into parent step
				// For the parent step to provide entity representations for the child step,
				// we need to inject key fields for the entity being resolved (entityTypeToResolve)
				// The path should be relative to the parent step's SelectionSet
				// Example: if parentStep is root (InsertionPath=[]), currentPath=[Query, product]
				// Then we need to inject into "product" field → relative path = [product]
				var relativePathForParent []string
				if len(parentStep.InsertionPath) == 0 {
					// Root step: InsertionPath is empty, currentPath starts with Query
					// Remove the "Query" prefix to get the path within the SelectionSet
					if len(currentPath) > 0 && currentPath[0] == "Query" {
						relativePathForParent = currentPath[1:]
					} else {
						relativePathForParent = currentPath
					}
				} else {
					// Non-root step: calculate relative path by removing parent's InsertionPath prefix
					relativePathForParent = currentPath[len(parentStep.InsertionPath):]
				}

				// For nested entity references (not extensions), include the boundary field in the path
				// Example: Review.product (reference) → inject into [reviews, product]
				// But for Customer.accounts (extension) → inject into [customer], not [customer, accounts]
				if isNestedEntity && entityTypeToResolve != parentType {
					relativePathForParent = append(relativePathForParent, fieldName)
				}

				p.injectKeyFieldsIntoParentStep(parentStep, entityTypeToResolve, targetSubGraph, relativePathForParent)

				// Recursively find nested boundary fields within this entity step's selections
				// Important: Use the ORIGINAL field.SelectionSet, not the filtered entitySelections
				// This ensures we can detect boundary fields that belong to other subgraphs
				if len(field.SelectionSet) > 0 {
					// For entity extensions: the nested selections are relative to the parent type
					// For entity references: the nested selections are relative to the entity type
					nestedParentType := entityTypeToResolve
					if entityTypeToResolve == parentType {
						// Extension case: fieldType is the type of the extension field
						nestedParentType = fieldType
					}
					p.findAndBuildEntitySteps(field.SelectionSet, newStep, plan, nextStepID, nestedParentType, fieldPath, fragmentDefs)
				}
			}
		}
	}
}

// buildEntityStepSelections builds SelectionSet for entity resolution steps.
// This follows Strong Planner principle: build complete, correct query structure.
// The selections parameter contains the boundary fields (e.g., reviews field).
// We need to preserve the boundary field structure and filter its children by ownership.
// Parameters:
//   - selections: boundary field selections from the original query
//   - subGraph: target subgraph that will resolve the entity
//   - parentType: type that contains the boundary field (e.g., Product for reviews field)
//   - parentStep: parent step
//   - entityType: entity type to resolve (e.g., Product when resolving _entities for Product)
//   - fragmentDefs: fragment definitions from the query
func (p *PlannerV2) buildEntityStepSelections(
	selections []ast.Selection,
	subGraph *graph.SubGraphV2,
	parentType string,
	parentStep *StepV2,
	entityType string,
	fragmentDefs map[string]*ast.FragmentDefinition,
) []ast.Selection {
	result := make([]ast.Selection, 0)

	// First, inject @key fields for the entity
	keyFields := p.getKeyFields(entityType, subGraph)
	for _, keyField := range keyFields {
		result = append(result, &ast.Field{
			Name: &ast.Name{
				Token: token.Token{Type: token.IDENT, Literal: keyField},
				Value: keyField,
			},
		})
	}

	// Process boundary fields - preserve the field structure with filtered children
	for _, selection := range selections {
		field, ok := selection.(*ast.Field)
		if !ok {
			continue
		}

		fieldName := field.Name.String()
		if fieldName == "__typename" {
			continue
		}

		// Get field return type from the parent type (not entity type)
		// For example: parentType=Product, fieldName=reviews -> fieldType=Review
		fieldType, err := p.getFieldTypeName(parentType, fieldName)
		if err != nil {
			continue
		}

		// Build new field with filtered child selections
		newField := &ast.Field{
			Alias:      field.Alias,
			Name:       field.Name,
			Arguments:  field.Arguments,
			Directives: field.Directives,
		}

		// Filter child selections by ownership for this subgraph
		if len(field.SelectionSet) > 0 {
			filteredChildren := p.buildStepSelections(field.SelectionSet, subGraph, fieldType, fragmentDefs)
			newField.SelectionSet = filteredChildren

			// Only include this field if it has children or if it's a leaf field
			if len(filteredChildren) > 0 {
				result = append(result, newField)
			}
		} else {
			// Leaf field - check if it's owned by this subgraph
			fieldSubGraphs := p.SuperGraph.GetSubGraphsForField(entityType, fieldName)
			if len(fieldSubGraphs) > 0 && fieldSubGraphs[0].Name == subGraph.Name {
				result = append(result, newField)
			}
		}
	}

	return result
}

// mergeSelections merges new selections into existing selections.
func (p *PlannerV2) mergeSelections(existing, newSels []ast.Selection, subGraph *graph.SubGraphV2, parentType string, fragmentDefs map[string]*ast.FragmentDefinition) []ast.Selection {
	// Simple implementation: just append and let buildStepSelections deduplicate later
	merged := append(existing, newSels...)
	return p.buildStepSelections(merged, subGraph, parentType, fragmentDefs)
}

// getKeyFields returns the @key fields for an entity type.
func (p *PlannerV2) getKeyFields(typeName string, subGraph *graph.SubGraphV2) []string {
	entity, exists := subGraph.GetEntity(typeName)
	if !exists || len(entity.Keys) == 0 {
		return []string{"__typename"}
	}

	// Use the first key
	keyFieldSet := entity.Keys[0].FieldSet

	// Handle composite keys by splitting on whitespace
	// Example: "number departureDate" -> ["number", "departureDate"]
	keyFieldNames := strings.Fields(keyFieldSet)

	// Always include __typename first
	result := []string{"__typename"}
	result = append(result, keyFieldNames...)

	return result
}

// injectKeyFieldsIntoParentStep injects @key fields into the parent step's selections
// so that entity resolution can extract representations.
func (p *PlannerV2) injectKeyFieldsIntoParentStep(parentStep *StepV2, entityType string, childSubGraph *graph.SubGraphV2, insertionPath []string) {
	// Get key fields
	keyFields := p.getKeyFields(entityType, childSubGraph)

	// insertionPath is relative to parentStep's SelectionSet
	// Example: [reviews, product] means navigate to reviews field, then product field

	if len(insertionPath) == 0 {
		return // No path to navigate
	}

	// Use ensureAndInjectKeyFields to both create missing fields and inject key fields
	parentStep.SelectionSet = p.ensureAndInjectKeyFields(parentStep.SelectionSet, insertionPath, keyFields)
}

// ensureAndInjectKeyFields recursively ensures fields in the path exist and injects key fields.
// This function both creates missing boundary fields and injects key fields into them.
func (p *PlannerV2) ensureAndInjectKeyFields(selections []ast.Selection, path []string, keyFields []string) []ast.Selection {
	if len(path) == 0 {
		return selections
	}

	targetField := path[0]
	var targetFieldNode *ast.Field

	// Find the target field
	for _, sel := range selections {
		if field, ok := sel.(*ast.Field); ok {
			fieldIdentifier := field.Name.String()
			if field.Alias != nil && field.Alias.String() != "" {
				fieldIdentifier = field.Alias.String()
			}

			if fieldIdentifier == targetField {
				targetFieldNode = field
				break
			}
		}
	}

	// If the field doesn't exist, create it
	if targetFieldNode == nil {
		targetFieldNode = &ast.Field{
			Name: &ast.Name{
				Token: token.Token{Type: token.IDENT, Literal: targetField},
				Value: targetField,
			},
			SelectionSet: make([]ast.Selection, 0),
		}
		selections = append(selections, targetFieldNode)
	}

	if len(path) == 1 {
		// We've reached the boundary field, inject key fields into it
		existingFields := make(map[string]bool)
		for _, childSel := range targetFieldNode.SelectionSet {
			if childField, ok := childSel.(*ast.Field); ok {
				existingFields[childField.Name.String()] = true
			}
		}

		// Add missing key fields
		for _, keyField := range keyFields {
			if !existingFields[keyField] {
				targetFieldNode.SelectionSet = append(targetFieldNode.SelectionSet, &ast.Field{
					Name: &ast.Name{
						Token: token.Token{Type: token.IDENT, Literal: keyField},
						Value: keyField,
					},
				})
			}
		}
	} else {
		// Continue navigating
		targetFieldNode.SelectionSet = p.ensureAndInjectKeyFields(targetFieldNode.SelectionSet, path[1:], keyFields)
	}

	return selections
}

// updateFieldSelectionSet recursively updates a field's SelectionSet.
func (p *PlannerV2) updateFieldSelectionSet(selections []ast.Selection, path []string, newSelectionSet []ast.Selection) {
	if len(path) == 0 {
		return
	}

	targetField := path[0]
	for _, sel := range selections {
		if field, ok := sel.(*ast.Field); ok {
			if field.Name.String() == targetField {
				if len(path) == 1 {
					// This is the target field, update its SelectionSet
					field.SelectionSet = newSelectionSet
					return
				} else {
					// Continue navigating
					p.updateFieldSelectionSet(field.SelectionSet, path[1:], newSelectionSet)
					return
				}
			}
		}
	}
}

// getOperation returns the operation from a document.
func (p *PlannerV2) getOperation(doc *ast.Document) *ast.OperationDefinition {
	for _, def := range doc.Definitions {
		if op, ok := def.(*ast.OperationDefinition); ok {
			return op
		}
	}
	return nil
}

// getRootTypeName returns the root type name from an operation.
func (p *PlannerV2) getRootTypeName(op *ast.OperationDefinition) (string, error) {
	var rootTypeName string

	switch op.Operation {
	case ast.Query:
		rootTypeName = "Query"
	case ast.Mutation:
		rootTypeName = "Mutation"
	case ast.Subscription:
		rootTypeName = "Subscription"
	default:
		return "", fmt.Errorf("unknown operation type: %v", op.Operation)
	}

	// Get actual type name from SchemaDefinition
	for _, def := range p.SuperGraph.Schema.Definitions {
		if sd, ok := def.(*ast.SchemaDefinition); ok {
			for _, ot := range sd.OperationTypes {
				if (ot.Operation == token.QUERY && op.Operation == ast.Query) ||
					(ot.Operation == token.MUTATION && op.Operation == ast.Mutation) ||
					(ot.Operation == token.SUBSCRIPTION && op.Operation == ast.Subscription) {
					rootTypeName = ot.Type.Name.String()
				}
			}
		}
	}

	return rootTypeName, nil
}

// getFieldTypeName returns the type name of a field.
func (p *PlannerV2) getFieldTypeName(parentTypeName, fieldName string) (string, error) {
	if fieldName == "__typename" {
		return "String", nil
	}

	for _, def := range p.SuperGraph.Schema.Definitions {
		if td, ok := def.(*ast.ObjectTypeDefinition); ok {
			if td.Name.String() == parentTypeName {
				for _, field := range td.Fields {
					if field.Name.String() == fieldName {
						return p.getNamedType(field.Type), nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("field %s not found in type %s", fieldName, parentTypeName)
}

// getNamedType returns the named type from a Type.
func (p *PlannerV2) getNamedType(t ast.Type) string {
	switch typ := t.(type) {
	case *ast.NamedType:
		return typ.Name.String()
	case *ast.ListType:
		return p.getNamedType(typ.Type)
	case *ast.NonNullType:
		return p.getNamedType(typ.Type)
	default:
		return ""
	}
}
