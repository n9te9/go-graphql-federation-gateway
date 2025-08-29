package gateway

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/n9te9/federation-gateway/gateway/federation"
	"github.com/n9te9/federation-gateway/registry"
	"github.com/n9te9/goliteql/schema"
)

type gateway struct {
	superGraph *federation.SuperGraph
}

var _ http.Handler = (*gateway)(nil)

func NewGateway() *gateway {
	return &gateway{
		superGraph: &federation.SuperGraph{},
	}
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/schema/register":
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
		s, err := schema.NewParser(schema.NewLexer()).Parse(req.SDL)
	}
}

func (g *gateway) Routing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

}
