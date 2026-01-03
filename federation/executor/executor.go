package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/n9te9/federation-gateway/federation/planner"
)

type Executor interface {
	Execute(plan *planner.Plan) error
}

type executor struct {
	QueryBuilder
	httpClient *http.Client
	mux        sync.Mutex
}

func NewExecutor(httpClient *http.Client) *executor {
	qb := NewQueryBuilder()
	return &executor{
		QueryBuilder: qb,
		httpClient:   httpClient,
		mux:          sync.Mutex{},
	}
}

func (e *executor) Execute(ctx context.Context, plan *planner.Plan) ([]map[string]any, error) {
	wg := sync.WaitGroup{}
	entities := make(Entities, 0)
	result := make([]map[string]any, 0)
	for _, step := range plan.Steps {
		wg.Add(1)
		go func(step *planner.Step) {
			e.waitDependStepEnded(plan, step)

			query, variables, err := e.QueryBuilder.Build(step, entities)
			if err != nil {
				step.Err = err
			}

			resp, err := e.doRequest(ctx, step.SubGraph.Host, query, variables)
			if err != nil {
				step.Err = err
			}

			e.mux.Lock()
			if !step.SubGraph.IsBase {
				newEntities, err := e.fetchEntities(step, resp)
				if err != nil {
					step.Err = err
				}
				entities = append(entities, newEntities...)
			}
			result = append(result, resp)
			e.mux.Unlock()

			close(step.Done)

			wg.Done()
		}(step)
	}

	wg.Wait()
	return result, nil
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
