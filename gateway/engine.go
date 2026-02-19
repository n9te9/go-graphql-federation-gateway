package gateway

import (
	"fmt"
	"net/http"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
)

// executionEngine bundles all read-only components required to serve GraphQL requests.
type executionEngine struct {
	planner    *planner.PlannerV2
	executor   *executor.ExecutorV2
	superGraph *graph.SuperGraphV2
}

// schemaStore holds the current set of raw SDLs, host URLs, and the pre-built engine.
// It is stored in atomic.Value, so every value must be read-only after it is constructed.
type schemaStore struct {
	sdls   map[string]string // subgraph name → SDL string
	hosts  map[string]string // subgraph name → base URL
	engine *executionEngine
}

// buildEngine composes a new SuperGraph from the given SDLs and host map, then wraps it
// in an executionEngine together with a PlannerV2 and ExecutorV2.
// The order that subgraphs are processed follows the iteration order of sdls, which is
// non-deterministic in Go maps; SuperGraphV2 is expected to be order-independent.
func buildEngine(sdls, hosts map[string]string, httpClient *http.Client) (*executionEngine, error) {
	subGraphs := make([]*graph.SubGraphV2, 0, len(sdls))
	for name, sdl := range sdls {
		sg, err := graph.NewSubGraphV2(name, []byte(sdl), hosts[name])
		if err != nil {
			return nil, fmt.Errorf("failed to build subgraph %q: %w", name, err)
		}
		subGraphs = append(subGraphs, sg)
	}

	superGraph, err := graph.NewSuperGraphV2(subGraphs)
	if err != nil {
		return nil, fmt.Errorf("composition failed: %w", err)
	}

	return &executionEngine{
		planner:    planner.NewPlannerV2(superGraph),
		executor:   executor.NewExecutorV2(httpClient, superGraph),
		superGraph: superGraph,
	}, nil
}

// copyMap returns a shallow copy of a string map.
func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
