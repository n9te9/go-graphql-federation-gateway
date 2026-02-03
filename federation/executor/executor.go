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
	"github.com/n9te9/goliteql/schema"
)

type Executor interface {
	Execute(ctx context.Context, plan *planner.Plan, variables map[string]any) map[string]any
}

type executor struct {
	QueryBuilder

	superGraph *graph.SuperGraph
	httpClient *http.Client
	mux        sync.Mutex
}

var _ Executor = (*executor)(nil)

func NewExecutor(httpClient *http.Client, superGraph *graph.SuperGraph) *executor {
	qb := NewQueryBuilder()
	return &executor{
		QueryBuilder: qb,
		superGraph:   superGraph,
		httpClient:   httpClient,
		mux:          sync.Mutex{},
	}
}

func (e *executor) Execute(ctx context.Context, plan *planner.Plan, variables map[string]any) map[string]any {
	wg := sync.WaitGroup{}
	entities := make(Entities, 0)
	stepInputs := sync.Map{}
	mergedResponse := make(map[string]any)
	mergedResponse["errors"] = make([]any, 0)

	for _, step := range plan.Steps {
		if len(step.Selections) == 0 {
			continue
		}

		wg.Add(1)
		go func(step *planner.Step) {
			defer wg.Done()
			defer close(step.Done)
			e.waitDependStepEnded(plan, step)
			var reqVariables map[string]any
			var currentRefs []entityRef

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
					} else {
						step.Err = errors.New("no entity refs for dependent step")
						return
					}
				}

				entities = make(Entities, 0)
				for _, ref := range currentRefs {
					entities = append(entities, ref.toRepresentation())
				}
			}

			query, builtVariables, err := e.QueryBuilder.Build(step, entities)
			if err != nil {
				step.Err = err
				return
			}

			if len(step.DependsOn) == 0 {
				reqVariables = variables
			} else {
				reqVariables = builtVariables
			}

			resp, err := e.doRequest(ctx, step.SubGraph.Host, query, reqVariables)
			if err != nil {
				step.Err = err
				return
			}

			errorsResp, ok := resp["errors"].([]any)
			if ok {
				e.mux.Lock()
				mergedResponse["errors"] = append(mergedResponse["errors"].([]any), errorsResp...)
				e.mux.Unlock()
			}

			if len(step.DependsOn) == 0 {
				if resp["data"] == nil {
					step.Err = errors.New("no data in response")
					return
				}

				e.mux.Lock()
				mergedResponse = resp
				e.mux.Unlock()

				paths := BuildPaths(resp["data"])
				refs, err := CollectEntityRefs(paths, resp["data"].(map[string]any), e.superGraph.Schema)
				if err != nil {
					step.Err = err
					return
				}
				stepInputs.Store(step.ID, refs)

			} else {
				for _, dependStepID := range step.DependsOn {
					if _, ok := stepInputs.Load(dependStepID); !ok {
						step.Err = errors.New("no entity refs for dependent step")
						return
					}
				}

				data, ok := resp["data"].(map[string]any)
				if !ok {
					step.Err = errors.New("no data in response")
					return
				}
				entitiesData, ok := data["_entities"].([]any)
				if ok {
					e.mux.Lock()
					err := e.mergeEntitiesResponse(mergedResponse, currentRefs, entitiesData)
					if err != nil {
						e.mux.Unlock()
						step.Err = err
						return
					}

					mergedData := mergedResponse["data"]
					paths := BuildPaths(mergedData)
					newRefs, err := CollectEntityRefs(paths, mergedData.(map[string]any), e.superGraph.Schema)
					e.mux.Unlock()

					if err != nil {
						step.Err = err
						return
					}

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

	for _, step := range plan.Steps {
		if step.Err != nil {
			slog.Error("failed to execute step", "error", step.Err)
			errors = append(errors, step.Err.Error())
		}
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
				val, exists := v[sel.Field]
				if !exists {
					continue
				}

				prunedObj[sel.Field] = prune(val, sel.SubSelections)
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

func getKey(directives []*schema.Directive) []string {
	for _, dir := range directives {
		if string(dir.Name) == "key" {
			for _, arg := range dir.Arguments {
				if string(arg.Name) != "fields" {
					continue
				}

				if len(dir.Arguments) == 0 {
					return nil
				}

				v := strings.ReplaceAll(string(arg.Value), "\"", "")
				return strings.Split(v, " ")
			}
		}
	}

	return nil
}

func (e *executor) waitDependStepEnded(plan *planner.Plan, step *planner.Step) {
	for _, dependStepID := range step.DependsOn {
		dependsStep := plan.GetStepByID(dependStepID)
		<-dependsStep.Done
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

func getTypeDefinitionFromPath(path Path, s *schema.Schema) *schema.TypeDefinition {
	if len(path) == 0 {
		return nil
	}

	var currentTD *schema.TypeDefinition
	for _, op := range s.Operations {
		for _, f := range op.Fields {
			if string(f.Name) == path[0].FieldName {
				currentTD = s.Indexes.TypeIndex[string(f.Type.GetRootType().Name)]
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

	currentTypeName := string(currentTD.Name)

	for _, seg := range path[1:] {
		if seg.FieldName == "" {
			continue
		}

		td, ok := s.Indexes.TypeIndex[currentTypeName]
		if !ok {
			return nil
		}

		var nextType *schema.TypeDefinition
		for _, f := range td.Fields {
			if string(f.Name) == seg.FieldName {
				nextType = s.Indexes.TypeIndex[string(f.Type.GetRootType().Name)]
				break
			}
		}

		if nextType == nil {
			return nil
		}

		currentTD = nextType
		currentTypeName = string(nextType.Name)
	}

	return currentTD
}

func CollectEntityRefs(paths Paths, obj map[string]any, s *schema.Schema) ([]entityRef, error) {
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

		td := getTypeDefinitionFromPath(parentPath, s)
		if td == nil {
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
			Typename:    string(td.Name),
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
