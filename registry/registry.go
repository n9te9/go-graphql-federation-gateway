package registry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/n9te9/federation-gateway/registry/federation"
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
		r.addHostChan <- rg.Host
		registratedGraphs = append(registratedGraphs, federation.NewSubGraph(rg.Name, rg.Host, rg.SDL))
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

func (r *Registry) RegisterSubGraph(name, host string, sdl []byte) error {
	registratedGraphs := r.registratedGraph.Load().([]*federation.SubGraph)
	registratedGraphs = append(registratedGraphs, federation.NewSubGraph(name, host, string(sdl)))
	r.registratedGraph.Store(registratedGraphs)

	gatewayHosts := r.gatewayHosts.Load().(map[string]struct{})
	for sgHost := range gatewayHosts {
		body := RegistrationRequest{
			RegistrationGraphs: []RegistrationGraph{
				{
					Name: name,
					Host: host,
					SDL:  string(sdl),
				},
			},
		}
		reqBody, err := json.Marshal(body)
		if err != nil {
			return err
		}

		registerGatewayRequest, err := http.NewRequest(http.MethodPost, sgHost+"/schema/registration", bytes.NewBuffer(reqBody))
		if err != nil {
			return err
		}

		go func() {
			if _, err := r.client.Do(registerGatewayRequest); err != nil {
				return
			}
		}()
	}

	return nil
}
