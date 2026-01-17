package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/n9te9/federation-gateway/federation/planner"
	"github.com/n9te9/goliteql/schema"
)

type Executor interface {
	Execute(ctx context.Context, plan *planner.Plan, variables map[string]any) (map[string]any, error)
}

type executor struct {
	QueryBuilder
	httpClient *http.Client
	mux        sync.Mutex
}

var _ Executor = (*executor)(nil)

func NewExecutor(httpClient *http.Client) *executor {
	qb := NewQueryBuilder()
	return &executor{
		QueryBuilder: qb,
		httpClient:   httpClient,
		mux:          sync.Mutex{},
	}
}

func (e *executor) Execute(ctx context.Context, plan *planner.Plan, vareiables map[string]any) (map[string]any, error) {
	wg := sync.WaitGroup{}
	entities := make(Entities, 0)
	stepInputs := sync.Map{}
	mergedResponse := make(map[string]any)
	for _, step := range plan.Steps {
		wg.Add(1)
		go func(step *planner.Step) {
			defer wg.Done()
			e.waitDependStepEnded(plan, step)

			var reqVariables map[string]any
			var currentRefs []entityRef

			if !step.SubGraph.IsBase {
				for _, dependStepID := range step.DependsOn {
					value, ok := stepInputs.Load(dependStepID)
					if ok {
						currentRefs = value.([]entityRef)
					} else {
						step.Err = errors.New("no entity refs for dependent step")
						close(step.Done)
					}

					entities = make(Entities, 0)
					for _, ref := range currentRefs {
						entities = append(entities, ref.toRepresentation())
					}
				}
			}

			query, builtVariables, err := e.QueryBuilder.Build(step, entities)
			if err != nil {
				step.Err = err
			}

			if step.SubGraph.IsBase {
				reqVariables = vareiables
			} else {
				reqVariables = builtVariables
			}

			resp, err := e.doRequest(ctx, step.SubGraph.Host, query, reqVariables)
			if err != nil {
				step.Err = err
			}

			if step.SubGraph.IsBase {
				if resp["data"] == nil {
					step.Err = errors.New("no data in response")
					return
				}

				e.mux.Lock()
				mergedResponse = resp
				e.mux.Unlock()

				paths := BuildPaths(resp["data"])
				refs, err := CollectEntityRefs(paths, resp["data"].(map[string]any), step.SubGraph.Schema)
				if err != nil {
					step.Err = err
					close(step.Done)
				}
				stepInputs.Store(step.ID, refs)
			} else {
				for _, dependStepID := range step.DependsOn {
					value, ok := stepInputs.Load(dependStepID)
					if ok {
						currentRefs = value.([]entityRef)
					} else {
						step.Err = errors.New("no entity refs for dependent step")
						close(step.Done)
					}
				}

				entitiesData, ok := resp["data"].(map[string]any)["_entities"].([]any)
				if ok {
					e.mux.Lock()
					defer e.mux.Unlock()
					err := e.mergeEntitiesResponse(mergedResponse, currentRefs, entitiesData)
					if err != nil {
						step.Err = err
					}
				}
			}
			close(step.Done)

		}(step)
	}

	wg.Wait()
	return mergedResponse, nil
}

func (e *executor) mergeEntitiesResponse(resp map[string]any, refs []entityRef, entitiesData []any) error {
	if len(refs) != len(entitiesData) {
		return errors.New("mismatched number of entity refs and entities data")
	}

	data := resp["data"].(map[string]any)

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
			for k, v := range resultMap {
				targetObj[k] = v
			}
		}
	}

	return nil
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
	Typename string
	Key      map[string]any
	Path     Path
}

func (e entityRef) toRepresentation() map[string]any {
	ret := make(map[string]any)
	for k, v := range e.Key {
		ret[k] = v
	}
	ret["__typename"] = e.Typename
	return ret
}

func CollectEntityRefs(paths Paths, obj map[string]any, s *schema.Schema) ([]entityRef, error) {
	refs := make([]entityRef, 0)

	for _, path := range paths {
		var td *schema.TypeDefinition

		if len(path) == 0 {
			continue
		}

		segment := path[0]
		td = getTypeFromOperation(segment, s)
		if td == nil {
			continue
		}

		keys := getKey(td.Directives)
		if keys == nil {
			continue
		}

		if !path.isKeySegment(keys) {
			continue
		}

		keyValue := getObjectFromPath(path, obj)
		entityRef := entityRef{
			Typename: string(td.Name),
			Key:      keyValue,
			Path:     path,
		}
		refs = append(refs, entityRef)
	}

	return refs, nil
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

func getTypeFromOperation(segment *PathSegment, s *schema.Schema) *schema.TypeDefinition {
	for _, operations := range s.Operations {
		for _, field := range operations.Fields {
			if string(field.Name) == segment.FieldName {
				rootType := field.Type.GetRootType()
				td := s.Indexes.TypeIndex[string(rootType.Name)]
				return td
			}
		}
	}

	return nil
}

func getObjectFromPath(path Path, obj any) map[string]any {
	switch v := obj.(type) {
	case []any:
		if path[0].Index != nil {
			return getObjectFromPath(path[1:], v[*path[0].Index])
		}
	case map[string]any:
		if len(path) == 1 {
			return v
		} else {
			return getObjectFromPath(path, v[path[0].FieldName])
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

func (e *executor) fetchEntities(step *planner.Step, resp map[string]any) (Entities, error) {
	data, ok := resp["data"].(map[string]any)
	if !ok {
		return nil, nil
	}

	entities, ok := data["_entities"].([]any)
	if !ok {
		return nil, nil
	}

	result := make(Entities, 0)
	for _, entity := range entities {
		entityMap, ok := entity.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, entityMap)
	}

	return result, nil
}
