package planner

import (
	"errors"
	"fmt"
	"strings"
	"sync"

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

func (s *StepV2) MergePath() []string {
	ret := make([]string, 0, len(s.InsertionPath))
	for i, segment := range s.InsertionPath {
		if i == 0 && (segment == "Query" || segment == "Mutation" || segment == "Subscription") {
			continue
		}
		ret = append(ret, segment)
	}
	return ret
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
	planCache  sync.Map            // key: query string, value: *PlanV2
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

	// Validate that no @inaccessible fields are queried.
	// This must run before the planning pass so clients receive a clear error.
	if err := p.validateQueryForInaccessible(op.SelectionSet, rootTypeName, fragmentDefs); err != nil {
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

	// Group root fields by responsible subgraph.
	// rootSubGraphOrder preserves the order in which subgraphs are first encountered
	// so that RootStepIndexes reflects the original field definition order.
	// This is required for mutation execution which must respect field order.
	rootFieldsBySubGraph := make(map[*graph.SubGraphV2][]ast.Selection)
	rootSubGraphOrder := make([]*graph.SubGraphV2, 0) // insertion-ordered subgraph list

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

		// For @shareable fields there may be multiple subgraphs; prefer the first match for root queries
		// (no parent step at root level, so pass "" to fall back to subGraphs[0]).
		subGraph := selectSubGraphForField(subGraphs, "")
		if _, exists := rootFieldsBySubGraph[subGraph]; !exists {
			// First time we see this subgraph: record its position in definition order
			rootSubGraphOrder = append(rootSubGraphOrder, subGraph)
		}
		rootFieldsBySubGraph[subGraph] = append(rootFieldsBySubGraph[subGraph], selection)
	}

	// Create root steps in field-definition order (determined by rootSubGraphOrder).
	for _, subGraph := range rootSubGraphOrder {
		selections := rootFieldsBySubGraph[subGraph]
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

	// Inject @requires dependencies into parent steps
	p.injectRequiresDependencies(plan)

	// TODO: Apply @provides optimization
	// @provides allows a subgraph to declare that it already provides certain fields
	// that would normally require a separate fetch from another subgraph.
	// Implementation would involve:
	// 1. Scanning steps for fields with @provides directives
	// 2. Checking if provided fields are queried
	// 3. Overriding ownership for provided fields to avoid unnecessary entity fetches
	// 4. Removing or merging entity resolution steps that are no longer needed
	// This optimization can significantly reduce network calls in federated queries.
	// For now, @provides directives are parsed and available in entity field metadata,
	// but the optimization logic is deferred to future implementation.

	return plan, nil
}

// PlanCache exposes the internal plan cache for read/write access by the gateway layer.
func (p *PlannerV2) PlanCache() *sync.Map {
	return &p.planCache
}

// PlanCached returns a cached plan for the given query string if available,
// otherwise calls Plan and stores the result. Variables are not part of the cache
// key because Plan does not use them — they are applied at execution time.
func (p *PlannerV2) PlanCached(query string, doc *ast.Document, variables map[string]any) (*PlanV2, error) {
	if cached, ok := p.planCache.Load(query); ok {
		return cached.(*PlanV2), nil
	}
	plan, err := p.Plan(doc, variables)
	if err != nil {
		return nil, err
	}
	p.planCache.Store(query, plan)
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
			// Preserve inline fragment with its type condition while still expanding
			// any nested fragment spreads within it.
			// This keeps union/interface discriminators (e.g. "... on Product { id name }")
			// intact so buildStepSelections can treat them as typed groups.
			newInline := &ast.InlineFragment{
				TypeCondition: sel.TypeCondition,
				Directives:    sel.Directives,
			}
			newInline.SelectionSet = p.expandFragmentsInSelections(sel.SelectionSet, fragmentDefs)
			result = append(result, newInline)

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
			// Use subGraphContains to support @shareable fields owned by multiple subgraphs.
			subGraphs := p.SuperGraph.GetSubGraphsForField(parentType, fieldName)
			if len(subGraphs) == 0 || !subGraphContains(subGraphs, subGraph.Name) {
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
			typeCondition := sel.TypeCondition.Name.String()
			if typeCondition == parentType {
				// Same-type inline fragment (e.g. "... on Product {}" inside a Product context):
				// flatten fields and apply normal ownership rules.
				flatSelections := p.buildStepSelections(sel.SelectionSet, subGraph, typeCondition, fragmentDefs)
				result = append(result, flatSelections...)
			} else {
				// Different-type inline fragment — union/interface discriminator
				// (e.g. "... on Product {}" inside a SearchResult union context).
				// Preserve the fragment wrapper and include all fields that this
				// subgraph declares (even @external), because the resolver can return them.
				inlineSelections := p.buildUnionFragmentSelections(sel.SelectionSet, subGraph, typeCondition)
				if len(inlineSelections) > 0 {
					newFrag := &ast.InlineFragment{TypeCondition: sel.TypeCondition}
					newFrag.SelectionSet = inlineSelections
					result = append(result, newFrag)
				}
			}

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
		// Handle inline fragments: for same-type fragments, recurse into their fields
		// for entity-boundary detection; for union/interface discriminators, skip
		// (their fields are provided directly by the subgraph resolver).
		if inlineFrag, ok := selection.(*ast.InlineFragment); ok {
			typeCondition := inlineFrag.TypeCondition.Name.String()
			if typeCondition == parentType {
				p.findAndBuildEntitySteps(inlineFrag.SelectionSet, parentStep, plan, nextStepID, typeCondition, currentPath, fragmentDefs)
			} else if p.SuperGraph.IsInterfaceObjectType(parentType) {
				// @interfaceObject: the inline fragment specifies a concrete type that this
				// @interfaceObject type represents. Create an entity step to resolve the
				// concrete type's fields from the appropriate subgraph.
				concreteType := typeCondition
				entityOwnerSubGraph := p.SuperGraph.GetEntityOwnerSubGraph(concreteType)
				if entityOwnerSubGraph != nil {
					// Use currentPath as the insertion path (concrete type is at the same level as the interface)
					fieldPath := currentPath

					// Build a unique step key for this @interfaceObject concrete type resolution
					stepKey := fmt.Sprintf("%s:%s:%d:io:%s", entityOwnerSubGraph.Name, concreteType, parentStep.ID, strings.Join(currentPath, "."))

					existingStep, exists := entityStepsByKey[stepKey]
					if exists {
						existingStep.SelectionSet = p.mergeSelections(existingStep.SelectionSet, inlineFrag.SelectionSet, entityOwnerSubGraph, concreteType, fragmentDefs)
					} else {
						// Build entity selections from the inline fragment's fields
						entitySelections := p.buildEntityStepSelections(inlineFrag.SelectionSet, entityOwnerSubGraph, concreteType, parentStep, concreteType, fragmentDefs)

						newStep := &StepV2{
							ID:            *nextStepID,
							SubGraph:      entityOwnerSubGraph,
							StepType:      StepTypeEntity,
							ParentType:    concreteType,
							SelectionSet:  entitySelections,
							Path:          fieldPath,
							DependsOn:     []int{parentStep.ID},
							InsertionPath: currentPath,
						}
						plan.Steps = append(plan.Steps, newStep)
						entityStepsByKey[stepKey] = newStep
						*nextStepID++

						// Inject key fields into the parent step so entity resolution can extract representations
						var relativePathForParent []string
						if len(parentStep.InsertionPath) == 0 {
							if len(currentPath) > 0 && (currentPath[0] == "Query" || currentPath[0] == "Mutation" || currentPath[0] == "Subscription") {
								relativePathForParent = currentPath[1:]
							} else {
								relativePathForParent = currentPath
							}
						} else {
							relativePathForParent = currentPath[len(parentStep.InsertionPath):]
						}
						p.injectKeyFieldsIntoParentStep(parentStep, concreteType, entityOwnerSubGraph, relativePathForParent)

						// Recursively find nested boundary fields within the inline fragment's fields
						// (e.g., Product.name → products-v2 via @override)
						p.findAndBuildEntitySteps(inlineFrag.SelectionSet, newStep, plan, nextStepID, concreteType, currentPath, fragmentDefs)
					}
				}
			}
			// Different-type (union discriminator) inline fragments are left as-is:
			// the subgraph returns those fields directly; no extra entity steps needed.
			continue
		}

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
		// For @shareable fields, prefer the same subgraph as parentStep to avoid
		// unnecessary Entity Fetches.
		fieldSubGraph := selectSubGraphForField(subGraphs, parentStep.SubGraph.Name)

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
			// Case 2: Field returns an entity type whose canonical owner is a different subgraph.
			// However, if the field's own subgraph can ALSO directly resolve that entity
			// (e.g., a root query field that directly returns an entity it owns), then no
			// extra entity step is needed — the root step already provides the data.
			if !p.SuperGraph.CanSubGraphResolveEntity(fieldSubGraph, fieldType) {
				isBoundaryField = true
				targetSubGraph = entityOwnerSubGraph
			}
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

			// @provides optimization: when this field is an entity reference (not an extension)
			// and the parent step's subgraph declares @provides covering ALL requested child fields,
			// skip creating the entity step.
			//
			// Two conditions must both hold to skip:
			//  1. @provides lists all requested (non-key, non-__typename) child fields.
			//  2. Those @provides fields are declared in the parent subgraph schema for the entity
			//     type (so buildStepSelections will include them in the parent query).
			//
			// Condition 2 guards against @provides declarations whose fields are not in the
			// subgraph schema (e.g. Review.product @provides(fields: "name price") where the
			// reviews service does not declare Product.name — its resolver cannot return it).
			if entityTypeToResolve != parentType {
				provides := p.getFieldProvides(parentStep.SubGraph, parentType, fieldName)
				if len(provides) > 0 {
					expandedChildSels := p.expandFragmentsInSelections(field.SelectionSet, fragmentDefs)
					if p.childFieldsCoveredByProvides(expandedChildSels, provides, entityTypeToResolve, targetSubGraph) &&
						p.providedFieldsDeclaredInSchema(parentStep.SubGraph, entityTypeToResolve, expandedChildSels, provides, targetSubGraph) {
						// Compute injection path (same formula as injectKeyFieldsIntoParentStep).
						// buildStepSelections uses the ownership map and excludes @external fields,
						// so we must explicitly inject the @provides fields into the parent step's
						// selection so the parent service's resolver includes them in its response.
						var providesRelPath []string
						if len(parentStep.InsertionPath) == 0 {
							if len(currentPath) > 0 && (currentPath[0] == "Query" || currentPath[0] == "Mutation" || currentPath[0] == "Subscription") {
								providesRelPath = currentPath[1:]
							} else {
								providesRelPath = currentPath
							}
						} else {
							providesRelPath = currentPath[len(parentStep.InsertionPath):]
						}
						if isNestedEntity && entityTypeToResolve != parentType {
							providesRelPath = append(append([]string{}, providesRelPath...), fieldName)
						}
						providedSels := p.buildProvidesSelections(expandedChildSels, provides)
						parentStep.SelectionSet = p.ensureAndInjectKeySelections(
							parentStep.SelectionSet, providesRelPath, providedSels,
						)
						continue // Entity step skipped — parent service returns @provides fields directly
					}
				}
			}

			// The stepKey should identify a unique entity resolution step, based on:
			// - Target subgraph
			// - Entity type
			// - Parent step ID
			// - Insertion path (not including individual child field names)
			//
			// For entity extensions (entityTypeToResolve == parentType), multiple sibling
			// fields on the same parent type may all go to the same target subgraph
			// (e.g., `inStock` and `shippingCost` both in inventory for Product).
			// In that case we must NOT include the fieldName in the key so they merge
			// into a single entity fetch step.
			// For entity references (entityTypeToResolve != parentType), each boundary
			// field points to a different child entity instance, so we include fieldName.
			var stepKey string
			if entityTypeToResolve == parentType {
				// Extension: key is subgraph + entityType + parentStep ID + currentPath (no fieldName)
				stepKey = fmt.Sprintf("%s:%s:%d:ext:%s", targetSubGraph.Name, entityTypeToResolve, parentStep.ID, strings.Join(currentPath, "."))
			} else {
				// Reference: key includes fieldName to distinguish different child entity fields
				boundaryFieldPath := append(append([]string{}, currentPath...), fieldName)
				stepKey = fmt.Sprintf("%s:%s:%d:ref:%s", targetSubGraph.Name, entityTypeToResolve, parentStep.ID, strings.Join(boundaryFieldPath, "."))
			}

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
					// Root step: InsertionPath is empty, currentPath starts with the
					// operation type (Query / Mutation / Subscription).
					// Remove that prefix to get the path within the SelectionSet.
					if len(currentPath) > 0 && (currentPath[0] == "Query" || currentPath[0] == "Mutation" || currentPath[0] == "Subscription") {
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

// buildUnionFragmentSelections builds the selection set for a union/interface
// discriminator inline fragment (e.g. "... on Product { id name }" when the parent
// field returns a union "SearchResult").  Unlike buildStepSelections it does NOT
// filter out @external fields: the owning subgraph's resolver can return them even
// if they are declared external, so we include every field that is declared in the
// subgraph's schema for that concrete type.
func (p *PlannerV2) buildUnionFragmentSelections(
	selections []ast.Selection,
	subGraph *graph.SubGraphV2,
	typeCondition string,
) []ast.Selection {
	result := make([]ast.Selection, 0)
	for _, sel := range selections {
		switch s := sel.(type) {
		case *ast.Field:
			fieldName := s.Name.String()
			if fieldName == "__typename" {
				result = append(result, sel)
				continue
			}
			// Include the field if this subgraph declares it (even if @external)
			if p.fieldDeclaredInSubGraph(subGraph, typeCondition, fieldName) {
				result = append(result, sel)
			}
		case *ast.InlineFragment:
			// Preserve nested inline fragments the same way
			inner := p.buildUnionFragmentSelections(s.SelectionSet, subGraph, s.TypeCondition.Name.String())
			if len(inner) > 0 {
				newFrag := &ast.InlineFragment{TypeCondition: s.TypeCondition}
				newFrag.SelectionSet = inner
				result = append(result, newFrag)
			}
		}
	}
	return result
}

// fieldDeclaredInSubGraph reports whether the given field is declared for typeName
// in the subgraph's schema, regardless of whether it carries @external.
func (p *PlannerV2) fieldDeclaredInSubGraph(subGraph *graph.SubGraphV2, typeName, fieldName string) bool {
	for _, def := range subGraph.Schema.Definitions {
		switch d := def.(type) {
		case *ast.ObjectTypeDefinition:
			if d.Name.String() == typeName {
				for _, f := range d.Fields {
					if f.Name.String() == fieldName {
						return true
					}
				}
			}
		case *ast.ObjectTypeExtension:
			if d.Name.String() == typeName {
				for _, f := range d.Fields {
					if f.Name.String() == fieldName {
						return true
					}
				}
			}
		}
	}
	return false
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
			// Use subGraphContains to support @shareable fields owned by multiple subgraphs.
			fieldSubGraphs := p.SuperGraph.GetSubGraphsForField(entityType, fieldName)
			if len(fieldSubGraphs) > 0 && subGraphContains(fieldSubGraphs, subGraph.Name) {
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

// buildProvidesSelections returns AST selections for the requested fields that are
// covered by @provides. Only fields present in both requestedSels and the provides
// list are included (key fields and __typename are always omitted since they are
// already managed separately).
func (p *PlannerV2) buildProvidesSelections(requestedSels []ast.Selection, provides []string) []ast.Selection {
	providesSet := make(map[string]bool, len(provides))
	for _, pf := range provides {
		providesSet[pf] = true
	}
	var sels []ast.Selection
	for _, sel := range requestedSels {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		if providesSet[field.Name.String()] {
			sels = append(sels, field)
		}
	}
	return sels
}

// getFieldProvides returns the @provides field names for a given field on a parent type
// within the specified subgraph. It checks both entity types (with @key) and plain
// object types (without @key, e.g. Review) by searching the schema AST.
// Returns nil if @provides is not declared.
func (p *PlannerV2) getFieldProvides(subGraph *graph.SubGraphV2, parentType, fieldName string) []string {
	// Check entity types first (types with @key — stored in subGraph.entities)
	entity, ok := subGraph.GetEntity(parentType)
	if ok {
		field, ok := entity.Fields[fieldName]
		if ok && len(field.Provides) > 0 {
			return field.Provides
		}
	}

	// For non-entity types (no @key), parse @provides directly from the schema AST.
	for _, def := range subGraph.Schema.Definitions {
		obj, ok := def.(*ast.ObjectTypeDefinition)
		if !ok || obj.Name.String() != parentType {
			continue
		}
		for _, f := range obj.Fields {
			if f.Name.String() != fieldName {
				continue
			}
			for _, d := range f.Directives {
				if d.Name != "provides" {
					continue
				}
				for _, arg := range d.Arguments {
					if arg.Name.String() == "fields" {
						fieldsVal := strings.Trim(arg.Value.String(), "\"")
						return strings.Fields(fieldsVal)
					}
				}
			}
		}
	}
	return nil
}

// providedFieldsDeclaredInSchema checks that the @provides optimization can safely apply.
//
// The key safety condition: the entity type must be declared as a FULL type definition
// (`type Foo { ... }`, i.e. ObjectTypeDefinition) in the providing subgraph — not merely
// as an extension (`extend type Foo { ... }`).
//
// Rationale: when a subgraph declares `type User { id @external; username @external }`,
// its resolver is expected to return username directly (the @provides contract). But when
// it only declares `extend type Organization { name @external }`, the fields are declared
// for schema-composition purposes only; the actual resolver typically does NOT return those
// fields — it relies on entity resolution from the owning service.
//
// Examples:
//   - reviews schema: `type User @key(fields:"id") { id @external; username @external }` → full type → optimization OK
//   - projects schema: `extend type Organization { name @external }` → extension only → skip optimization
func (p *PlannerV2) providedFieldsDeclaredInSchema(subGraph *graph.SubGraphV2, entityType string, childSels []ast.Selection, provides []string, targetSG *graph.SubGraphV2) bool {
	// The entity type must be declared as a full ObjectTypeDefinition in the providing subgraph.
	// Extensions (ObjectTypeExtension) indicate that the resolver does not own the type and
	// is unlikely to return the @provides fields.
	entityTypeDeclaredAsFull := false
	for _, def := range subGraph.Schema.Definitions {
		if obj, ok := def.(*ast.ObjectTypeDefinition); ok && obj.Name.String() == entityType {
			entityTypeDeclaredAsFull = true
			break
		}
	}
	if !entityTypeDeclaredAsFull {
		return false
	}

	providesSet := make(map[string]bool, len(provides))
	for _, pf := range provides {
		providesSet[pf] = true
	}

	keyFields := p.getKeyFields(entityType, targetSG)
	keySet := make(map[string]bool, len(keyFields))
	for _, kf := range keyFields {
		keySet[kf] = true
	}

	for _, sel := range childSels {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		fname := field.Name.String()
		if fname == "__typename" || keySet[fname] {
			continue
		}
		if !providesSet[fname] {
			continue // Not a @provides field — not our concern here
		}
		// @provides field: must be declared in the subgraph schema
		if !p.fieldDeclaredInSubGraph(subGraph, entityType, fname) {
			return false
		}
	}
	return true
}

// childFieldsCoveredByProvides reports whether every requested non-trivial child field
// (i.e., excluding __typename and key fields of the entity type) is listed in provides.
func (p *PlannerV2) childFieldsCoveredByProvides(childSels []ast.Selection, provides []string, entityType string, targetSubGraph *graph.SubGraphV2) bool {
	if len(childSels) == 0 {
		return true // No child fields requested — trivially covered
	}

	providesSet := make(map[string]bool, len(provides))
	for _, pf := range provides {
		providesSet[pf] = true
	}

	// Key fields are always available so don't need to be in @provides
	keyFields := p.getKeyFields(entityType, targetSubGraph)
	keySet := make(map[string]bool, len(keyFields))
	for _, kf := range keyFields {
		keySet[kf] = true
	}

	for _, sel := range childSels {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		fname := field.Name.String()
		if fname == "__typename" || keySet[fname] {
			continue // Always available
		}
		if !providesSet[fname] {
			return false // Field not covered by @provides
		}
	}
	return true
}

// injectKeyFieldsIntoParentStep injects @key fields into the parent step's selections
// so that entity resolution can extract representations.
// It supports both flat keys ("id") and nested keys ("coordinate { lat lng }").
func (p *PlannerV2) injectKeyFieldsIntoParentStep(parentStep *StepV2, entityType string, childSubGraph *graph.SubGraphV2, insertionPath []string) {
	if len(insertionPath) == 0 {
		return
	}

	entity, exists := childSubGraph.GetEntity(entityType)
	if !exists || len(entity.Keys) == 0 {
		return
	}

	// Build AST selections from the parsed key field nodes (supports nesting)
	keyNodes := entity.Keys[0].ParsedFields
	keySelections := keyFieldNodesToASTSelections(keyNodes)

	// Always prepend __typename
	typenameField := &ast.Field{
		Name: &ast.Name{
			Token: token.Token{Type: token.IDENT, Literal: "__typename"},
			Value: "__typename",
		},
	}
	keySelections = append([]ast.Selection{typenameField}, keySelections...)

	parentStep.SelectionSet = p.ensureAndInjectKeySelections(parentStep.SelectionSet, insertionPath, keySelections)
}

// keyFieldNodesToASTSelections converts a slice of KeyFieldNode into AST selections.
// Leaf nodes produce simple ast.Field; non-leaf nodes produce ast.Field with nested SelectionSet.
func keyFieldNodesToASTSelections(nodes []*graph.KeyFieldNode) []ast.Selection {
	var sels []ast.Selection
	for _, node := range nodes {
		field := &ast.Field{
			Name: &ast.Name{
				Token: token.Token{Type: token.IDENT, Literal: node.Name},
				Value: node.Name,
			},
		}
		if len(node.Fields) > 0 {
			field.SelectionSet = keyFieldNodesToASTSelections(node.Fields)
		}
		sels = append(sels, field)
	}
	return sels
}

// ensureAndInjectKeySelections navigates insertionPath in selections, creating missing fields as
// needed, and at the terminal field merges keySelections into its SelectionSet.
func (p *PlannerV2) ensureAndInjectKeySelections(selections []ast.Selection, path []string, keySelections []ast.Selection) []ast.Selection {
	if len(path) == 0 {
		return selections
	}

	targetName := path[0]
	var targetField *ast.Field

	for _, sel := range selections {
		if f, ok := sel.(*ast.Field); ok {
			name := f.Name.String()
			if f.Alias != nil && f.Alias.String() != "" {
				name = f.Alias.String()
			}
			if name == targetName {
				targetField = f
				break
			}
		}
	}

	if targetField == nil {
		targetField = &ast.Field{
			Name: &ast.Name{
				Token: token.Token{Type: token.IDENT, Literal: targetName},
				Value: targetName,
			},
			SelectionSet: make([]ast.Selection, 0),
		}
		selections = append(selections, targetField)
	}

	if len(path) == 1 {
		// Terminal: merge keySelections into targetField.SelectionSet
		targetField.SelectionSet = mergeKeySelectionsInto(targetField.SelectionSet, keySelections)
	} else {
		targetField.SelectionSet = p.ensureAndInjectKeySelections(targetField.SelectionSet, path[1:], keySelections)
	}

	return selections
}

// mergeKeySelectionsInto adds each selection from keySelections into existing if not already present.
// For nested fields (non-leaf), it recursively merges children rather than duplicating the parent.
func mergeKeySelectionsInto(existing []ast.Selection, keySelections []ast.Selection) []ast.Selection {
	for _, keySel := range keySelections {
		keyField, ok := keySel.(*ast.Field)
		if !ok {
			continue
		}
		found := false
		for _, ex := range existing {
			if exField, ok := ex.(*ast.Field); ok && exField.Name.String() == keyField.Name.String() {
				// Field already present; merge children if this is a nested key field
				if len(keyField.SelectionSet) > 0 {
					exField.SelectionSet = mergeKeySelectionsInto(exField.SelectionSet, keyField.SelectionSet)
				}
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, keyField)
		}
	}
	return existing
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
// It checks both ObjectTypeDefinition and InterfaceTypeDefinition to support
// @interfaceObject entities defined as interface types.
func (p *PlannerV2) getFieldTypeName(parentTypeName, fieldName string) (string, error) {
	if fieldName == "__typename" {
		return "String", nil
	}

	for _, def := range p.SuperGraph.Schema.Definitions {
		// Check ObjectTypeDefinition
		if td, ok := def.(*ast.ObjectTypeDefinition); ok {
			if td.Name.String() == parentTypeName {
				for _, field := range td.Fields {
					if field.Name.String() == fieldName {
						return p.getNamedType(field.Type), nil
					}
				}
			}
		}
		// Also check InterfaceTypeDefinition (for @interfaceObject entities defined as interface types)
		if td, ok := def.(*ast.InterfaceTypeDefinition); ok {
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

// injectRequiresDependencies injects @requires fields into parent steps.
// This ensures that required fields are fetched before they're needed by child steps.
func (p *PlannerV2) injectRequiresDependencies(plan *PlanV2) {
	// For each step, check if any field has @requires
	for _, step := range plan.Steps {
		// Only entity steps need dependency injection
		if step.StepType != StepTypeEntity {
			continue
		}

		// Get required fields for this step's selections
		requiredFields := p.collectRequiredFields(step.SelectionSet, step.ParentType, step.SubGraph)

		if len(requiredFields) == 0 {
			continue
		}

		// Inject required fields into parent steps (steps this one depends on)
		for _, parentStepID := range step.DependsOn {
			parentStep := plan.Steps[parentStepID]

			// Inject into the entity fields within parent step
			// We need to find fields that return the entity type (step.ParentType)
			p.injectFieldsIntoSelections(parentStep.SelectionSet, parentStep.ParentType, step.ParentType, requiredFields)
		}
	}
}

// injectFieldsIntoSelections recursively finds fields that return targetTypeName and injects required fields
func (p *PlannerV2) injectFieldsIntoSelections(selections []ast.Selection, currentTypeName, targetTypeName string, fieldsToInject map[string]bool) {
	for _, sel := range selections {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}

		fieldName := field.Name.String()

		// Skip meta fields
		if fieldName == "__typename" {
			continue
		}

		// Get the field's return type
		fieldTypeName, err := p.getFieldTypeName(currentTypeName, fieldName)
		if err != nil {
			continue
		}

		// If this field's return type matches the target type, inject required fields here
		if fieldTypeName == targetTypeName {
			// Inject required fields into this field's selection set
			for requiredFieldName := range fieldsToInject {
				if !p.hasFieldInSelectionSet(field.SelectionSet, requiredFieldName) {
					newField := &ast.Field{
						Name: &ast.Name{Value: requiredFieldName},
					}
					field.SelectionSet = append(field.SelectionSet, newField)
				}
			}
		}

		// Recursively check nested selections
		if len(field.SelectionSet) > 0 {
			p.injectFieldsIntoSelections(field.SelectionSet, fieldTypeName, targetTypeName, fieldsToInject)
		}
	}
}

// collectRequiredFields collects all fields specified in @requires directives
// for the given selection set.
func (p *PlannerV2) collectRequiredFields(selections []ast.Selection, parentTypeName string, subGraph *graph.SubGraphV2) map[string]bool {
	required := make(map[string]bool)

	for _, sel := range selections {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}

		fieldName := field.Name.String()

		// Get entity metadata from subgraph
		if entity, exists := subGraph.GetEntity(parentTypeName); exists {
			if fieldMetadata, ok := entity.Fields[fieldName]; ok {
				// Add all required fields
				for _, reqField := range fieldMetadata.Requires {
					required[reqField] = true
				}
			}
		}

		// Recursively check nested selections
		if len(field.SelectionSet) > 0 {
			fieldTypeName, err := p.getFieldTypeName(parentTypeName, fieldName)
			if err == nil {
				nestedRequired := p.collectRequiredFields(field.SelectionSet, fieldTypeName, subGraph)
				for reqField := range nestedRequired {
					required[reqField] = true
				}
			}
		}
	}

	return required
}

// hasFieldInSelectionSet checks if a field with the given name exists in the selection set.
func (p *PlannerV2) hasFieldInSelectionSet(selections []ast.Selection, fieldName string) bool {
	for _, sel := range selections {
		if field, ok := sel.(*ast.Field); ok {
			if field.Name.String() == fieldName {
				return true
			}
		}
	}
	return false
}

// selectSubGraphForField selects the most appropriate subgraph for a field.
// For @shareable fields that can be resolved by multiple subgraphs, it prefers
// the subgraph that matches parentSubGraphName to avoid unnecessary Entity Fetches.
// If no match is found, it falls back to subGraphs[0] to preserve existing behaviour.
func selectSubGraphForField(
	subGraphs []*graph.SubGraphV2,
	parentSubGraphName string,
) *graph.SubGraphV2 {
	if parentSubGraphName != "" {
		for _, sg := range subGraphs {
			if sg.Name == parentSubGraphName {
				return sg
			}
		}
	}
	return subGraphs[0]
}

// subGraphContains reports whether any subgraph in the slice has the given name.
// Used to check ownership of @shareable fields that may be owned by multiple subgraphs.
func subGraphContains(subGraphs []*graph.SubGraphV2, name string) bool {
	for _, sg := range subGraphs {
		if sg.Name == name {
			return true
		}
	}
	return false
}

// validateQueryForInaccessible recursively walks the query selection set and returns an error
// if any field is marked @inaccessible in the subgraphs. This provides clear error messages
// to clients that attempt to query hidden (inaccessible) fields.
func (p *PlannerV2) validateQueryForInaccessible(
	selections []ast.Selection,
	parentTypeName string,
	fragmentDefs map[string]*ast.FragmentDefinition,
) error {
	for _, selection := range selections {
		switch sel := selection.(type) {
		case *ast.Field:
			fieldName := sel.Name.String()
			// Skip GraphQL meta-fields
			if fieldName == "__typename" || fieldName == "__schema" || fieldName == "__type" {
				continue
			}
			// Return a descriptive error if the field is @inaccessible
			if p.SuperGraph.IsFieldInaccessible(parentTypeName, fieldName) {
				return fmt.Errorf(
					"field '%s.%s' is marked @inaccessible and cannot be queried",
					parentTypeName, fieldName,
				)
			}
			// Recursively validate child selections
			if len(sel.SelectionSet) > 0 {
				fieldType, err := p.getFieldTypeName(parentTypeName, fieldName)
				if err == nil && fieldType != "" {
					if err := p.validateQueryForInaccessible(sel.SelectionSet, fieldType, fragmentDefs); err != nil {
						return err
					}
				}
			}

		case *ast.InlineFragment:
			typeCondition := sel.TypeCondition.Name.String()
			if err := p.validateQueryForInaccessible(sel.SelectionSet, typeCondition, fragmentDefs); err != nil {
				return err
			}

		case *ast.FragmentSpread:
			fragName := sel.Name.String()
			fragDef, ok := fragmentDefs[fragName]
			if !ok {
				continue
			}
			typeCondition := fragDef.TypeCondition.Name.String()
			if err := p.validateQueryForInaccessible(fragDef.SelectionSet, typeCondition, fragmentDefs); err != nil {
				return err
			}
		}
	}
	return nil
}
