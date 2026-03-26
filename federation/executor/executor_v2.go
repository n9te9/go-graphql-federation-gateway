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
	"time"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor/merger"
	"github.com/n9te9/go-graphql-federation-gateway/federation/executor/query_builder"
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
	httpClient      *http.Client
	pool            sync.Pool // pool of *ExecutionContext
	bufPool         sync.Pool // pool of *bytes.Buffer for request body serialization
	queryBuilder    query_builder.QueryBuilderV2
	merger          merger.Merger
	superGraph      *graph.SuperGraphV2
	subgraphTimeout time.Duration // per-subgraph request timeout; 0 means no extra timeout
}

// NewExecutorV2 creates a new ExecutorV2 instance.
func NewExecutorV2(httpClient *http.Client, superGraph *graph.SuperGraphV2) *ExecutorV2 {
	return newExecutorV2(httpClient, superGraph, 0)
}

// NewExecutorV2WithTimeout creates a new ExecutorV2 instance with a per-subgraph
// request timeout. When a subgraph does not respond within the given duration,
// the request is cancelled and a timeout error is recorded in the response.
func NewExecutorV2WithTimeout(httpClient *http.Client, superGraph *graph.SuperGraphV2, timeout time.Duration) *ExecutorV2 {
	return newExecutorV2(httpClient, superGraph, timeout)
}

func newExecutorV2(httpClient *http.Client, superGraph *graph.SuperGraphV2, timeout time.Duration) *ExecutorV2 {
	return &ExecutorV2{
		httpClient:      httpClient,
		subgraphTimeout: timeout,
		pool: sync.Pool{
			New: func() interface{} {
				return &ExecutionContext{
					results: make(map[int]interface{}),
					errors:  make([]GraphQLError, 0, 8), // Pre-allocate small capacity
				}
			},
		},
		// bufPool holds *bytes.Buffer instances for reuse across sendRequest calls.
		// Each buffer starts with a 4 KiB backing array which covers most GraphQL
		// request bodies without reallocation.
		bufPool: sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(make([]byte, 0, 4096))
			},
		},
		queryBuilder: query_builder.NewQueryBuilderV2(superGraph),
		superGraph:   superGraph,
		merger:       merger.NewMerger(),
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
func (e *ExecutorV2) Execute(ctx context.Context, plan *planner.PlanV2, variables map[string]interface{}) (map[string]interface{}, error) {
	// Validate DAG
	if err := e.validateDAG(plan); err != nil {
		return nil, fmt.Errorf("invalid plan: %w", err)
	}

	// Initialize execution context from pool
	execCtx := e.pool.Get().(*ExecutionContext)
	defer func() {
		// Clear context before returning to pool to prevent memory leaks
		execCtx.ctx = nil
		execCtx.plan = nil
		// Clear map entries (reuse underlying storage)
		for k := range execCtx.results {
			delete(execCtx.results, k)
		}
		// Reset slice but keep capacity
		execCtx.errors = execCtx.errors[:0]
		e.pool.Put(execCtx)
	}()

	// Set context and plan
	execCtx.ctx = ctx
	execCtx.plan = plan

	// Clear results and errors (should already be cleared from previous use)
	for k := range execCtx.results {
		delete(execCtx.results, k)
	}
	execCtx.errors = execCtx.errors[:0]

	// Execute root steps (don't fail on error, collect them)
	if plan.OperationType == "mutation" {
		_ = e.executeMutationSequentially(execCtx, variables)
	} else {
		_ = e.executeSteps(execCtx, plan.RootStepIndexes, variables)
	}

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
func (e *ExecutorV2) executeSteps(execCtx *ExecutionContext, stepIDs []int, variables map[string]interface{}) error {
	if len(stepIDs) == 0 {
		return nil
	}

	// Execute all steps in this group in parallel
	eg, ctx := errgroup.WithContext(execCtx.ctx)

	for _, stepID := range stepIDs {
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

// executeMutationSequentially executes mutation root steps one-by-one in order.
// After each root step completes its entity-resolution dependents are also run
// (sequentially) before proceeding to the next root mutation field.
// If a root step (or its entity resolution) records an error the remaining root
// steps are skipped, implementing "fail-fast / stop-on-first-error" semantics
// required by the GraphQL specification for mutations.
func (e *ExecutorV2) executeMutationSequentially(
	execCtx *ExecutionContext,
	variables map[string]interface{},
) error {
	for _, rootStepIdx := range execCtx.plan.RootStepIndexes {
		step := execCtx.plan.Steps[rootStepIdx]

		// Execute this mutation field
		if err := e.processStep(execCtx.ctx, execCtx, step, variables); err != nil {
			return err
		}

		// Stop if the step recorded any errors (e.g. HTTP failure)
		execCtx.mu.RLock()
		hasErrors := len(execCtx.errors) > 0
		execCtx.mu.RUnlock()
		if hasErrors {
			return nil // errors are already in execCtx, just stop
		}

		// Execute entity-resolution steps that are now ready (sequentially)
		if err := e.executeDependentsSequentially(execCtx, variables); err != nil {
			return err
		}

		// Stop if entity resolution produced errors
		execCtx.mu.RLock()
		hasErrors = len(execCtx.errors) > 0
		execCtx.mu.RUnlock()
		if hasErrors {
			return nil
		}
	}
	return nil
}

// executeDependentsSequentially repeatedly finds steps whose dependencies are now
// all satisfied and executes them one-by-one until no more are found.
// This is used after each root mutation step to drain entity-resolution work
// before moving on to the next mutation field.
func (e *ExecutorV2) executeDependentsSequentially(
	execCtx *ExecutionContext,
	variables map[string]interface{},
) error {
	for {
		ready := e.findReadySteps(execCtx)
		if len(ready) == 0 {
			return nil
		}
		for _, stepIdx := range ready {
			step := execCtx.plan.Steps[stepIdx]
			if err := e.processStep(execCtx.ctx, execCtx, step, variables); err != nil {
				return err
			}
		}
	}
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
func (e *ExecutorV2) processStep(ctx context.Context, execCtx *ExecutionContext, step *planner.StepV2, variables map[string]interface{}) error {
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
		// Root query - pass operation type from plan
		query, queryVars, err = e.queryBuilder.Build(step, nil, variables, execCtx.plan.OperationType)
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

		query, queryVars, err = e.queryBuilder.Build(step, representations, variables, execCtx.plan.OperationType)
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
		rootResult, rootResultIndex, err := e.extractRootResult(execCtx)
		if err != nil {
			e.recordError(execCtx, step, fmt.Errorf("failed to extract root result for merging: %w", err))
			e.setNullForFailedStep(execCtx, step)
			return nil
		}

		execCtx.mu.Lock()
		mergedRootResult, err := e.merger.MergeEntities(rootResult, result, step)
		if err != nil {
			e.recordError(execCtx, step, fmt.Errorf("failed to merge entities: %w", err))
			e.setNullForFailedStep(execCtx, step)
			return nil
		}
		execCtx.results[rootResultIndex] = mergedRootResult
		execCtx.results[step.ID] = mergedRootResult
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

		// Build path by combining step path with error path from subgraph.
		// For entity (_entities) responses the subgraph path starts with
		// ["_entities", <arrayIndex>, ...] which is an internal detail.
		// We strip that prefix so clients see the Gateway-level field path.
		path := e.buildErrorPath(step)
		if errPath, hasPath := errMap["path"].([]interface{}); hasPath {
			path = append(path, adjustEntityErrorPath(errPath)...)
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

// adjustEntityErrorPath strips the internal ["_entities", <index>, ...] prefix
// from a subgraph entity-query error path, returning only the meaningful tail.
// For example ["_entities", 0, "reviews"] → ["reviews"].
// Non-entity paths (no "_entities" prefix) are returned unchanged.
func adjustEntityErrorPath(errPath []interface{}) []interface{} {
	if len(errPath) == 0 {
		return errPath
	}
	s, ok := errPath[0].(string)
	if !ok || s != "_entities" {
		return errPath
	}
	// Skip "_entities" and the immediately following array index (float64 from JSON)
	skip := 1
	if len(errPath) > 1 {
		if _, isNum := errPath[1].(float64); isNum {
			skip = 2
		}
	}
	return errPath[skip:]
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

	// Extract representations using the @key declared by the TARGET subgraph (step.SubGraph).
	// This ensures we send the representation format the target expects (e.g. username key),
	// which must match what the planner injected into the parent step's SelectionSet.
	// If the target subgraph doesn't declare the entity (e.g. in some test setups), fall
	// back to the owner subgraph's @key for backward compatibility.
	entity, exists := step.SubGraph.GetEntity(step.ParentType)
	if !exists || len(entity.Keys) == 0 {
		ownerSubGraph := e.superGraph.GetEntityOwnerSubGraph(step.ParentType)
		if ownerSubGraph == nil {
			return representations
		}
		entity, exists = ownerSubGraph.GetEntity(step.ParentType)
		if !exists || len(entity.Keys) == 0 {
			return representations
		}
	}

	parsedFields := entity.Keys[0].ParsedFields

	// Collect @requires fields from this step's subgraph for the entity type
	requiredFields := e.collectRequiredFields(step)

	// Handle both single entity and list of entities
	switch v := current.(type) {
	case map[string]interface{}:
		// Single entity
		if rep := e.buildRepresentationFromNodes(v, step.ParentType, parsedFields, requiredFields); rep != nil {
			representations = append(representations, rep)
		}
	case []interface{}:
		// List of entities
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if rep := e.buildRepresentationFromNodes(itemMap, step.ParentType, parsedFields, requiredFields); rep != nil {
					representations = append(representations, rep)
				}
			}
		}
	}

	return representations
}

// collectRequiredFields collects all @requires field names for the entity type in this step's subgraph.
func (e *ExecutorV2) collectRequiredFields(step *planner.StepV2) []string {
	var required []string
	seen := make(map[string]bool)

	entityDef, exists := step.SubGraph.GetEntity(step.ParentType)
	if !exists {
		return required
	}

	// For each field in the step's SelectionSet, check if it has @requires
	for _, sel := range step.SelectionSet {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		if fieldMeta, ok := entityDef.Fields[field.Name.String()]; ok {
			for _, rf := range fieldMeta.Requires {
				if !seen[rf] {
					seen[rf] = true
					required = append(required, rf)
				}
			}
		}
	}

	return required
}

// navigatePathWithArrays navigates through a path that may contain nested arrays
func (e *ExecutorV2) navigatePathWithArrays(current map[string]interface{}, path []string, step *planner.StepV2) []map[string]interface{} {
	representations := make([]map[string]interface{}, 0)

	if len(path) == 0 {
		// Reached the end - use same key-selection logic as extractRepresentations:
		// prefer step.SubGraph's @key, fall back to owner's @key.
		entity, exists := step.SubGraph.GetEntity(step.ParentType)
		if !exists || len(entity.Keys) == 0 {
			if ownerSubGraph := e.superGraph.GetEntityOwnerSubGraph(step.ParentType); ownerSubGraph != nil {
				entity, exists = ownerSubGraph.GetEntity(step.ParentType)
			}
		}
		if exists && len(entity.Keys) > 0 {
			requiredFields := e.collectRequiredFields(step)
			parsedFields := entity.Keys[0].ParsedFields
			if rep := e.buildRepresentationFromNodes(current, step.ParentType, parsedFields, requiredFields); rep != nil {
				representations = append(representations, rep)
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

// buildRepresentationFromNodes builds a representation using a parsed KeyFieldNode tree.
// It supports both flat keys (leaf nodes) and nested keys (non-leaf nodes), e.g.:
//
//	"coordinate { lat lng }" → { __typename: "T", coordinate: { lat: X, lng: Y } }
//
// Returns nil if any required key field is missing from entity.
func (e *ExecutorV2) buildRepresentationFromNodes(entity map[string]interface{}, typeName string, nodes []*graph.KeyFieldNode, requiredFields []string) map[string]interface{} {
	representation := map[string]interface{}{
		"__typename": typeName,
	}

	for _, node := range nodes {
		if len(node.Fields) == 0 {
			// Leaf node: extract scalar value directly
			val, exists := entity[node.Name]
			if !exists {
				return nil
			}
			representation[node.Name] = val
		} else {
			// Non-leaf node: recurse into the nested object
			nested, exists := entity[node.Name]
			if !exists {
				return nil
			}
			nestedMap, ok := nested.(map[string]interface{})
			if !ok {
				return nil
			}
			sub := e.extractNestedKeyFields(nestedMap, node.Fields)
			if sub == nil {
				return nil
			}
			representation[node.Name] = sub
		}
	}

	// Inject @requires field values
	for _, rf := range requiredFields {
		if val, ok := entity[rf]; ok {
			representation[rf] = val
		}
	}

	return representation
}

// extractNestedKeyFields recursively extracts only the key fields from a nested object map.
// Returns nil if any required key field is missing.
func (e *ExecutorV2) extractNestedKeyFields(obj map[string]interface{}, nodes []*graph.KeyFieldNode) map[string]interface{} {
	result := map[string]interface{}{}
	for _, node := range nodes {
		if len(node.Fields) == 0 {
			val, exists := obj[node.Name]
			if !exists {
				return nil
			}
			result[node.Name] = val
		} else {
			nested, exists := obj[node.Name]
			if !exists {
				return nil
			}
			nestedMap, ok := nested.(map[string]interface{})
			if !ok {
				return nil
			}
			sub := e.extractNestedKeyFields(nestedMap, node.Fields)
			if sub == nil {
				return nil
			}
			result[node.Name] = sub
		}
	}
	return result
}

// buildRepresentation builds a representation for an entity.
// keyField can be a single field or composite keys separated by space (e.g., "number departureDate")
// requiredFields contains additional fields needed by @requires directives.
func (e *ExecutorV2) buildRepresentation(entity map[string]interface{}, typeName string, keyField string, requiredFields []string) map[string]interface{} {
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

	// Inject @requires field values into the representation
	for _, rf := range requiredFields {
		if val, ok := entity[rf]; ok {
			representation[rf] = val
		}
	}

	return representation
}

func (e *ExecutorV2) extractRootResult(execCtx *ExecutionContext) (map[string]interface{}, int, error) {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()

	var rootStepID int
	var rootResult any
	for _, s := range execCtx.plan.Steps {
		if len(s.DependsOn) == 0 {
			rootStepID = s.ID
			rootResult = execCtx.results[s.ID]
			break
		}
	}

	if rootResult == nil {
		return nil, -1, fmt.Errorf("root step result not found")
	}

	rootResultMap, ok := rootResult.(map[string]interface{})
	if !ok {
		return nil, -1, fmt.Errorf("root result is not a map")
	}

	return rootResultMap, rootStepID, nil
}

// sendRequest sends a GraphQL request to a subgraph.
func (e *ExecutorV2) sendRequest(
	ctx context.Context,
	host string,
	query string,
	variables map[string]interface{},
) (map[string]interface{}, error) {
	// Apply per-subgraph timeout if configured
	if e.subgraphTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.subgraphTimeout)
		defer cancel()
	}

	// Obtain a pooled buffer and encode the request body directly into it.
	// This avoids the intermediate []byte allocation from json.Marshal and the
	// bytes.NewReader wrapper. The buffer is returned to the pool after Do()
	// completes because httpClient.Do reads the body synchronously before
	// returning, making it safe to reuse the backing memory.
	buf := e.bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer e.bufPool.Put(buf)

	reqBody := map[string]interface{}{
		"query": query,
	}
	if len(variables) > 0 {
		reqBody["variables"] = variables
	}

	if err := json.NewEncoder(buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request using the pooled buffer directly as the body.
	// net/http detects *bytes.Buffer and sets Content-Length automatically.
	req, err := http.NewRequestWithContext(ctx, "POST", host, buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := e.httpClient.Do(req)
	if err != nil {
		// Distinguish timeout from other network errors
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("subgraph request timeout after %v: %w", e.subgraphTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Treat HTTP 4xx / 5xx as errors
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("subgraph returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Parse response JSON
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response (invalid JSON): %w", err)
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

	// Collect fragment definitions from the original document
	fragmentDefs := collectFragmentDefinitionsFromDocument(plan.OriginalDocument)

	// Expand fragments in the operation's selection set before pruning
	expandedSelections := expandFragmentsInSelections(op.SelectionSet, fragmentDefs)

	// Prune the data based on the expanded selection set
	prunedData := e.pruneObject(data, expandedSelections)

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
			switch s := sel.(type) {
			case *ast.Field:
				field := s
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

			case *ast.InlineFragment:
				// Handle inline fragments: include fields if __typename matches type condition
				if s.TypeCondition == nil {
					continue
				}
				typeCondition := s.TypeCondition.Name.String()
				objTypeName, hasTypename := v["__typename"].(string)
				if !hasTypename || objTypeName != typeCondition {
					continue
				}
				// __typename matches: include fields from this fragment
				for _, childSel := range s.SelectionSet {
					childField, ok := childSel.(*ast.Field)
					if !ok {
						continue
					}
					fieldName := childField.Name.String()
					lookupKey := fieldName
					if childField.Alias != nil {
						lookupKey = childField.Alias.String()
					}
					value, exists := v[fieldName]
					if !exists && lookupKey != fieldName {
						value, exists = v[lookupKey]
					}
					if !exists {
						continue
					}
					if len(childField.SelectionSet) > 0 {
						result[lookupKey] = e.pruneObject(value, childField.SelectionSet)
					} else {
						result[lookupKey] = value
					}
				}
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

// collectFragmentDefinitionsFromDocument extracts all fragment definitions from a document.
func collectFragmentDefinitionsFromDocument(doc *ast.Document) map[string]*ast.FragmentDefinition {
	fragments := make(map[string]*ast.FragmentDefinition)
	if doc == nil {
		return fragments
	}

	for _, def := range doc.Definitions {
		if fragDef, ok := def.(*ast.FragmentDefinition); ok {
			fragments[fragDef.Name.String()] = fragDef
		}
	}
	return fragments
}

// expandFragmentsInSelections recursively expands fragment spreads and inline fragments.
func expandFragmentsInSelections(selections []ast.Selection, fragmentDefs map[string]*ast.FragmentDefinition) []ast.Selection {
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
				newField.SelectionSet = expandFragmentsInSelections(sel.SelectionSet, fragmentDefs)
				result = append(result, newField)
			} else {
				result = append(result, sel)
			}

		case *ast.InlineFragment:
			// Expand inline fragment - inline its selections
			expandedSelections := expandFragmentsInSelections(sel.SelectionSet, fragmentDefs)
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
			expandedSelections := expandFragmentsInSelections(fragDef.SelectionSet, fragmentDefs)
			result = append(result, expandedSelections...)

		default:
			// Unknown selection type, include as-is
			result = append(result, sel)
		}
	}

	return result
}
