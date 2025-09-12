package registry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/n9te9/federation-gateway/federation"
)

type Registry struct {
	gatewayHosts     atomic.Value
	addHostChan      chan string
	registratedGraph atomic.Value
	client           *http.Client
}

func NewRegistry() *Registry {
	gatewayHosts := atomic.Value{}
	gatewayHosts.Store(make(map[string]struct{}))

	registratedGraph := atomic.Value{}
	registratedGraph.Store(make([]*federation.SubGraph, 0))

	return &Registry{
		gatewayHosts:     gatewayHosts,
		addHostChan:      make(chan string),
		registratedGraph: registratedGraph,
		client:           &http.Client{},
	}
}

func (r *Registry) Start() {
	go func() {
		for host := range r.addHostChan {
			r.addGatewayHost(host)
		}
	}()
}

func (r *Registry) addGatewayHost(host string) {
	gatewayHosts := r.gatewayHosts.Load().(map[string]struct{})
	gatewayHosts[host] = struct{}{}
	r.gatewayHosts.Store(gatewayHosts)
}

type RegistrationGraph struct {
	Name string `json:"name"`
	Host string `json:"host"`
	SDL  string `json:"sdl"`
}

type RegistrationRequest struct {
	RegistrationGraphs []RegistrationGraph `json:"registration_graphs"`
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/schema/registration":
		r.RegisterGateway(w, req)
	}
}

func (r *Registry) RegisterGateway(w http.ResponseWriter, req *http.Request) {
	var body RegistrationRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	registratedGraphs := r.registratedGraph.Load().([]*federation.SubGraph)
	for _, rg := range body.RegistrationGraphs {
		subGraph, err := federation.NewSubGraph(rg.Name, []byte(rg.SDL), rg.Host)
		if err != nil {
			http.Error(w, "Failed to create subgraph", http.StatusBadRequest)
			return
		}

		r.addHostChan <- rg.Host
		registratedGraphs = append(registratedGraphs, subGraph)
	}

	gatewayHosts := r.gatewayHosts.Load().(map[string]struct{})
	for sgHost := range gatewayHosts {
		reqBody, err := json.Marshal(body)
		if err != nil {
			http.Error(w, "Failed to marshal request body", http.StatusInternalServerError)
			return
		}

		registerGatewayRequest, err := http.NewRequestWithContext(req.Context(), http.MethodPost, sgHost+"/schema/registration", bytes.NewBuffer(reqBody))
		if err != nil {
			http.Error(w, "Failed to create gateway request", http.StatusInternalServerError)
			return
		}

		go func() {
			if _, err := r.client.Do(registerGatewayRequest); err != nil {
				http.Error(w, "Failed to register gateway", http.StatusInternalServerError)
				return
			}
		}()
	}

	r.registratedGraph.Store(registratedGraphs)
}
