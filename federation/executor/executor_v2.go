package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"golang.org/x/sync/errgroup"
)

// GraphQLError represents a GraphQL error with path information.
type GraphQLError struct {
	Message    string                 `json:"message"`
	Path       []interface{}          `json:"path,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// ExecutorV2 executes a query plan by orchestrating requests to subgraphs.
type ExecutorV2 struct {
	httpClient   *http.Client
	queryBuilder *QueryBuilderV2
	superGraph   *graph.SuperGraphV2
}

// NewExecutorV2 creates a new ExecutorV2 instance.
func NewExecutorV2(httpClient *http.Client, superGraph *graph.SuperGraphV2) *ExecutorV2 {
	return &ExecutorV2{
		httpClient:   httpClient,
		queryBuilder: NewQueryBuilderV2(superGraph),
		superGraph:   superGraph,
	}
}

// ExecutionContext holds the execution state.
type ExecutionContext struct {
	ctx     context.Context
	plan    *planner.PlanV2
	results map[int]interface{} // Step ID -> Result
	errors  []GraphQLError      // Accumulated errors
	mu      sync.RWMutex
}

// Execute executes a query plan and returns the merged result.
// It validates the plan is a DAG, then executes steps in dependency order.
func (e *ExecutorV2) Execute(
	ctx context.Context,
	plan *planner.PlanV2,
	variables map[string]interface{},
) (map[string]interface{}, error) {
	// Validate DAG
	if err := e.validateDAG(plan); err != nil {
		return nil, fmt.Errorf("invalid plan: %w", err)
	}

	// Initialize execution context
	execCtx := &ExecutionContext{
		ctx:     ctx,
		plan:    plan,
		results: make(map[int]interface{}),
		errors:  make([]GraphQLError, 0),
	}

	// Execute root steps (don't fail on error, collect them)
	_ = e.executeSteps(execCtx, plan.RootStepIndexes, variables)

	// Build final response from root step results
	response := make(map[string]interface{})
	data := make(map[string]interface{})

	// Merge all root step results
	for _, stepID := range plan.RootStepIndexes {
		execCtx.mu.RLock()
		stepResult := execCtx.results[stepID]
		execCtx.mu.RUnlock()

		if stepData, ok := stepResult.(map[string]interface{}); ok {
			if stepDataMap, ok := stepData["data"].(map[string]interface{}); ok {
				for k, v := range stepDataMap {
					data[k] = v
				}
			}
		}
	}

	response["data"] = data

	// Add errors if any occurred
	execCtx.mu.RLock()
	if len(execCtx.errors) > 0 {
		response["errors"] = execCtx.errors
	}
	execCtx.mu.RUnlock()

	// Prune response to remove fields not requested in original query
	return e.pruneResponse(response, plan), nil
}

// validateDAG validates that the plan is a directed acyclic graph (no cycles).
// It uses topological sort (Kahn's algorithm) to detect cycles.
func (e *ExecutorV2) validateDAG(plan *planner.PlanV2) error {
	// Build in-degree map
	inDegree := make(map[int]int)
	for _, step := range plan.Steps {
		if _, exists := inDegree[step.ID]; !exists {
			inDegree[step.ID] = 0
		}
		for range step.DependsOn {
			inDegree[step.ID]++
		}
	}

	// Find nodes with in-degree 0
	queue := make([]int, 0)
	for stepID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, stepID)
		}
	}

	visited := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		visited++

		// Find steps that depend on current step
		for _, step := range plan.Steps {
			for _, dep := range step.DependsOn {
				if dep == current {
					inDegree[step.ID]--
					if inDegree[step.ID] == 0 {
						queue = append(queue, step.ID)
					}
				}
			}
		}
	}

	// If visited count != total steps, there's a cycle
	if visited != len(plan.Steps) {
		return fmt.Errorf("plan contains circular dependencies")
	}

	return nil
}

// executeSteps executes a group of steps in parallel and then recursively executes dependent steps.
func (e *ExecutorV2) executeSteps(
	execCtx *ExecutionContext,
	stepIDs []int,
	variables map[string]interface{},
) error {
	if len(stepIDs) == 0 {
		return nil
	}

	// Execute all steps in this group in parallel
	eg, ctx := errgroup.WithContext(execCtx.ctx)

	for _, stepID := range stepIDs {
		stepID := stepID // Capture for goroutine
		step := execCtx.plan.Steps[stepID]

		eg.Go(func() error {
			return e.processStep(ctx, execCtx, step, variables)
		})
	}

	// Wait for all steps in this group to complete
	if err := eg.Wait(); err != nil {
		return err
	}

	// Find next steps to execute (steps whose dependencies are now all satisfied)
	nextSteps := e.findReadySteps(execCtx)
	if len(nextSteps) > 0 {
		return e.executeSteps(execCtx, nextSteps, variables)
	}

	return nil
}

// findReadySteps finds steps whose dependencies have all been completed.
func (e *ExecutorV2) findReadySteps(execCtx *ExecutionContext) []int {
	ready := make([]int, 0)

	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()

	for _, step := range execCtx.plan.Steps {
		// Skip if already executed
		if _, exists := execCtx.results[step.ID]; exists {
			continue
		}

		// Check if all dependencies are satisfied
		allDepsReady := true
		for _, depID := range step.DependsOn {
			if _, exists := execCtx.results[depID]; !exists {
				allDepsReady = false
				break
			}
		}

		if allDepsReady && len(step.DependsOn) > 0 {
			ready = append(ready, step.ID)
		}
	}

	return ready
}

// processStep processes a single step.
func (e *ExecutorV2) processStep(
	ctx context.Context,
	execCtx *ExecutionContext,
	step *planner.StepV2,
	variables map[string]interface{},
) error {
	// Guard against nil subgraph
	if step.SubGraph == nil {
		err := fmt.Errorf("step %d has nil subgraph", step.ID)
		e.recordError(execCtx, step, err)
		return err
	}

	var query string
	var queryVars map[string]interface{}
	var err error

	if step.StepType == planner.StepTypeQuery {
		// Root query
		query, queryVars, err = e.queryBuilder.Build(step, nil, variables)
		if err != nil {
			e.recordError(execCtx, step, fmt.Errorf("failed to build root query: %w", err))
			return err
		}
	} else {
		// Entity query - need to extract representations from parent results
		representations := e.extractRepresentations(execCtx, step)
		if len(representations) == 0 {
			// No entities to fetch, skip this step
			execCtx.mu.Lock()
			execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
			execCtx.mu.Unlock()
			return nil
		}

		query, queryVars, err = e.queryBuilder.Build(step, representations, variables)
		if err != nil {
			e.recordError(execCtx, step, fmt.Errorf("failed to build entity query: %w", err))
			return err
		}
	}

	// Send request to subgraph
	result, err := e.sendRequest(ctx, step.SubGraph.Host, query, queryVars)
	if err != nil {
		// Record error but continue with partial response
		e.recordError(execCtx, step, err)
		e.setNullForFailedStep(execCtx, step)
		return nil // Don't propagate error, allow partial response
	}

	// Check if result contains errors
	if errors, hasErrors := result["errors"]; hasErrors && errors != nil {
		// Record GraphQL errors from subgraph
		e.recordSubgraphErrors(execCtx, step, errors)
	}

	// Store result or merge into parent
	if step.StepType == planner.StepTypeQuery {
		execCtx.mu.Lock()
		execCtx.results[step.ID] = result
		execCtx.mu.Unlock()

	} else {
		// Merge entity results into parent
		if err := e.mergeEntityResults(execCtx, step, result); err != nil {
			e.recordError(execCtx, step, fmt.Errorf("failed to merge entity results: %w", err))
			e.setNullForFailedStep(execCtx, step)
			return nil // Don't propagate error
		}
		execCtx.mu.Lock()
		execCtx.results[step.ID] = result
		execCtx.mu.Unlock()

	}

	return nil
}

// recordError records an error in the execution context with path information.
func (e *ExecutorV2) recordError(execCtx *ExecutionContext, step *planner.StepV2, err error) {
	if step.StepType == planner.StepTypeEntity && len(step.SelectionSet) > 0 {
		// For entity steps, record errors for each field (excluding key fields)
		basePath := e.buildErrorPath(step)
		for _, sel := range step.SelectionSet {
			if field, ok := sel.(*ast.Field); ok {
				fieldName := field.Name.String()
				if field.Alias != nil && field.Alias.String() != "" {
					fieldName = field.Alias.String()
				}
				// Skip __typename and common key fields (id, _id, etc.)
				if fieldName == "__typename" || fieldName == "id" || fieldName == "_id" {
					continue
				}
				fieldPath := make([]interface{}, len(basePath))
				copy(fieldPath, basePath)
				fieldPath = append(fieldPath, fieldName)

				graphqlErr := GraphQLError{
					Message: err.Error(),
					Path:    fieldPath,
					Extensions: map[string]interface{}{
						"serviceName": step.SubGraph.Name,
					},
				}

				execCtx.mu.Lock()
				execCtx.errors = append(execCtx.errors, graphqlErr)
				execCtx.mu.Unlock()
			}
		}
	} else {
		// For root steps, record a single error
		path := e.buildErrorPath(step)

		graphqlErr := GraphQLError{
			Message: err.Error(),
			Path:    path,
			Extensions: map[string]interface{}{
				"serviceName": step.SubGraph.Name,
			},
		}

		execCtx.mu.Lock()
		execCtx.errors = append(execCtx.errors, graphqlErr)
		execCtx.mu.Unlock()
	}
}

// recordSubgraphErrors records errors from subgraph response.
func (e *ExecutorV2) recordSubgraphErrors(execCtx *ExecutionContext, step *planner.StepV2, errors interface{}) {
	errorList, ok := errors.([]interface{})
	if !ok {
		return
	}

	for _, errItem := range errorList {
		errMap, ok := errItem.(map[string]interface{})
		if !ok {
			continue
		}

		message, _ := errMap["message"].(string)
		if message == "" {
			message = "Unknown error from subgraph"
		}

		// Build path by combining step path with error path from subgraph
		path := e.buildErrorPath(step)
		if errPath, hasPath := errMap["path"].([]interface{}); hasPath {
			path = append(path, errPath...)
		}

		graphqlErr := GraphQLError{
			Message: message,
			Path:    path,
			Extensions: map[string]interface{}{
				"serviceName": step.SubGraph.Name,
			},
		}

		if extensions, hasExt := errMap["extensions"].(map[string]interface{}); hasExt {
			for k, v := range extensions {
				graphqlErr.Extensions[k] = v
			}
		}

		execCtx.mu.Lock()
		execCtx.errors = append(execCtx.errors, graphqlErr)
		execCtx.mu.Unlock()
	}
}

// buildErrorPath builds the error path from step information.
func (e *ExecutorV2) buildErrorPath(step *planner.StepV2) []interface{} {
	path := make([]interface{}, 0)

	// Use InsertionPath for entity steps, Path for root steps
	var pathSegments []string
	if step.StepType == planner.StepTypeEntity && len(step.InsertionPath) > 0 {
		pathSegments = step.InsertionPath
	} else if len(step.Path) > 0 {
		pathSegments = step.Path
	}

	for _, segment := range pathSegments {
		// Skip root type names (Query, Mutation, Subscription)
		if segment == "Query" || segment == "Mutation" || segment == "Subscription" {
			continue
		}
		path = append(path, segment)
	}

	return path
}

// setNullForFailedStep sets null for the fields that failed to resolve.
func (e *ExecutorV2) setNullForFailedStep(execCtx *ExecutionContext, step *planner.StepV2) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()

	if step.StepType == planner.StepTypeQuery {
		// For root queries, create a null result
		nullData := make(map[string]interface{})
		for _, sel := range step.SelectionSet {
			if field, ok := sel.(*ast.Field); ok {
				fieldName := field.Name.String()
				if field.Alias != nil && field.Alias.String() != "" {
					fieldName = field.Alias.String()
				}
				nullData[fieldName] = nil
			}
		}
		execCtx.results[step.ID] = map[string]interface{}{
			"data": nullData,
		}
	} else {
		// For entity queries, set null for fields in parent result
		if len(step.DependsOn) == 0 {
			execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
			return
		}

		// Find root step result
		var rootStepID int
		var rootResult interface{}
		for _, s := range execCtx.plan.Steps {
			if len(s.DependsOn) == 0 {
				rootStepID = s.ID
				rootResult = execCtx.results[s.ID]
				break
			}
		}

		if rootResult == nil {
			execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
			return
		}

		rootResultMap, ok := rootResult.(map[string]interface{})
		if !ok {
			execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
			return
		}

		rootData, ok := rootResultMap["data"].(map[string]interface{})
		if !ok {
			execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
			return
		}

		// Navigate to target entity using InsertionPath
		mergePath := make([]string, 0)
		for i, segment := range step.InsertionPath {
			if i == 0 && (segment == "Query" || segment == "Mutation" || segment == "Subscription") {
				continue
			}
			mergePath = append(mergePath, segment)
		}

		// Navigate to the target entity
		var current interface{} = rootData
		for _, segment := range mergePath {
			if currentMap, ok := current.(map[string]interface{}); ok {
				if next, exists := currentMap[segment]; exists {
					current = next
				} else {
					execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
					return
				}
			} else if currentArray, ok := current.([]interface{}); ok {
				// If it's an array, set null for each item
				for _, item := range currentArray {
					if itemMap, ok := item.(map[string]interface{}); ok {
						e.setNullFieldsInEntity(itemMap, step.SelectionSet)
					}
				}
				execCtx.results[rootStepID] = rootResultMap
				execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
				return
			} else {
				execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
				return
			}
		}

		// Set null for each field in the selection set
		if entityMap, ok := current.(map[string]interface{}); ok {
			e.setNullFieldsInEntity(entityMap, step.SelectionSet)
		}

		// Update root result
		execCtx.results[rootStepID] = rootResultMap
		execCtx.results[step.ID] = map[string]interface{}{"data": map[string]interface{}{}}
	}
}

// setNullFieldsInEntity sets null for fields in an entity map.
func (e *ExecutorV2) setNullFieldsInEntity(entityMap map[string]interface{}, selectionSet []ast.Selection) {
	for _, sel := range selectionSet {
		if field, ok := sel.(*ast.Field); ok {
			fieldName := field.Name.String()
			if field.Alias != nil && field.Alias.String() != "" {
				fieldName = field.Alias.String()
			}
			// Skip __typename and key fields
			if fieldName == "__typename" || fieldName == "id" || fieldName == "_id" {
				continue
			}
			entityMap[fieldName] = nil
		}
	}
}

// extractRepresentations extracts entity representations from parent step results.
func (e *ExecutorV2) extractRepresentations(execCtx *ExecutionContext, step *planner.StepV2) []map[string]interface{} {
	representations := make([]map[string]interface{}, 0)

	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()

	// Get parent step results
	if len(step.DependsOn) == 0 {
		return representations
	}

	// For entity steps, we need to extract from the root step's result (which has been merged)
	// Find the root step (ID 0 or any step with no dependencies)
	var rootResult interface{}
	for _, s := range execCtx.plan.Steps {
		if len(s.DependsOn) == 0 {
			if result, exists := execCtx.results[s.ID]; exists {
				rootResult = result
				break
			}
		}
	}

	if rootResult == nil {
		return representations
	}

	// Navigate to the insertion path
	var current interface{} = rootResult

	// Extract data field
	if resultMap, ok := current.(map[string]interface{}); ok {
		if data, ok := resultMap["data"].(map[string]interface{}); ok {
			current = data
		} else {
			return representations
		}
	}

	// Navigate through the insertion path (skip "Query" or root type)
	for i, pathSegment := range step.InsertionPath {
		// Skip root type names (Query, Mutation, Subscription)
		if i == 0 && (pathSegment == "Query" || pathSegment == "Mutation" || pathSegment == "Subscription") {
			continue
		}

		currentMap, ok := current.(map[string]interface{})
		if !ok {
			// Current is not a map, something went wrong
			return representations
		}

		next, exists := currentMap[pathSegment]
		if !exists {
			return representations
		}

		// IMPORTANT: Check if next is an array BEFORE moving to it
		// If it's an array, we need to process array elements with the REMAINING path (not including this segment)
		if arr, isArray := next.([]interface{}); isArray {
			// Remaining path segments AFTER this array segment
			remainingPath := step.InsertionPath[i+1:]

			// For each array element, navigate the remaining path
			for _, elem := range arr {
				elemMap, ok := elem.(map[string]interface{})
				if !ok {
					continue
				}

				// Navigate through remaining path in this element, handling nested arrays
				elemResults := e.navigatePathWithArrays(elemMap, remainingPath, step)
				representations = append(representations, elemResults...)
			}

			return representations
		}

		current = next
	}

	// Extract representations from entities
	// Get @key fields from entity definition
	// We need to get the entity from the subgraph that owns it, not step.SubGraph
	ownerSubGraph := e.superGraph.GetEntityOwnerSubGraph(step.ParentType)
	if ownerSubGraph == nil {
		return representations
	}

	entity, exists := ownerSubGraph.GetEntity(step.ParentType)
	if !exists || len(entity.Keys) == 0 {
		return representations
	}

	keyField := entity.Keys[0].FieldSet

	// Handle both single entity and list of entities
	switch v := current.(type) {
	case map[string]interface{}:
		// Single entity
		if rep := e.buildRepresentation(v, step.ParentType, keyField); rep != nil {
			representations = append(representations, rep)
		}
	case []interface{}:
		// List of entities
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if rep := e.buildRepresentation(itemMap, step.ParentType, keyField); rep != nil {
					representations = append(representations, rep)
				}
			}
		}
	}

	return representations
}

// navigatePathWithArrays navigates through a path that may contain nested arrays
func (e *ExecutorV2) navigatePathWithArrays(current map[string]interface{}, path []string, step *planner.StepV2) []map[string]interface{} {
	representations := make([]map[string]interface{}, 0)

	if len(path) == 0 {
		// Reached the end - extract representation from current
		if ownerSubGraph := e.superGraph.GetEntityOwnerSubGraph(step.ParentType); ownerSubGraph != nil {
			if entity, exists := ownerSubGraph.GetEntity(step.ParentType); exists && len(entity.Keys) > 0 {
				keyField := entity.Keys[0].FieldSet
				if rep := e.buildRepresentation(current, step.ParentType, keyField); rep != nil {
					representations = append(representations, rep)
				}
			}
		}
		return representations
	}

	segment := path[0]
	remainingPath := path[1:]

	next, exists := current[segment]
	if !exists {
		return representations
	}

	// Check if next is an array
	if arr, isArray := next.([]interface{}); isArray {
		// Process each array element with remaining path
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]interface{}); ok {
				elemResults := e.navigatePathWithArrays(elemMap, remainingPath, step)
				representations = append(representations, elemResults...)
			}
		}
	} else if nextMap, ok := next.(map[string]interface{}); ok {
		// Continue navigating
		representations = e.navigatePathWithArrays(nextMap, remainingPath, step)
	}

	return representations
}

// buildRepresentation builds a representation for an entity.
// keyField can be a single field or composite keys separated by space (e.g., "number departureDate")
func (e *ExecutorV2) buildRepresentation(entity map[string]interface{}, typeName string, keyField string) map[string]interface{} {
	representation := map[string]interface{}{
		"__typename": typeName,
	}

	// Handle composite keys by splitting on whitespace
	keyFieldNames := strings.Fields(keyField)

	// Extract all key field values
	for _, fieldName := range keyFieldNames {
		if keyValue, exists := entity[fieldName]; exists {
			representation[fieldName] = keyValue
		} else {
			// Missing required key field
			return nil
		}
	}

	return representation
}

// mergeEntityResults merges entity query results back into parent results.
func (e *ExecutorV2) mergeEntityResults(execCtx *ExecutionContext, step *planner.StepV2, result map[string]interface{}) error {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()

	// Get parent step result
	if len(step.DependsOn) == 0 {
		return nil
	}

	// Always merge into the root step (Step 0), not the immediate parent
	// This is because nested entity steps (e.g., Step 2 depends on Step 1)
	// cannot merge into Step 1's _entities result format
	var rootStepID int
	var rootResult interface{}
	for _, s := range execCtx.plan.Steps {
		if len(s.DependsOn) == 0 {
			rootStepID = s.ID
			rootResult = execCtx.results[s.ID]
			break
		}
	}

	if rootResult == nil {
		return fmt.Errorf("root step result not found")
	}

	// Extract data from root result
	rootResultMap, ok := rootResult.(map[string]interface{})
	if !ok {
		return fmt.Errorf("root result is not a map")
	}

	rootData, ok := rootResultMap["data"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("root result does not have data field")
	}

	// Extract _entities from entity query result
	resultData, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil // No data to merge
	}

	entitiesData, ok := resultData["_entities"]
	if !ok {
		return nil // No entities to merge
	}

	// Build merge path (skip root type name)
	mergePath := make([]string, 0)
	for i, segment := range step.InsertionPath {
		// Skip root type names (Query, Mutation, Subscription)
		if i == 0 && (segment == "Query" || segment == "Mutation" || segment == "Subscription") {
			continue
		}
		mergePath = append(mergePath, segment)
	}

	// Navigate to the target field to check if it's an array or object
	// Also collect all array positions in the path for nested array handling
	var current interface{} = rootData
	var firstArrayIndex = -1 // Index of the first array in the path

	for i, segment := range mergePath {
		if currentMap, ok := current.(map[string]interface{}); ok {
			if next, exists := currentMap[segment]; exists {
				current = next

				// Check if the value we just navigated to is an array
				if _, isArray := current.([]interface{}); isArray {
					// We hit an array - mark it
					if firstArrayIndex < 0 {
						firstArrayIndex = i
					}
					break
				}
			} else {
				// Path doesn't exist yet
				current = nil
				break
			}
		} else {
			// Not a map or array, can't navigate further
			current = nil
			break
		}
	}

	// Handle different merge scenarios
	if firstArrayIndex >= 0 {
		// We encountered an array - need to handle nested array merging
		entities, ok := entitiesData.([]interface{})
		if !ok {
			return fmt.Errorf("entities data is not an array")
		}

		// Navigate to the first array
		var arrayContainer interface{} = rootData
		arrayPath := mergePath[:firstArrayIndex+1] // Include the array field itself
		for _, segment := range arrayPath {
			if containerMap, ok := arrayContainer.(map[string]interface{}); ok {
				arrayContainer = containerMap[segment]
			}
		}

		arrayData, ok := arrayContainer.([]interface{})
		if !ok {
			return fmt.Errorf("expected array at merge path %v", arrayPath)
		}

		// The remaining path after the array
		remainingPath := mergePath[firstArrayIndex+1:]

		// Merge entities into the nested structure
		entityIndex := 0
		for _, elem := range arrayData {
			elemMap, ok := elem.(map[string]interface{})
			if !ok {
				continue
			}

			// Recursively merge entities into potentially nested arrays
			entityIndex = e.mergeIntoNestedArrays(elemMap, entities, remainingPath, entityIndex, step)
		}

	} else if current == nil {
		// Path doesn't exist yet, treat as single object and let Merge handle it
		entities, ok := entitiesData.([]interface{})
		if !ok || len(entities) == 0 {
			return nil
		}

		firstEntity, ok := entities[0].(map[string]interface{})
		if !ok {
			return fmt.Errorf("first entity is not a map")
		}

		if err := Merge(rootData, firstEntity, mergePath); err != nil {
			return fmt.Errorf("failed to merge entity object: %w", err)
		}
	} else if _, isArray := current.([]interface{}); isArray {
		// Target is an array, merge entities directly
		if err := Merge(rootData, entitiesData, mergePath); err != nil {
			return fmt.Errorf("failed to merge entities array: %w", err)
		}
	} else {
		// Target is a single object, merge first entity
		entities, ok := entitiesData.([]interface{})
		if !ok || len(entities) == 0 {
			return nil
		}

		// For single object, merge the first entity's fields
		firstEntity, ok := entities[0].(map[string]interface{})
		if !ok {
			return fmt.Errorf("first entity is not a map")
		}

		if err := Merge(rootData, firstEntity, mergePath); err != nil {
			return fmt.Errorf("failed to merge entity object: %w", err)
		}
	}

	// Update the root step's result to reflect the merge
	execCtx.results[rootStepID] = rootResultMap

	return nil
}

// mergeIntoNestedArrays recursively merges entities into potentially nested array structures
// Returns the next entity index to use
func (e *ExecutorV2) mergeIntoNestedArrays(
	current map[string]interface{},
	entities []interface{},
	path []string,
	entityIndex int,
	step *planner.StepV2,
) int {
	if len(path) == 0 {
		// Reached the target - merge the entity here
		if entityIndex < len(entities) {
			if entityMap, ok := entities[entityIndex].(map[string]interface{}); ok {
				// Deep merge entity fields into current
				// Use the Merge function to properly handle nested structures
				Merge(current, entityMap, []string{})
			}
			return entityIndex + 1
		}
		return entityIndex
	}

	segment := path[0]
	remainingPath := path[1:]

	next, exists := current[segment]
	if !exists {
		return entityIndex
	}

	// Check if next is an array
	if arr, isArray := next.([]interface{}); isArray {
		// Process each array element
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]interface{}); ok {
				entityIndex = e.mergeIntoNestedArrays(elemMap, entities, remainingPath, entityIndex, step)
			}
		}
	} else if nextMap, ok := next.(map[string]interface{}); ok {
		// Continue navigating
		entityIndex = e.mergeIntoNestedArrays(nextMap, entities, remainingPath, entityIndex, step)
	}

	return entityIndex
}

// sendRequest sends a GraphQL request to a subgraph.
func (e *ExecutorV2) sendRequest(
	ctx context.Context,
	host string,
	query string,
	variables map[string]interface{},
) (map[string]interface{}, error) {
	// Build request body
	reqBody := map[string]interface{}{
		"query": query,
	}
	if len(variables) > 0 {
		reqBody["variables"] = variables
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", host, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result, nil
}

// pruneResponse removes fields from response that were not in the original query.
// This removes __typename and key fields that were added by the planner for entity resolution.
func (e *ExecutorV2) pruneResponse(resp map[string]interface{}, plan *planner.PlanV2) map[string]interface{} {
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		return resp
	}

	// Get the operation from the original document
	if plan.OriginalDocument == nil {
		return resp
	}

	op := getOperationFromDocument(plan.OriginalDocument)
	if op == nil || len(op.SelectionSet) == 0 {
		return resp
	}

	// Prune the data based on the original selection set
	prunedData := e.pruneObject(data, op.SelectionSet)

	result := make(map[string]interface{})
	result["data"] = prunedData
	if errors, ok := resp["errors"]; ok {
		result["errors"] = errors
	}

	return result
}

// pruneObject recursively prunes an object based on the selection set.
func (e *ExecutorV2) pruneObject(obj interface{}, selections []ast.Selection) interface{} {
	if obj == nil {
		return nil
	}

	switch v := obj.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for _, sel := range selections {
			field, ok := sel.(*ast.Field)
			if !ok {
				continue
			}

			fieldName := field.Name.String()
			lookupKey := fieldName
			if field.Alias != nil {
				lookupKey = field.Alias.String()
			}

			value, exists := v[fieldName]
			if !exists && lookupKey != fieldName {
				value, exists = v[lookupKey]
			}
			if !exists {
				continue
			}

			// Recursively prune child selections
			if len(field.SelectionSet) > 0 {
				result[lookupKey] = e.pruneObject(value, field.SelectionSet)
			} else {
				result[lookupKey] = value
			}
		}
		return result

	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = e.pruneObject(item, selections)
		}
		return result

	default:
		return v
	}
}

// getOperationFromDocument extracts the operation from a document.
func getOperationFromDocument(doc *ast.Document) *ast.OperationDefinition {
	if doc == nil {
		return nil
	}

	for _, def := range doc.Definitions {
		if op, ok := def.(*ast.OperationDefinition); ok {
			return op
		}
	}

	return nil
}
