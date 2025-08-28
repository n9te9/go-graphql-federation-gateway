package gateway

import (
	"log"
	"net/http"

	"github.com/n9te9/federation-gateway/federation"
)

type gateway struct {
	superGraph *federation.SuperGraph
}

var _ http.Handler = (*gateway)(nil)

func NewGateway(initializeSchema []byte) (*gateway, error) {
	superGraph, err := federation.NewSuperGraphFromBytes(initializeSchema)
	if err != nil {
		return nil, err
	}

	return &gateway{
		superGraph: superGraph,
	}, nil
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {

}

func GenerateNextGateway(currentGateway http.Handler, src []byte) (http.Handler, error) {
	cg, ok := currentGateway.(*gateway)
	if !ok {
		log.Fatal("currentGateway is not a *gateway")
	}

	newSubGraph, err := federation.NewSubGraph(src)
	if err != nil {
		return nil, err
	}
	cg.superGraph.SubGraphs = append(cg.superGraph.SubGraphs, newSubGraph)

	nextSuperGraph := new(federation.SuperGraph)
	nextSuperGraph.Schema = cg.superGraph.Schema
	nextSuperGraph.SubGraphs = append(nextSuperGraph.SubGraphs, cg.superGraph.SubGraphs...)

	return &gateway{
		superGraph: nextSuperGraph,
	}, nil
}
