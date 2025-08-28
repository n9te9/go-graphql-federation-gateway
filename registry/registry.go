package registry

import (
	"io"
	"net/http"
	"sync/atomic"

	"github.com/n9te9/federation-gateway/gateway"
)

type Registry struct {
	gatewayEndpoint string
	currentGateway  atomic.Value
	nextGateway     atomic.Value
	registerChan    chan struct{}
}

func NewRegistry(gatewayEndpoint string, initializeGateway http.Handler) *Registry {
	currentGateway := atomic.Value{}
	currentGateway.Store(initializeGateway)

	return &Registry{
		gatewayEndpoint: gatewayEndpoint,
		currentGateway:  currentGateway,
		nextGateway:     atomic.Value{},
		registerChan:    make(chan struct{}),
	}
}

func (r *Registry) Start() {
	for range r.registerChan {
		// TODO: implement graceful shut-down for currentGateway.
		r.currentGateway.Store(r.nextGateway.Load().(http.Handler))
	}
}

func (r *Registry) AppliedGateway() http.Handler {
	return r.currentGateway.Load().(http.Handler)
}

func (r *Registry) RegisterGateway(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	currentGateway := r.currentGateway.Load().(http.Handler)
	nextGateway, err := gateway.GenerateNextGateway(currentGateway, body)
	if err != nil {
		http.Error(w, "Failed to generate next gateway", http.StatusInternalServerError)
		return
	}

	r.nextGateway.Store(nextGateway)

	// register the new gateway
	r.registerChan <- struct{}{}
}
