package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type GatewayService struct {
	Name        string   `yaml:"name"`
	Host        string   `yaml:"host"`
	SchemaFiles []string `yaml:"schema_files"`
}

type GatewayOption struct {
	Endpoint                    string               `yaml:"endpoint"`
	ServiceName                 string               `yaml:"service_name"`
	Port                        int                  `yaml:"port"`
	TimeoutDuration             string               `yaml:"timeout_duration" default:"5s"`
	EnableHangOverRequestHeader bool                 `yaml:"enable_hang_over_request_header" default:"true"`
	Services                    []GatewayService     `yaml:"services"`
	Opentelemetry               OpentelemetrySetting `yaml:"opentelemetry"`
}

type OpentelemetrySetting struct {
	TracingSetting OpentelemetryTracingSetting `yaml:"tracing"`
}

type OpentelemetryTracingSetting struct {
	Enable bool `yaml:"enable" default:"false"`
}

type gateway struct {
	graphQLEndpoint string
	serviceName     string
	planner         *planner.PlannerV2
	executor        *executor.ExecutorV2
	superGraph      *graph.SuperGraphV2

	enableComplementRequestId   bool
	enableHangOverRequestHeader bool
	enableOpentelemetryTracing  bool
}

var _ http.Handler = (*gateway)(nil)

func NewGateway(settings GatewayOption) (*gateway, error) {
	var subGraphs []*graph.SubGraphV2
	for _, s := range settings.Services {
		var schema []byte
		for _, f := range s.SchemaFiles {
			src, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			schema = append(schema, src...)
		}

		subGraph, err := graph.NewSubGraphV2(s.Name, schema, s.Host)
		if err != nil {
			return nil, err
		}

		subGraphs = append(subGraphs, subGraph)
	}

	superGraph, err := graph.NewSuperGraphV2(subGraphs)
	if err != nil {
		return nil, err
	}

	// Create HTTP client with timeout for subgraph requests
	httpClient := &http.Client{
		Timeout: 3 * time.Second, // 3 second timeout for subgraph requests
	}

	if settings.Opentelemetry.TracingSetting.Enable {
		httpClient.Transport = otelhttp.NewTransport(http.DefaultTransport)
	}

	return &gateway{
		graphQLEndpoint:             settings.Endpoint,
		serviceName:                 settings.ServiceName,
		planner:                     planner.NewPlannerV2(superGraph),
		executor:                    executor.NewExecutorV2(httpClient, superGraph),
		superGraph:                  superGraph,
		enableComplementRequestId:   true,
		enableHangOverRequestHeader: settings.EnableHangOverRequestHeader,
		enableOpentelemetryTracing:  settings.Opentelemetry.TracingSetting.Enable,
	}, nil
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req graphQLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if g.enableHangOverRequestHeader {
		ctx = executor.SetRequestHeaderToContext(ctx, r.Header)
	}

	l := lexer.New(req.Query)
	p := parser.New(l)
	doc := p.ParseDocument()
	if len(p.Errors()) > 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"errors": p.Errors(),
		})
		return
	}

	plan, err := g.planner.Plan(doc, req.Variables)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []string{err.Error()},
		})
		return
	}

	resp, err := g.executor.Execute(ctx, plan, req.Variables)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []string{err.Error()},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (g *gateway) Start(port int) error {
	fmt.Printf("Gateway started on port %d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), g)
}
