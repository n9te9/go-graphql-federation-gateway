package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
)

type executionContext struct {
	concurrencyMap map[int]chan struct{}
	Errs           []error
}

type Executor interface {
	Execute(ctx context.Context, plan *planner.Plan, variables map[string]any) map[string]any
}

type executor struct {
	QueryBuilder

	superGraph                 *graph.SuperGraph
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
	return &executor{
		QueryBuilder:               qb,
		superGraph:                 superGraph,
		httpClient:                 httpClient,
		mux:                        sync.Mutex{},
		enableOpentelemetryTracing: option.EnableOpentelemetryTracing,
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

				for _, dependStepID := range step.DependsOn {
					value, ok := stepInputs.Load(dependStepID)
					if ok {
						refs := value.([]entityRef)
						for _, ref := range refs {
							if _, isTarget := targetTypes[ref.Typename]; isTarget {
								currentRefs = append(currentRefs, ref)
							}
						}
					}
				}

				for _, ref := range currentRefs {
					stepEntities = append(stepEntities, ref.toRepresentation())
				}
			}

			if len(step.DependsOn) != 0 && len(stepEntities) == 0 {
				return
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
				for k, v := range data {
					storeKey := k
					for _, sel := range step.Selections {
						if sel.Field == k {
							if sel.Alias != "" {
								storeKey = sel.Alias
							}
							break
						}
					}
					mergedResponse["data"].(map[string]any)[storeKey] = v
				}

				paths := BuildPaths(data)
				refs, err := CollectEntityRefs(paths, data, e.superGraph.Schema)
				slog.Debug("Extracted Refs", "stepID", step.ID, "refsCount", len(refs), "refs", refs)

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
					err := e.mergeEntitiesResponse(mergedResponse, currentRefs, entitiesData)
					if err != nil {
						ectx.Errs = append(ectx.Errs, err)
						responseMux.Unlock()
						return
					}

					mergedData := mergedResponse["data"].(map[string]any)
					paths := BuildPaths(mergedData)
					newRefs, err := CollectEntityRefs(paths, mergedData, e.superGraph.Schema)

					if err != nil {
						ectx.Errs = append(ectx.Errs, err)
						responseMux.Unlock()
						return
					}
					responseMux.Unlock()
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

func (e *executor) mergeEntitiesResponse(resp map[string]any, refs []entityRef, entitiesData []any) error {
	if len(refs) != len(entitiesData) {
		return fmt.Errorf("mismatched number of entity refs (%d) and entities data (%d)", len(refs), len(entitiesData))
	}

	data := resp["data"].(map[string]any)
	errors, ok := resp["errors"].([]any)
	if !ok {
		errors = make([]any, 0)
		resp["errors"] = errors
	}

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

func (p Path) isKeySegment(keys []string) bool {
	return slices.Contains(keys, p[len(p)-1].FieldName)
}

func BuildPaths(v any) []Path {
	var paths Paths

	var walk func(value any, path Path)

	walk = func(value any, path Path) {
		switch vv := value.(type) {
		case map[string]any:
			for field, child := range vv {
				next := appendPath(path, &PathSegment{FieldName: field})
				walk(child, next)
			}

		case []any:
			for i, elem := range vv {
				idx := i
				next := appendPath(path, &PathSegment{Index: &idx})
				walk(elem, next)
			}

		default:
			paths = append(paths, path)
		}
	}

	walk(v, Path{})
	return paths.Merge()
}

func appendPath(p Path, seg *PathSegment) Path {
	next := make(Path, len(p), len(p)+1)
	copy(next, p)
	return append(next, seg)
}

type Paths []Path

func (p Paths) Merge() Paths {
	length := len(p)
	merged := make(Paths, 0, length)
	for _, path := range p {
		merged = append(merged, path.Merge())
	}

	return merged
}

type entityRef struct {
	Typename    string
	Key         any
	Path        Path
	ExtraFields map[string]any
}

func (e entityRef) toRepresentation() map[string]any {
	ret := make(map[string]any)
	for k, v := range e.Key.(map[string]any) {
		ret[k] = v
	}

	for k, v := range e.ExtraFields {
		ret[k] = v
	}

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

func pathToString(path Path) string {
	var sb strings.Builder
	for _, seg := range path {
		sb.WriteString(seg.FieldName)
		if seg.Index != nil {
			sb.WriteString(fmt.Sprintf("[%d]", *seg.Index))
		}
		sb.WriteString("/")
	}
	return sb.String()
}

func getTypeDefinitionFromPath(path Path, doc *ast.Document) *ast.ObjectTypeDefinition {
	if len(path) == 0 {
		return nil
	}

	var rootTypeName string
	for _, def := range doc.Definitions {
		if sd, ok := def.(*ast.SchemaDefinition); ok {
			for _, ot := range sd.OperationTypes {
				if ot.Operation.String() == "query" {
					rootTypeName = ot.Type.Name.String()
				}
			}
		}
	}
	if rootTypeName == "" {
		rootTypeName = "Query"
	}

	var currentTD *ast.ObjectTypeDefinition
	for _, def := range doc.Definitions {
		var fields []*ast.FieldDefinition
		switch td := def.(type) {
		case *ast.ObjectTypeDefinition:
			if td.Name.String() == rootTypeName {
				fields = td.Fields
			}
		case *ast.ObjectTypeExtension:
			if td.Name.String() == rootTypeName {
				fields = td.Fields
			}
		}

		for _, f := range fields {
			if f.Name.String() == path[0].FieldName {
				currentTD = getTD(doc, getNamedType(f.Type))
				break
			}
		}
		if currentTD != nil {
			break
		}
	}

	if currentTD == nil {
		return nil
	}

	for _, seg := range path[1:] {
		if seg.FieldName == "" {
			continue
		}

		var nextType *ast.ObjectTypeDefinition
		for _, def := range doc.Definitions {
			var fields []*ast.FieldDefinition
			switch td := def.(type) {
			case *ast.ObjectTypeDefinition:
				if td.Name.String() == currentTD.Name.String() {
					fields = td.Fields
				}
			case *ast.ObjectTypeExtension:
				if td.Name.String() == currentTD.Name.String() {
					fields = td.Fields
				}
			}

			for _, f := range fields {
				if f.Name.String() == seg.FieldName {
					nextType = getTD(doc, getNamedType(f.Type))
					break
				}
			}
			if nextType != nil {
				break
			}
		}

		if nextType == nil {
			return nil
		}

		currentTD = nextType
	}

	return currentTD
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

func getTD(doc *ast.Document, typeName string) *ast.ObjectTypeDefinition {
	for _, def := range doc.Definitions {
		switch td := def.(type) {
		case *ast.ObjectTypeDefinition:
			if td.Name.String() == typeName {
				return td
			}
		case *ast.ObjectTypeExtension:
			if td.Name.String() == typeName {
				// While ObjectTypeExtension is not ObjectTypeDefinition,
				// we return the definition for the type if it exists.
				// For the purposes of field lookup, we might need a combined view.
				// But here we need a starting point TD.
				// Let's look for ObjectTypeDefinition first.
			}
		}
	}
	return nil
}

func CollectEntityRefs(paths Paths, obj map[string]any, doc *ast.Document) ([]entityRef, error) {
	refs := make([]entityRef, 0)
	seenPaths := make(map[string]struct{})

	for _, path := range paths {
		if len(path) == 0 {
			continue
		}

		parentPath := path[:len(path)-1]
		if len(parentPath) == 0 {
			continue
		}

		td := getTypeDefinitionFromPath(parentPath, doc)
		if td == nil {
			slog.Debug("type not found", "path", pathToString(parentPath))
			continue
		}

		keys := getKey(td.Directives)
		if len(keys) == 0 {
			continue
		}

		leafSegment := path[len(path)-1]
		if !slices.Contains(keys, leafSegment.FieldName) {
			continue
		}

		pathKey := pathToString(parentPath)
		if _, exists := seenPaths[pathKey]; exists {
			continue
		}
		seenPaths[pathKey] = struct{}{}

		parentObj := getObjectFromPath(parentPath, obj)
		parentMap, ok := parentObj.(map[string]any)
		if !ok {
			continue
		}

		keyMap := make(map[string]any)
		missingKey := false
		for _, k := range keys {
			if val, ok := parentMap[k]; ok {
				keyMap[k] = val
			} else {
				missingKey = true
				break
			}
		}
		if missingKey {
			continue
		}

		extraFields := make(map[string]any)
		for k, v := range parentMap {
			extraFields[k] = v
		}

		refs = append(refs, entityRef{
			Typename:    td.Name.String(),
			Key:         keyMap,
			Path:        parentPath,
			ExtraFields: extraFields,
		})
	}

	return refs, nil
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
