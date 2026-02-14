package planner

import (
	"errors"
	"fmt"

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
	Steps           []*StepV2 // List of execution steps
	RootStepIndexes []int     // Indexes of root steps
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
// It implements the BFS algorithm described in the design document.
func (p *PlannerV2) Plan(doc *ast.Document, variables map[string]any) (*PlanV2, error) {
	// Get the operation
	op := p.getOperation(doc)
	if op == nil {
		return nil, errors.New("no operation found")
	}
	if len(op.SelectionSet) == 0 {
		return nil, errors.New("empty selection")
	}

	// Determine root type name
	rootTypeName, err := p.getRootTypeName(op)
	if err != nil {
		return nil, err
	}

	// Initialize plan
	plan := &PlanV2{
		Steps:           make([]*StepV2, 0),
		RootStepIndexes: make([]int, 0),
	}

	// Step ID counter
	nextStepID := 0

	// Group root fields by responsible subgraph
	rootFieldsBySubGraph := make(map[*graph.SubGraphV2][]ast.Selection)

	for _, selection := range op.SelectionSet {
		field, ok := selection.(*ast.Field)
		if !ok {
			continue // InlineFragment and others will be handled later
		}

		fieldName := field.Name.String()

		// Get responsible subgraph from ownership map
		subGraphs := p.SuperGraph.GetSubGraphsForField(rootTypeName, fieldName)
		if len(subGraphs) == 0 {
			return nil, fmt.Errorf("no subgraph found for field %s.%s", rootTypeName, fieldName)
		}

		// Use the first subgraph (for @shareable fields there may be multiple, but use the first one for now)
		subGraph := subGraphs[0]
		rootFieldsBySubGraph[subGraph] = append(rootFieldsBySubGraph[subGraph], selection)
	}

	// Create root steps
	for subGraph, selections := range rootFieldsBySubGraph {
		step := &StepV2{
			ID:           nextStepID,
			SubGraph:     subGraph,
			StepType:     StepTypeQuery,
			ParentType:   rootTypeName,
			SelectionSet: selections,
			Path:         []string{rootTypeName},
			DependsOn:    []int{},
		}

		plan.Steps = append(plan.Steps, step)
		plan.RootStepIndexes = append(plan.RootStepIndexes, nextStepID)
		nextStepID++
	}

	// BFS processing queue
	queue := make([]*StepV2, 0)
	for _, idx := range plan.RootStepIndexes {
		queue = append(queue, plan.Steps[idx])
	}

	// Track processed steps
	processed := make(map[int]bool)

	// Process fields using BFS
	for len(queue) > 0 {
		currentStep := queue[0]
		queue = queue[1:]

		if processed[currentStep.ID] {
			continue
		}
		processed[currentStep.ID] = true

		// Traverse all fields in the step to detect boundary fields
		newSteps, err := p.findBoundaryFields(currentStep, plan, &nextStepID)
		if err != nil {
			return nil, err
		}

		// Add new steps to the queue
		queue = append(queue, newSteps...)
	}

	return plan, nil
}

// findBoundaryFields detects boundary fields (fields handled by different subgraphs) and creates new steps.
func (p *PlannerV2) findBoundaryFields(currentStep *StepV2, plan *PlanV2, nextStepID *int) ([]*StepV2, error) {
	newSteps := make([]*StepV2, 0)
	newStepsByKey := make(map[string]*StepV2)

	// Process each selection
	for _, selection := range currentStep.SelectionSet {
		steps, err := p.findBoundaryFieldsInSelection(selection, currentStep, plan, nextStepID, newStepsByKey, currentStep.ParentType)
		if err != nil {
			return nil, err
		}
		newSteps = append(newSteps, steps...)
	}

	return newSteps, nil
}

// findBoundaryFieldsInSelection recursively processes a selection to detect boundary fields.
func (p *PlannerV2) findBoundaryFieldsInSelection(
	selection ast.Selection,
	currentStep *StepV2,
	plan *PlanV2,
	nextStepID *int,
	newStepsByKey map[string]*StepV2,
	parentType string,
) ([]*StepV2, error) {
	field, ok := selection.(*ast.Field)
	if !ok {
		return nil, nil
	}

	fieldName := field.Name.String()

	// Get the field type
	fieldType, err := p.getFieldTypeName(parentType, fieldName)
	if err != nil {
		return nil, err
	}

	newSteps := make([]*StepV2, 0)

	// Process child fields
	for _, childSelection := range field.SelectionSet {
		childField, ok := childSelection.(*ast.Field)
		if !ok {
			continue
		}

		childFieldName := childField.Name.String()

		// Skip meta fields like __typename
		if childFieldName == "__typename" {
			continue
		}

		// Get responsible subgraph from ownership map
		subGraphs := p.SuperGraph.GetSubGraphsForField(fieldType, childFieldName)
		if len(subGraphs) == 0 {
			return nil, fmt.Errorf("no subgraph found for field %s.%s", fieldType, childFieldName)
		}

		childSubGraph := subGraphs[0]

		// Check if it's the same subgraph as the current step
		if childSubGraph.Name != currentStep.SubGraph.Name {
			// Different subgraph: create a new step
			stepKey := fmt.Sprintf("%s:%s:%d", childSubGraph.Name, fieldType, currentStep.ID)

			existingStep, exists := newStepsByKey[stepKey]
			if exists {
				// Add selection to existing step
				existingStep.SelectionSet = append(existingStep.SelectionSet, childSelection)
			} else {
				// Create a new step
				newStep := &StepV2{
					ID:            *nextStepID,
					SubGraph:      childSubGraph,
					StepType:      StepTypeEntity,
					ParentType:    fieldType,
					SelectionSet:  []ast.Selection{childSelection},
					Path:          append(append([]string{}, currentStep.Path...), fieldName),
					DependsOn:     []int{currentStep.ID},
					InsertionPath: append(append([]string{}, currentStep.Path...), fieldName),
				}

				// Dependency resolution: inject @key fields into the corresponding field of the current step
				if err := p.injectKeyFieldsToField(currentStep.SelectionSet, fieldName, fieldType, childSubGraph); err != nil {
					return nil, err
				}

				plan.Steps = append(plan.Steps, newStep)
				newStepsByKey[stepKey] = newStep
				newSteps = append(newSteps, newStep)
				*nextStepID++
			}
			// If crossing a boundary, the child fields will be processed in the new step, so skip here
			continue
		}

		// Same subgraph: recursively process child fields
		if len(childField.SelectionSet) > 0 {
			childType, err := p.getFieldTypeName(fieldType, childFieldName)
			if err != nil {
				return nil, err
			}

			steps, err := p.findBoundaryFieldsInSelection(childSelection, currentStep, plan, nextStepID, newStepsByKey, childType)
			if err != nil {
				return nil, err
			}
			newSteps = append(newSteps, steps...)
		}
	}

	return newSteps, nil
}

// injectKeyFieldsToField injects @key fields into a specific field.
func (p *PlannerV2) injectKeyFieldsToField(selections []ast.Selection, targetFieldName, typeName string, targetSubGraph *graph.SubGraphV2) error {
	// Get entity from targetSubGraph
	entity, exists := targetSubGraph.GetEntity(typeName)
	if !exists {
		// Skip if not an entity
		return nil
	}

	// Get @key fields
	if len(entity.Keys) == 0 {
		return nil
	}

	// Use the first key
	keyFieldSet := entity.Keys[0].FieldSet

	// Find targetFieldName from SelectionSet
	for _, selection := range selections {
		if field, ok := selection.(*ast.Field); ok {
			if field.Name.String() == targetFieldName {
				// Check if key field already exists
				hasKeyField := false
				for _, subSel := range field.SelectionSet {
					if subField, ok := subSel.(*ast.Field); ok {
						if subField.Name.String() == keyFieldSet {
							hasKeyField = true
							break
						}
					}
				}

				// Add key field
				if !hasKeyField {
					keyField := &ast.Field{
						Name: &ast.Name{
							Token: token.Token{Type: token.IDENT, Literal: keyFieldSet},
							Value: keyFieldSet,
						},
					}
					field.SelectionSet = append(field.SelectionSet, keyField)
				}
				return nil
			}
		}
	}

	return nil
}

// injectKeyFieldsToSelection injects @key fields into the specified SelectionSet.
func (p *PlannerV2) injectKeyFieldsToSelection(selections []ast.Selection, typeName string, targetSubGraph *graph.SubGraphV2) error {
	// Get entity from targetSubGraph
	entity, exists := targetSubGraph.GetEntity(typeName)
	if !exists {
		// Skip if not an entity
		return nil
	}

	// Get @key fields
	if len(entity.Keys) == 0 {
		return nil
	}

	// Use the first key
	keyFieldSet := entity.Keys[0].FieldSet

	// Find fields of the corresponding type in the SelectionSet and inject the key
	for _, selection := range selections {
		if field, ok := selection.(*ast.Field); ok {
			// Check if key field already exists
			hasKeyField := false
			for _, subSel := range field.SelectionSet {
				if subField, ok := subSel.(*ast.Field); ok {
					if subField.Name.String() == keyFieldSet {
						hasKeyField = true
						break
					}
				}
			}

			// Add key field
			if !hasKeyField {
				keyField := &ast.Field{
					Name: &ast.Name{
						Token: token.Token{Type: token.IDENT, Literal: keyFieldSet},
						Value: keyFieldSet,
					},
				}
				field.SelectionSet = append(field.SelectionSet, keyField)
			}
		}
	}

	return nil
}

// injectKeyFields injects @key fields of the specified type into the SelectionSet (legacy).
func (p *PlannerV2) injectKeyFields(step *StepV2, typeName string) error {
	// Get entity from subgraph
	entity, exists := step.SubGraph.GetEntity(typeName)
	if !exists {
		// Skip if not an entity
		return nil
	}

	// Get @key fields
	if len(entity.Keys) == 0 {
		return nil
	}

	// Use the first key
	keyFieldSet := entity.Keys[0].FieldSet

	// Add keyField to SelectionSet (skip if already exists)
	for _, selection := range step.SelectionSet {
		if field, ok := selection.(*ast.Field); ok {
			for _, subSel := range field.SelectionSet {
				if subField, ok := subSel.(*ast.Field); ok {
					if subField.Name.String() == keyFieldSet {
						// Already exists
						return nil
					}
				}
			}

			// Add key field
			keyField := &ast.Field{
				Name: &ast.Name{
					Token: token.Token{Type: token.IDENT, Literal: keyFieldSet},
					Value: keyFieldSet,
				},
			}
			field.SelectionSet = append(field.SelectionSet, keyField)
		}
	}

	return nil
}

// getOperation はドキュメントからオペレーションを取得する
func (p *PlannerV2) getOperation(doc *ast.Document) *ast.OperationDefinition {
	for _, def := range doc.Definitions {
		if op, ok := def.(*ast.OperationDefinition); ok {
			return op
		}
	}
	return nil
}

// getRootTypeName はオペレーションからルート型名を取得する
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

	// SchemaDefinition から実際の型名を取得
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

// getFieldTypeName はフィールドの型名を取得する
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

// getNamedType は Type から名前付き型を取得する
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
