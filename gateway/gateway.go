package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/n9te9/federation-gateway/federation/executor"
	"github.com/n9te9/federation-gateway/federation/graph"
	"github.com/n9te9/federation-gateway/federation/planner"
	"github.com/n9te9/goliteql/query"
)

type GatewayService struct {
	Name   string `yaml:"name"`
	Host   string `yaml:"host"`
	Schema string `yaml:"schema"`
}
type GatewaySetting struct {
	Endpoint        string           `yaml:"endpoint"`
	Port            int              `yaml:"port"`
	TimeoutDuration string           `yaml:"timeout_duration" default:"5s"`
	Services        []GatewayService `yaml:"services"`
}
type gateway struct {
	graphQLEndpoint string
	planner         planner.Planner
	executor        executor.Executor
	superGraph      *graph.SuperGraph
	queryParser     *query.Parser
}

var _ http.Handler = (*gateway)(nil)

func NewGateway(settings *GatewaySetting) (*gateway, error) {
	subGraphs := make([]*graph.SubGraph, 0, len(settings.Services))
	allSchemaSrc := []byte{}

	for _, srv := range settings.Services {
		subGraph, err := graph.NewSubGraph(srv.Name, []byte(srv.Schema), srv.Host)
		if err != nil {
			return nil, fmt.Errorf("failed to create subgraph for service %s: %w", srv.Name, err)
		}
		subGraphs = append(subGraphs, subGraph)
		allSchemaSrc = append(allSchemaSrc, []byte(srv.Schema+"\n")...)
	}

	superGraph, err := graph.NewSuperGraph(allSchemaSrc, subGraphs)
	if err != nil {
		return nil, fmt.Errorf("failed to create supergraph: %w", err)
	}

	return &gateway{
		graphQLEndpoint: settings.Endpoint,
		superGraph:      superGraph,
		planner:         planner.NewPlanner(superGraph),
		executor:        executor.NewExecutor(&http.Client{}),
		queryParser:     query.NewParserWithLexer(),
	}, nil
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case g.graphQLEndpoint:
		g.Routing(w, r)
	}
}

type Request struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func (g *gateway) Routing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	document, err := g.queryParser.Parse([]byte(req.Query))
	if err != nil {
		http.Error(w, "Failed to parse query", http.StatusBadRequest)
		return
	}

	plan, err := g.planner.Plan(document)
	if err != nil {
		http.Error(w, "Failed to create execution plan", http.StatusInternalServerError)
		return
	}

	resp, err := g.executor.Execute(r.Context(), plan, req.Variables)
	if err != nil {
		http.Error(w, "Failed to execute query", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}
