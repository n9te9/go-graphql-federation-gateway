package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/n9te9/federation-gateway/federation/graph"
	"github.com/n9te9/federation-gateway/federation/planner"
	"github.com/n9te9/goliteql/query"
)

type gateway struct {
	graphQLEndpoint string
	planner         planner.Planner
	superGraph      *graph.SuperGraph
	queryParser     *query.Parser
}

var _ http.Handler = (*gateway)(nil)

func NewGateway(graphQLEndpoint string) *gateway {
	return &gateway{
		graphQLEndpoint: graphQLEndpoint,
		superGraph:      &graph.SuperGraph{},
		queryParser:     query.NewParserWithLexer(),
	}
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case g.graphQLEndpoint:
		g.Routing(w, r)
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

	g.planner.Plan(document)
}
