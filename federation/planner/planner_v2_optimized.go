package planner

import (
	"fmt"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/graphql-parser/ast"
)

// PlanOptimized generates an execution plan using Dijkstra-based graph traversal.
//
// The algorithm:
//  1. Extract all fields required by the query.
//  2. Fast Path: if all root fields belong to a single subgraph, delegate to Plan().
//  3. Dijkstra Path: run Dijkstra from all entry-point nodes (query root field owners)
//     to determine the minimum-cost subgraph assignment. @provides shortcuts are honored
//     (cost 0), so fields that can be resolved via @provides avoid extra entity fetches.
//
// For the Dijkstra path the plan is rebuilt by:
//   - Grouping root fields by the subgraph that owns them (same as Plan()).
//   - For entity fields, checking whether the required fields are reachable via a ShortCut
//     at cost 0, in which case no extra entity resolution step is needed.
func (p *PlannerV2) PlanOptimized(doc *ast.Document, variables map[string]any) (*PlanV2, error) {
	op := p.getOperation(doc)
	if op == nil {
		return nil, fmt.Errorf("no operation found")
	}
	if len(op.SelectionSet) == 0 {
		return nil, fmt.Errorf("empty selection")
	}

	fragmentDefs := p.collectFragmentDefinitions(doc)

	rootTypeName, err := p.getRootTypeName(op)
	if err != nil {
		return nil, err
	}

	expandedSelections := p.expandFragmentsInSelections(op.SelectionSet, fragmentDefs)

	// -----------------------------------------------------------------------
	// Fast Path check: can all root fields be served by a single subgraph?
	// -----------------------------------------------------------------------
	if p.isSingleSubGraphQuery(expandedSelections, rootTypeName) {
		return p.Plan(doc, variables)
	}

	// -----------------------------------------------------------------------
	// Dijkstra Path: build entry points and run graph traversal.
	// -----------------------------------------------------------------------
	entryPoints := p.collectEntryPoints(expandedSelections, rootTypeName)
	dijkstraResult := p.SuperGraph.Graph.Dijkstra(entryPoints)

	// Build the plan with @provides-aware subgraph assignment.
	plan := &PlanV2{
		Steps:            make([]*StepV2, 0),
		RootStepIndexes:  make([]int, 0),
		OriginalDocument: doc,
		OperationType:    string(op.Operation),
	}

	nextStepID := 0

	// Group root fields by their owning subgraph (identical to Plan()).
	rootFieldsBySubGraph := make(map[*graph.SubGraphV2][]ast.Selection)
	for _, sel := range expandedSelections {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		fieldName := field.Name.String()
		if fieldName == "__typename" || fieldName == "__schema" || fieldName == "__type" {
			continue
		}
		subGraphs := p.SuperGraph.GetSubGraphsForField(rootTypeName, fieldName)
		if len(subGraphs) == 0 {
			return nil, fmt.Errorf("no subgraph found for field %s.%s", rootTypeName, fieldName)
		}
		subGraph := subGraphs[0]
		rootFieldsBySubGraph[subGraph] = append(rootFieldsBySubGraph[subGraph], sel)
	}

	// Create root steps.
	for subGraph, selections := range rootFieldsBySubGraph {
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

	// Build entity steps with @provides optimization.
	for _, rootStepIdx := range plan.RootStepIndexes {
		rootStep := plan.Steps[rootStepIdx]
		originalSelections := rootFieldsBySubGraph[rootStep.SubGraph]
		p.findAndBuildEntityStepsOptimized(
			originalSelections,
			rootStep,
			plan,
			&nextStepID,
			rootStep.ParentType,
			rootStep.Path,
			fragmentDefs,
			dijkstraResult,
		)
	}

	p.injectRequiresDependencies(plan)

	return plan, nil
}

// isSingleSubGraphQuery returns true if all root-level fields are owned by the same subgraph.
func (p *PlannerV2) isSingleSubGraphQuery(selections []ast.Selection, rootTypeName string) bool {
	var singleSG *graph.SubGraphV2
	for _, sel := range selections {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		name := field.Name.String()
		if name == "__typename" || name == "__schema" || name == "__type" {
			continue
		}
		owners := p.SuperGraph.GetSubGraphsForField(rootTypeName, name)
		if len(owners) == 0 {
			return false
		}
		if singleSG == nil {
			singleSG = owners[0]
		} else if singleSG.Name != owners[0].Name {
			return false
		}
	}
	return singleSG != nil
}

// collectEntryPoints returns the Dijkstra entry node IDs for the given root selections.
// Each root field's owning subgraph type node is an entry point at cost 0.
func (p *PlannerV2) collectEntryPoints(selections []ast.Selection, rootTypeName string) []string {
	seen := make(map[string]bool)
	var entries []string
	for _, sel := range selections {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		name := field.Name.String()
		if name == "__typename" || name == "__schema" || name == "__type" {
			continue
		}
		owners := p.SuperGraph.GetSubGraphsForField(rootTypeName, name)
		for _, sg := range owners {
			// Entry at the field type node level
			fieldTypeName, err := p.getFieldTypeName(rootTypeName, name)
			if err != nil {
				continue
			}
			typeKey := graph.NodeKey(sg.Name, fieldTypeName, "")
			if !seen[typeKey] {
				seen[typeKey] = true
				entries = append(entries, typeKey)
			}
		}
	}
	return entries
}

// findAndBuildEntityStepsOptimized is an @provides-aware variant of findAndBuildEntitySteps.
// When a field is reachable via a cost-0 ShortCut from the current subgraph, no additional
// entity resolution step is generated for it.
func (p *PlannerV2) findAndBuildEntityStepsOptimized(
	selections []ast.Selection,
	parentStep *StepV2,
	plan *PlanV2,
	nextStepID *int,
	parentType string,
	currentPath []string,
	fragmentDefs map[string]*ast.FragmentDefinition,
	dijkstraResult *graph.DijkstraResult,
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

		fieldType, err := p.getFieldTypeName(parentType, fieldName)
		if err != nil {
			continue
		}

		fieldIdentifier := fieldName
		if field.Alias != nil && field.Alias.String() != "" {
			fieldIdentifier = field.Alias.String()
		}
		fieldPath := append(append([]string{}, currentPath...), fieldIdentifier)

		subGraphs := p.SuperGraph.GetSubGraphsForField(parentType, fieldName)
		if len(subGraphs) == 0 {
			continue
		}
		fieldSubGraph := subGraphs[0]
		entityOwnerSubGraph := p.SuperGraph.GetEntityOwnerSubGraph(fieldType)

		isBoundaryField := false
		targetSubGraph := fieldSubGraph

		if fieldSubGraph.Name != parentStep.SubGraph.Name {
			isBoundaryField = true
		} else if entityOwnerSubGraph != nil && entityOwnerSubGraph.Name != parentStep.SubGraph.Name {
			isBoundaryField = true
			targetSubGraph = entityOwnerSubGraph
		}

		if !isBoundaryField {
			if len(field.SelectionSet) > 0 {
				p.findAndBuildEntityStepsOptimized(
					field.SelectionSet, parentStep, plan, nextStepID,
					fieldType, fieldPath, fragmentDefs, dijkstraResult,
				)
			}
			continue
		}

		// -----------------------------------------------------------------------
		// @provides optimization: if every child field in field.SelectionSet is
		// reachable at cost 0 from the parent step's current subgraph node (via
		// ShortCut), we can skip generating an entity resolution step.
		// -----------------------------------------------------------------------
		if p.canResolveViaProvides(field.SelectionSet, parentStep.SubGraph, parentType, fieldName, fieldType, dijkstraResult) {
			// Inject the provided fields directly into the parent step's selection so
			// they are fetched in the same request.
			parentStep.SelectionSet = p.injectProvidedFields(
				parentStep.SelectionSet, parentType, fieldName, field.SelectionSet,
				parentStep.SubGraph, fieldType, fragmentDefs,
			)
			continue
		}

		// Standard entity step creation (identical to findAndBuildEntitySteps).
		var entityTypeToResolve string
		_, parentIsExtendedInTarget := targetSubGraph.GetEntity(parentType)
		if parentIsExtendedInTarget {
			entityTypeToResolve = parentType
		} else {
			entityTypeToResolve = fieldType
		}

		isNestedEntity := (entityOwnerSubGraph != nil && entityOwnerSubGraph.Name == targetSubGraph.Name)
		boundaryFieldPath := append(append([]string{}, currentPath...), fieldName)
		stepKey := fmt.Sprintf("%s:%s:%d:%s", targetSubGraph.Name, entityTypeToResolve, parentStep.ID, joinPath(boundaryFieldPath))

		existingStep, exists := entityStepsByKey[stepKey]
		if exists {
			existingStep.SelectionSet = p.mergeSelections(existingStep.SelectionSet, []ast.Selection{selection}, targetSubGraph, entityTypeToResolve, fragmentDefs)
		} else {
			var entitySelections []ast.Selection
			var insertionPath []string

			if entityTypeToResolve == parentType {
				entitySelections = p.buildEntityStepSelections([]ast.Selection{selection}, targetSubGraph, parentType, parentStep, entityTypeToResolve, fragmentDefs)
				insertionPath = currentPath
			} else {
				entitySelections = p.buildEntityStepSelections(field.SelectionSet, targetSubGraph, entityTypeToResolve, parentStep, entityTypeToResolve, fragmentDefs)
				insertionPath = append(currentPath, fieldName)
			}

			newStep := &StepV2{
				ID:            *nextStepID,
				SubGraph:      targetSubGraph,
				StepType:      StepTypeEntity,
				ParentType:    entityTypeToResolve,
				SelectionSet:  entitySelections,
				Path:          fieldPath,
				DependsOn:     []int{parentStep.ID},
				InsertionPath: insertionPath,
			}
			plan.Steps = append(plan.Steps, newStep)
			entityStepsByKey[stepKey] = newStep
			*nextStepID++

			var relativePathForParent []string
			if len(parentStep.InsertionPath) == 0 {
				if len(currentPath) > 0 && currentPath[0] == "Query" {
					relativePathForParent = currentPath[1:]
				} else {
					relativePathForParent = currentPath
				}
			} else {
				relativePathForParent = currentPath[len(parentStep.InsertionPath):]
			}
			if isNestedEntity && entityTypeToResolve != parentType {
				relativePathForParent = append(relativePathForParent, fieldName)
			}

			p.injectKeyFieldsIntoParentStep(parentStep, entityTypeToResolve, targetSubGraph, relativePathForParent)

			if len(field.SelectionSet) > 0 {
				nestedParentType := entityTypeToResolve
				if entityTypeToResolve == parentType {
					nestedParentType = fieldType
				}
				p.findAndBuildEntityStepsOptimized(
					field.SelectionSet, newStep, plan, nextStepID,
					nestedParentType, fieldPath, fragmentDefs, dijkstraResult,
				)
			}
		}
	}
}

// canResolveViaProvides returns true when ALL child selections of a boundary field
// are reachable at cost 0 via ShortCut edges from the parent subgraph.
//
// The check is:
//  1. Compute the field node key for the parent subgraph: "{sg}:{parentType}.{fieldName}"
//  2. Look up that node in the graph; check its ShortCut map.
//  3. For each child field, verify there is a ShortCut entry to "{sg}:{fieldType}.{childField}" with cost 0.
func (p *PlannerV2) canResolveViaProvides(
	childSelections []ast.Selection,
	parentSG *graph.SubGraphV2,
	parentType, fieldName, fieldType string,
	dijkstraResult *graph.DijkstraResult,
) bool {
	if len(childSelections) == 0 {
		return false
	}

	srcNodeKey := graph.NodeKey(parentSG.Name, parentType, fieldName)
	srcNode, ok := p.SuperGraph.Graph.Nodes[srcNodeKey]
	if !ok || len(srcNode.ShortCut) == 0 {
		return false
	}

	for _, sel := range childSelections {
		childField, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		childName := childField.Name.String()
		if childName == "__typename" {
			continue
		}
		// Check if this child field is in any ShortCut from srcNode.
		// ShortCut keys are real node IDs: "{sgName}:{fieldType}.{childField}"
		found := false
		for scKey := range srcNode.ShortCut {
			node, exists := p.SuperGraph.Graph.Nodes[scKey]
			if exists && node.TypeName == fieldType && node.FieldName == childName {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// injectProvidedFields adds the @provides-covered child selections into the parent step
// under the given field path, so they are returned in the same subgraph response.
func (p *PlannerV2) injectProvidedFields(
	selections []ast.Selection,
	parentType, fieldName string,
	childSelections []ast.Selection,
	sg *graph.SubGraphV2,
	fieldType string,
	fragmentDefs map[string]*ast.FragmentDefinition,
) []ast.Selection {
	for _, sel := range selections {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		if f.Name.String() == fieldName {
			// Merge child selections into this field.
			filtered := p.buildStepSelections(childSelections, sg, fieldType, fragmentDefs)
			f.SelectionSet = p.mergeSelectionsByName(f.SelectionSet, filtered)
			return selections
		}
	}
	// Field not yet present; create it and append.
	newField := &ast.Field{
		Name:         fieldNameToken(fieldName),
		SelectionSet: p.buildStepSelections(childSelections, sg, fieldType, fragmentDefs),
	}
	return append(selections, newField)
}

// mergeSelectionsByName merges new selections into existing ones, deduplicating by field name.
func (p *PlannerV2) mergeSelectionsByName(existing, additions []ast.Selection) []ast.Selection {
	names := make(map[string]bool)
	for _, sel := range existing {
		if f, ok := sel.(*ast.Field); ok {
			names[f.Name.String()] = true
		}
	}
	result := append([]ast.Selection{}, existing...)
	for _, sel := range additions {
		if f, ok := sel.(*ast.Field); ok {
			if !names[f.Name.String()] {
				result = append(result, sel)
				names[f.Name.String()] = true
			}
		}
	}
	return result
}

// joinPath joins path elements with ".".
func joinPath(path []string) string {
	result := ""
	for i, p := range path {
		if i > 0 {
			result += "."
		}
		result += p
	}
	return result
}

// fieldNameToken creates an *ast.Name from a plain string, used for synthetic field nodes.
func fieldNameToken(name string) *ast.Name {
	return &ast.Name{Value: name}
}
