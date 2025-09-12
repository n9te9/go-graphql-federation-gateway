package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/n9te9/federation-gateway/federation"
	"github.com/n9te9/federation-gateway/registry"
	"github.com/n9te9/goliteql/query"
)

type gateway struct {
	superGraph  *federation.SuperGraph
	queryParser *query.Parser
}

var _ http.Handler = (*gateway)(nil)

func NewGateway() *gateway {
	return &gateway{
		superGraph:  &federation.SuperGraph{},
		queryParser: query.NewParserWithLexer(),
	}
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/schema/registeration":
		if r.Method == http.MethodPost {
			g.RegisterSchema(w, r)
		}
	case "/graphql":
		g.Routing(w, r)
	}
}

func (g *gateway) RegisterSchema(w http.ResponseWriter, r *http.Request) {
	reqs := make([]*registry.RegistrationGraph, 0)
	if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	for _, req := range reqs {
		subgraph, err := federation.NewSubGraph(req.Name, []byte(req.SDL), req.Host)
		if err != nil {
			http.Error(w, "Failed to create subgraph", http.StatusBadRequest)
			return
		}

		if err := g.superGraph.Merge(subgraph); err != nil {
			http.Error(w, "Failed to merge subgraph", http.StatusBadRequest)
			return
		}
	}
}

type Request struct {
	Query     []byte                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
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

	document, err := g.queryParser.Parse(req.Query)
	if err != nil {
		http.Error(w, "Failed to parse query", http.StatusBadRequest)
		return
	}

	g.superGraph.Execute(r.Context(), document, req.Variables)
}
