package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/token"
)

type executionContext struct {
	concurrencyMap map[int]chan struct{}
	Errs           []error
}

type stepMetadata struct {
	repToRefIndices map[string][]int
	orderedRepKeys  []string
}

type Executor interface {
	Execute(ctx context.Context, plan *planner.Plan, variables map[string]any) map[string]any
}

type executor struct {
	QueryBuilder

	superGraph                 *graph.SuperGraph
	typeDefinitions            map[string]*ast.ObjectTypeDefinition
	httpClient                 *http.Client
	mux                        sync.Mutex
	enableOpentelemetryTracing bool
}

var _ Executor = (*executor)(nil)

type ExecutorOption struct {
	EnableOpentelemetryTracing bool
}

func NewExecutor(httpClient *http.Client, superGraph *graph.SuperGraph, option ExecutorOption) *executor {
	qb := NewQueryBuilder()
	e := &executor{
		QueryBuilder:               qb,
		superGraph:                 superGraph,
		typeDefinitions:            make(map[string]*ast.ObjectTypeDefinition),
		httpClient:                 httpClient,
		mux:                        sync.Mutex{},
		enableOpentelemetryTracing: option.EnableOpentelemetryTracing,
	}
	e.buildTypeDefinitionsCache()
	return e
}

func (e *executor) buildTypeDefinitionsCache() {
	if e.superGraph == nil || e.superGraph.Schema == nil {
		return
	}
	for _, def := range e.superGraph.Schema.Definitions {
		switch td := def.(type) {
		case *ast.ObjectTypeDefinition:
			name := td.Name.String()
			if existing, ok := e.typeDefinitions[name]; ok {
				existing.Fields = append(existing.Fields, td.Fields...)
				existing.Directives = append(existing.Directives, td.Directives...)
				existing.Interfaces = append(existing.Interfaces, td.Interfaces...)
			} else {
				e.typeDefinitions[name] = td
			}
		case *ast.ObjectTypeExtension:
			name := td.Name.String()
			if existing, ok := e.typeDefinitions[name]; ok {
				existing.Fields = append(existing.Fields, td.Fields...)
				existing.Directives = append(existing.Directives, td.Directives...)
				existing.Interfaces = append(existing.Interfaces, td.Interfaces...)
			} else {
				e.typeDefinitions[name] = &ast.ObjectTypeDefinition{
					Name:       td.Name,
					Token:      td.Token,
					Directives: td.Directives,
					Fields:     td.Fields,
					Interfaces: td.Interfaces,
				}
			}
		}
	}
}

func getRootTypeName(doc *ast.Document, operation string) string {
	if doc != nil {
		for _, def := range doc.Definitions {
			if sd, ok := def.(*ast.SchemaDefinition); ok {
				for _, opDef := range sd.OperationTypes {
					opStr := ""
					switch opDef.Operation {
					case token.QUERY:
						opStr = "query"
					case token.MUTATION:
						opStr = "mutation"
					case token.SUBSCRIPTION:
						opStr = "subscription"
					}
					if opStr == operation {
						return opDef.Type.Name.String()
					}
				}
			}
		}
	}

	switch operation {
	case "query":
		return "Query"
	case "mutation":
		return "Mutation"
	case "subscription":
		return "Subscription"
	default:
		return "Query"
	}
}

func (e *executor) Execute(ctx context.Context, plan *planner.Plan, variables map[string]any) map[string]any {
	wg := sync.WaitGroup{}
	stepInputs := sync.Map{}
	mergedResponse := make(map[string]any)
	mergedResponse["data"] = make(map[string]any)
	mergedResponse["errors"] = make([]any, 0)
	var responseMux sync.Mutex
	ectx := &executionContext{
		concurrencyMap: make(map[int]chan struct{}),
		Errs:           make([]error, 0),
	}

	for _, step := range plan.Steps {
		if len(step.Selections) == 0 {
			continue
		}
		ectx.concurrencyMap[step.ID] = make(chan struct{})

		wg.Add(1)
		go func(step *planner.Step) {
			defer wg.Done()
			defer close(ectx.concurrencyMap[step.ID])
			e.waitDependStepEnded(ectx, plan, step)
			var currentRefs []entityRef
			var stepEntities Entities = make(Entities, 0)

			if len(step.DependsOn) != 0 {
				targetTypes := make(map[string]struct{})
				for _, sel := range step.Selections {
					targetTypes[sel.ParentType] = struct{}{}
				}

				meta := stepMetadata{
					repToRefIndices: make(map[string][]int),
					orderedRepKeys:  make([]string, 0),
				}

				for _, dependStepID := range step.DependsOn {
					value, ok := stepInputs.Load(dependStepID)
					if ok {
						refs := value.([]entityRef)
						for _, ref := range refs {
							if _, isTarget := targetTypes[ref.Typename]; isTarget {
								rep := ref.toRepresentation()
								repKey := fmt.Sprintf("%s:%v", ref.Typename, ref.Key)

								currentRefs = append(currentRefs, ref)
								refIdx := len(currentRefs) - 1

								if indices, exists := meta.repToRefIndices[repKey]; exists {
									meta.repToRefIndices[repKey] = append(indices, refIdx)
								} else {
									meta.repToRefIndices[repKey] = []int{refIdx}
									meta.orderedRepKeys = append(meta.orderedRepKeys, repKey)
									stepEntities = append(stepEntities, rep)
								}
							}
						}
					}
				}

				step.Metadata = meta
			}

			query, builtVariables, err := e.QueryBuilder.Build(step, stepEntities, variables)
			if err != nil {
				e.mux.Lock()
				ectx.Errs = append(ectx.Errs, err)
				e.mux.Unlock()
				return
			}

			resp, err := e.doRequest(ctx, step.SubGraph.Host, query, builtVariables)
			if err != nil {
				e.mux.Lock()
				ectx.Errs = append(ectx.Errs, err)
				e.mux.Unlock()
				return
			}

			errorsResp, ok := resp["errors"].([]any)
			if ok {
				e.mux.Lock()
				errs, ok := mergedResponse["errors"]
				if !ok {
					errs = make([]any, 0)
					mergedResponse["errors"] = errs
				} else {
					mergedResponse["errors"] = append(mergedResponse["errors"].([]any), errorsResp...)
				}
				e.mux.Unlock()
			}

			data, ok := resp["data"].(map[string]any)
			if !ok {
				if len(step.DependsOn) == 0 {
					e.mux.Lock()
					ectx.Errs = append(ectx.Errs, errors.New("no data in response"))
					e.mux.Unlock()
				}
				return
			}

			if len(step.DependsOn) == 0 {
				responseMux.Lock()
				// We need to merge initial data into mergedResponse immediately
				// so that later steps can collect refs from it.
				for k, v := range data {
					mergedResponse["data"].(map[string]any)[k] = v
				}

				rootTypeName := getRootTypeName(e.superGraph.Schema, plan.OperationType)
				refs, err := e.CollectEntityRefs(data, step.RootFields, rootTypeName, Path{})
				if err != nil {
					ectx.Errs = append(ectx.Errs, err)
					responseMux.Unlock()
					return
				}

				responseMux.Unlock()
				stepInputs.Store(step.ID, refs)
			} else {
				entitiesData, ok := data["_entities"].([]any)
				if ok {
					responseMux.Lock()
					err := e.mergeEntitiesResponse(mergedResponse, step, currentRefs, entitiesData)
					if err != nil {
						ectx.Errs = append(ectx.Errs, err)
						responseMux.Unlock()
						return
					}

					mergedData := mergedResponse["data"].(map[string]any)
					rootTypeName := getRootTypeName(e.superGraph.Schema, plan.OperationType)
					newRefs, err := e.CollectEntityRefs(mergedData, plan.RootSelections, rootTypeName, Path{})

					if err != nil {
						ectx.Errs = append(ectx.Errs, err)
						responseMux.Unlock()
						return
					}
					responseMux.Unlock()
					stepInputs.Store(step.ID, newRefs)
				} else {
					// Fallback: If no _entities but data exists (rare but for robustness)
					mergedData := mergedResponse["data"].(map[string]any)
					rootTypeName := getRootTypeName(e.superGraph.Schema, plan.OperationType)
					newRefs, _ := e.CollectEntityRefs(mergedData, plan.RootSelections, rootTypeName, Path{})
					stepInputs.Store(step.ID, newRefs)
				}
			}
		}(step)
	}

	wg.Wait()

	errors := make([]string, 0)
	responseErrors, ok := mergedResponse["errors"].([]string)
	if ok {
		errors = append(errors, responseErrors...)
	}

	for _, err := range ectx.Errs {
		slog.Error("failed to execute step", "error", err)
		errors = append(errors, err.Error())
	}

	if len(errors) > 0 {
		mergedResponse["errors"] = errors
	}

	return e.pruneResponse(mergedResponse, plan.RootSelections)
}

func (e *executor) mergeEntitiesResponse(resp map[string]any, step *planner.Step, refs []entityRef, entitiesData []any) error {
	data := resp["data"].(map[string]any)

	if meta, ok := step.Metadata.(stepMetadata); ok {
		if len(meta.orderedRepKeys) != len(entitiesData) {
			return fmt.Errorf("mismatched number of unique reps (%d) and entities data (%d)", len(meta.orderedRepKeys), len(entitiesData))
		}

		for i, entityResult := range entitiesData {
			if entityResult == nil {
				continue
			}

			resultMap, ok := entityResult.(map[string]any)
			if !ok {
				continue
			}

			repKey := meta.orderedRepKeys[i]
			refIndices := meta.repToRefIndices[repKey]

			for _, refIdx := range refIndices {
				ref := refs[refIdx]
				targetObj := getObjectFromPath(ref.Path, data)

				if targetObj != nil {
					targetObjMap, ok := targetObj.(map[string]any)
					if !ok {
						continue
					}

					for k, v := range resultMap {
						targetObjMap[k] = v
					}
				}
			}
		}
		return nil
	}

	// Simple case: no deduplication or we match 1-to-1
	if len(refs) == len(entitiesData) {
		for i, entityResult := range entitiesData {
			if entityResult == nil {
				continue
			}

			ref := refs[i]
			resultMap, ok := entityResult.(map[string]any)
			if !ok {
				continue
			}
			targetObj := getObjectFromPath(ref.Path, data)

			if targetObj != nil {
				targetObjMap, ok := targetObj.(map[string]any)
				if !ok {
					continue
				}

				for k, v := range resultMap {
					targetObjMap[k] = v
				}
			}
		}
		return nil
	}

	return fmt.Errorf("mismatched number of entity refs (%d) and entities data (%d)", len(refs), len(entitiesData))
}

func (e *executor) pruneResponse(resp map[string]any, rootSelections []*planner.Selection) map[string]any {
	data, ok := resp["data"].(map[string]any)
	if !ok {
		slog.Error("no data in response")
		return map[string]any{"data": nil, "errors": resp["errors"]}
	}

	var prune func(obj any, sels []*planner.Selection) any

	prune = func(obj any, sels []*planner.Selection) any {
		if obj == nil {
			return nil
		}

		if len(sels) == 0 {
			return obj
		}

		switch v := obj.(type) {
		case map[string]any:
			prunedObj := make(map[string]any)

			for _, sel := range sels {
				lookupKey := sel.Field
				if sel.Alias != "" {
					lookupKey = sel.Alias
				}

				val, exists := v[lookupKey]

				if !exists && lookupKey != sel.Field {
					val, exists = v[sel.Field]
				}

				if !exists {
					continue
				}

				prunedObj[lookupKey] = prune(val, sel.SubSelections)
			}

			return prunedObj

		case []any:
			prunedArr := make([]any, 0, len(v))
			for _, elem := range v {
				prunedArr = append(prunedArr, prune(elem, sels))
			}
			return prunedArr

		default:
			return v
		}
	}

	prunedResult, ok := prune(data, rootSelections).(map[string]any)
	if !ok {
		return map[string]any{"data": nil, "errors": resp["errors"]}
	}

	return map[string]any{"data": prunedResult, "errors": resp["errors"]}
}

type PathSegment struct {
	FieldName string
	Index     *int
}

type Path []*PathSegment

func (p Path) Merge() Path {
	merged := make(Path, 0, len(p))

	length := len(p)
	for i := 0; i < length; i++ {
		if i < length-1 && p[i+1].Index != nil {
			merged = append(merged, &PathSegment{
				FieldName: p[i].FieldName,
				Index:     p[i+1].Index,
			})
			i++
		} else {
			merged = append(merged, p[i])
		}
	}

	return merged
}

func appendPath(p Path, seg *PathSegment) Path {
	next := make(Path, len(p), len(p)+1)
	copy(next, p)
	return append(next, seg)
}

type entityRef struct {
	Typename    string
	Key         any
	Path        Path
	ExtraFields map[string]any
}

// 【Fix: toRepresentation should only include Key and __typename】
func (e entityRef) toRepresentation() map[string]any {
	ret := make(map[string]any)
	for k, v := range e.Key.(map[string]any) {
		ret[k] = v
	}
	// DO NOT include ExtraFields here unless specifically required by @requires.
	// For standard Federation, only Keys are allowed.
	ret["__typename"] = e.Typename
	return ret
}

func getKey(directives []*ast.Directive) []string {
	for _, dir := range directives {
		if dir.Name == "key" {
			for _, arg := range dir.Arguments {
				if arg.Name.String() != "fields" {
					continue
				}

				v := strings.ReplaceAll(arg.Value.String(), "\"", "")
				return strings.Split(v, " ")
			}
		}
	}

	return nil
}

func (e *executor) waitDependStepEnded(ectx *executionContext, plan *planner.Plan, step *planner.Step) {
	for _, dependStepID := range step.DependsOn {
		dependsStep := plan.GetStepByID(dependStepID)
		<-ectx.concurrencyMap[dependsStep.ID]
	}
}

func (e *executor) doRequest(ctx context.Context, host string, query string, variables map[string]any) (map[string]any, error) {
	body := map[string]any{
		"query":     query,
		"variables": variables,
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host, bytes.NewBuffer(b))
	if err != nil {
		return nil, err
	}

	header := GetRequestHeaderFromContext(ctx)

	for k, values := range header {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var respBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return nil, err
	}

	return respBody, nil
}

func getObjectFromPath(path Path, obj any) any {
	if obj == nil {
		return nil
	}

	if len(path) == 0 {
		return obj
	}

	segment := path[0]
	var currentObj = obj
	if segment.FieldName != "" {
		if m, ok := currentObj.(map[string]any); ok {
			if val, exists := m[segment.FieldName]; exists {
				currentObj = val
			} else {
				return nil
			}
		} else {
			return nil
		}
	}

	if segment.Index != nil {
		if s, ok := currentObj.([]any); ok {
			idx := *segment.Index
			if idx >= 0 && idx < len(s) {
				currentObj = s[idx]
			} else {
				return nil
			}
		} else {
			return nil
		}
	}

	return getObjectFromPath(path[1:], currentObj)
}

func (e *executor) getTD(typeName string) *ast.ObjectTypeDefinition {
	return e.typeDefinitions[typeName]
}

func (e *executor) CollectEntityRefs(obj map[string]any, selections []*planner.Selection, parentTypeName string, currentPath Path) ([]entityRef, error) {
	refs := make([]entityRef, 0)
	td := e.getTD(parentTypeName)
	if td != nil {
		keys := getKey(td.Directives)
		if len(keys) > 0 {
			keyMap := make(map[string]any)
			missingKey := false
			for _, k := range keys {
				if val, ok := obj[k]; ok {
					keyMap[k] = val
				} else {
					missingKey = true
					break
				}
			}

			if !missingKey {
				refs = append(refs, entityRef{
					Typename:    parentTypeName,
					Key:         keyMap,
					Path:        currentPath,
					ExtraFields: obj,
				})
			}
		}
	}

	for _, sel := range selections {
		lookupKey := sel.Field
		if sel.Alias != "" {
			lookupKey = sel.Alias
		}

		val, exists := obj[lookupKey]
		if !exists || val == nil {
			continue
		}

		nextTypeName := e.getFieldTypeName(parentTypeName, sel.Field)
		if nextTypeName == "" {
			continue
		}

		nextPath := appendPath(currentPath, &PathSegment{FieldName: lookupKey})

		switch v := val.(type) {
		case map[string]any:
			childRefs, err := e.CollectEntityRefs(v, sel.SubSelections, nextTypeName, nextPath)
			if err != nil {
				return nil, err
			}
			refs = append(refs, childRefs...)
		case []any:
			for i, item := range v {
				if itemMap, ok := item.(map[string]any); ok {
					idx := i
					itemPath := appendPath(currentPath, &PathSegment{FieldName: lookupKey, Index: &idx})
					childRefs, err := e.CollectEntityRefs(itemMap, sel.SubSelections, nextTypeName, itemPath)
					if err != nil {
						return nil, err
					}
					refs = append(refs, childRefs...)
				}
			}
		}
	}

	return refs, nil
}

func (e *executor) getFieldTypeName(parentTypeName string, fieldName string) string {
	td := e.getTD(parentTypeName)
	if td == nil {
		return ""
	}

	for _, f := range td.Fields {
		if f.Name.String() == fieldName {
			return getNamedType(f.Type)
		}
	}
	return ""
}

func getNamedType(t ast.Type) string {
	switch typ := t.(type) {
	case *ast.NamedType:
		return typ.Name.String()
	case *ast.ListType:
		return getNamedType(typ.Type)
	case *ast.NonNullType:
		return getNamedType(typ.Type)
	default:
		return ""
	}
}

type requestHeaderContextKey struct{}

func SetRequestHeaderToContext(ctx context.Context, header http.Header) context.Context {
	return context.WithValue(ctx, requestHeaderContextKey{}, header)
}

func GetRequestHeaderFromContext(ctx context.Context) http.Header {
	h, ok := ctx.Value(requestHeaderContextKey{}).(http.Header)
	if !ok {
		return nil
	}

	return h
}
