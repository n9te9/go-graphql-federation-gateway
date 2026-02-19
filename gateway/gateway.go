package gateway

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// GatewayService describes a single upstream subgraph.
type GatewayService struct {
	Name  string      `yaml:"name"`
	Host  string      `yaml:"host"`
	Retry RetryOption `yaml:"retry"`
}

// GatewayOption is the top-level configuration loaded from gateway.yaml.
type GatewayOption struct {
	Endpoint                    string               `yaml:"endpoint"`
	ServiceName                 string               `yaml:"service_name"`
	Port                        int                  `yaml:"port"`
	TimeoutDuration             string               `yaml:"timeout_duration"  default:"5s"`
	RequestTimeout              string               `yaml:"request_timeout"   default:"30s"`
	EnableHangOverRequestHeader bool                 `yaml:"enable_hang_over_request_header" default:"true"`
	Services                    []GatewayService     `yaml:"services"`
	Opentelemetry               OpentelemetrySetting `yaml:"opentelemetry"`
}

// OpentelemetrySetting holds OpenTelemetry config.
type OpentelemetrySetting struct {
	TracingSetting OpentelemetryTracingSetting `yaml:"tracing"`
}

// OpentelemetryTracingSetting holds OpenTelemetry tracing config.
type OpentelemetryTracingSetting struct {
	Enable bool `yaml:"enable" default:"false"`
}

// gateway is the main HTTP handler for the federation gateway.
// It holds an atomically-swappable execution engine so schemas can be
// updated at runtime without restarting.
type gateway struct {
	graphQLEndpoint string
	serviceName     string

	// currentSchema and previousSchema hold *schemaStore values.
	// Read with Load(), write with Store() — no mutex needed for reads.
	currentSchema  atomic.Value
	previousSchema atomic.Value

	// inFlight counts requests that are currently being processed.
	// applySubgraph waits on this before swapping the schema.
	inFlight sync.WaitGroup

	// mu serialises calls to applySubgraph so only one schema update
	// runs at a time.
	mu sync.Mutex

	// requestTimeout is how long applySubgraph waits for in-flight
	// requests to drain before giving up.
	requestTimeout time.Duration

	// httpClient is shared across all subgraph requests (SDL fetch and query forwarding).
	httpClient *http.Client

	// retryOptions maps subgraph name → SDL fetch retry config.
	retryOptions map[string]RetryOption

	enableComplementRequestId   bool
	enableHangOverRequestHeader bool
	enableOpentelemetryTracing  bool
}

var _ http.Handler = (*gateway)(nil)

// NewGateway builds a gateway by fetching the SDL from every subgraph listed in
// settings, composing them into a SuperGraph, and wiring up the execution engine.
func NewGateway(settings GatewayOption) (*gateway, error) {
	httpClient := &http.Client{
		Timeout: 3 * time.Second,
	}
	if settings.Opentelemetry.TracingSetting.Enable {
		httpClient.Transport = otelhttp.NewTransport(http.DefaultTransport)
	}

	requestTimeout := 30 * time.Second
	if settings.RequestTimeout != "" {
		if d, err := time.ParseDuration(settings.RequestTimeout); err == nil {
			requestTimeout = d
		}
	}

	sdls := make(map[string]string, len(settings.Services))
	hosts := make(map[string]string, len(settings.Services))
	retryOptions := make(map[string]RetryOption, len(settings.Services))

	for _, svc := range settings.Services {
		hosts[svc.Name] = svc.Host
		retryOptions[svc.Name] = svc.Retry

		sdl, err := fetchSDL(svc.Host, httpClient, svc.Retry)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch SDL for service %q: %w", svc.Name, err)
		}
		sdls[svc.Name] = sdl
	}

	engine, err := buildEngine(sdls, hosts, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to build execution engine: %w", err)
	}

	store := &schemaStore{sdls: sdls, hosts: hosts, engine: engine}

	gw := &gateway{
		graphQLEndpoint:             settings.Endpoint,
		serviceName:                 settings.ServiceName,
		requestTimeout:              requestTimeout,
		httpClient:                  httpClient,
		retryOptions:                retryOptions,
		enableComplementRequestId:   true,
		enableHangOverRequestHeader: settings.EnableHangOverRequestHeader,
		enableOpentelemetryTracing:  settings.Opentelemetry.TracingSetting.Enable,
	}
	gw.currentSchema.Store(store)

	return gw, nil
}

// graphQLRequest is the body of an incoming GraphQL request.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// currentStore returns the active *schemaStore. It panics if nothing has been stored
// yet, which should never happen after a successful NewGateway call.
func (g *gateway) currentStore() *schemaStore {
	return g.currentSchema.Load().(*schemaStore)
}

// ServeHTTP dispatches incoming HTTP requests.
// POST /{name}/apply  → schema update endpoint
// POST /*             → GraphQL endpoint
func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Route schema-update requests before the method check so apply always works.
	if r.Method == http.MethodPost {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasSuffix(path, "/apply") {
			name := strings.TrimSuffix(path, "/apply")
			if name != "" {
				g.handleApply(w, name)
				return
			}
		}
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Track in-flight requests so applySubgraph can wait for them.
	g.inFlight.Add(1)
	defer g.inFlight.Done()

	// Snapshot the engine before processing so a concurrent schema swap
	// does not affect this request mid-flight.
	store := g.currentStore()
	engine := store.engine

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
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"errors": p.Errors(),
		})
		return
	}

	// Validate @inaccessible fields using the snapshot engine.
	if err := g.validateAccessibility(doc, engine); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"errors": []map[string]any{
				{
					"message":    err.Error(),
					"extensions": map[string]string{"code": "INACCESSIBLE_FIELD"},
				},
			},
		})
		return
	}

	plan, err := engine.planner.Plan(doc, req.Variables)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"errors": []string{err.Error()},
		})
		return
	}

	resp, err := engine.executor.Execute(ctx, plan, req.Variables)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"errors": []string{err.Error()},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleApply processes a POST /{name}/apply request from a subgraph.
// It delegates to applySubgraph and returns an appropriate HTTP response.
func (g *gateway) handleApply(w http.ResponseWriter, name string) {
	if err := g.applySubgraph(name); err != nil {
		log.Printf("schema apply failed for %q: %v", name, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
}

// applySubgraph fetches a fresh SDL for the named subgraph, recomposes the supergraph,
// waits for currently in-flight requests to complete, and atomically installs the
// new schema.  A previous schema is kept for panic-time rollback.
func (g *gateway) applySubgraph(name string) (retErr error) {
	// Panic recovery: if anything panics during composition or swap, roll back.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic during schema application for %q: %v — rolling back", name, r)
			g.rollbackToPreviousSchema()
			retErr = fmt.Errorf("panic during schema application: %v", r)
		}
	}()

	// Serialise concurrent apply calls.
	g.mu.Lock()
	defer g.mu.Unlock()

	current := g.currentStore()

	retry := g.retryOptions[name]
	newSDL, err := fetchSDL(current.hosts[name], g.httpClient, retry)
	if err != nil {
		return fmt.Errorf("SDL fetch failed: %w", err)
	}

	newSDLs := copyMap(current.sdls)
	newSDLs[name] = newSDL

	newEngine, err := buildEngine(newSDLs, current.hosts, g.httpClient)
	if err != nil {
		// Composition failed — current schema stays, treated as rollback.
		return fmt.Errorf("composition failed: %w", err)
	}

	// Wait for in-flight requests to drain before swapping.
	done := make(chan struct{})
	go func() {
		g.inFlight.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All in-flight requests finished — safe to swap.
	case <-time.After(g.requestTimeout):
		return fmt.Errorf("timeout waiting for in-flight requests after %s", g.requestTimeout)
	}

	newStore := &schemaStore{sdls: newSDLs, hosts: current.hosts, engine: newEngine}
	g.previousSchema.Store(g.currentSchema.Load())
	g.currentSchema.Store(newStore)
	return nil
}

// rollbackToPreviousSchema restores the last known-good schema.
// It is a no-op when no previous schema has been stored.
func (g *gateway) rollbackToPreviousSchema() {
	prev := g.previousSchema.Load()
	if prev != nil {
		g.currentSchema.Store(prev)
	}
}

// Start starts the gateway HTTP server on the given port.
func (g *gateway) Start(port int) error {
	fmt.Printf("Gateway started on port %d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), g)
}

// ---------------------------------------------------------------------------
// Accessibility validation helpers (use the engine snapshot, not g.superGraph)
// ---------------------------------------------------------------------------

func (g *gateway) validateAccessibility(doc *ast.Document, engine *executionEngine) error {
	for _, def := range doc.Definitions {
		if opDef, ok := def.(*ast.OperationDefinition); ok {
			rootTypeName := "Query"
			switch opDef.Operation {
			case ast.Query:
				rootTypeName = "Query"
			case ast.Mutation:
				rootTypeName = "Mutation"
			case ast.Subscription:
				rootTypeName = "Subscription"
			}

			if err := g.validateSelectionSet(opDef.SelectionSet, rootTypeName, engine); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *gateway) validateSelectionSet(selSet []ast.Selection, parentTypeName string, engine *executionEngine) error {
	if selSet == nil {
		return nil
	}

	for _, sel := range selSet {
		switch s := sel.(type) {
		case *ast.Field:
			fieldName := s.Name.String()

			if fieldName == "__typename" || fieldName == "__schema" || fieldName == "__type" {
				continue
			}

			if err := g.checkFieldAccessibility(parentTypeName, fieldName, engine); err != nil {
				return err
			}

			nextTypeName := g.getFieldTypeName(parentTypeName, fieldName, engine)
			if nextTypeName != "" {
				if err := g.validateSelectionSet(s.SelectionSet, nextTypeName, engine); err != nil {
					return err
				}
			}

		case *ast.InlineFragment:
			typeCondition := ""
			if s.TypeCondition != nil {
				typeCondition = s.TypeCondition.String()
			}
			if typeCondition == "" {
				typeCondition = parentTypeName
			}
			if err := g.validateSelectionSet(s.SelectionSet, typeCondition, engine); err != nil {
				return err
			}

		case *ast.FragmentSpread:
			// TODO: Implement fragment validation.
		}
	}

	return nil
}

func (g *gateway) checkFieldAccessibility(typeName, fieldName string, engine *executionEngine) error {
	// Search the COMPOSED supergraph schema first.
	// This is the authoritative source: if the field is not here it must not be queried.
	typeFound := false
	for _, def := range engine.superGraph.Schema.Definitions {
		objDef, ok := def.(*ast.ObjectTypeDefinition)
		if !ok || objDef.Name.String() != typeName {
			continue
		}
		typeFound = true
		fieldFound := false
		for _, f := range objDef.Fields {
			if f.Name.String() != fieldName {
				continue
			}
			fieldFound = true
			for _, d := range f.Directives {
				if d.Name == "inaccessible" {
					return fmt.Errorf("Cannot query field %q on type %q", fieldName, typeName)
				}
			}
		}
		// Field not present in the composed schema for this type definition.
		// (A type may be split across multiple definitions; keep looking.)
		_ = fieldFound
	}

	// Also check entity maps (captures @inaccessible recorded during subgraph parsing).
	for _, subGraph := range engine.superGraph.SubGraphs {
		if entity, exists := subGraph.GetEntity(typeName); exists {
			if field, ok := entity.Fields[fieldName]; ok {
				if field.IsInaccessible() {
					return fmt.Errorf("Cannot query field %q on type %q", fieldName, typeName)
				}
			}
		}
	}

	// If the type exists in the schema but the field is not found in any definition,
	// treat it as inaccessible/unknown.
	if typeFound && !g.fieldExistsInSchema(typeName, fieldName, engine) {
		return fmt.Errorf("Cannot query field %q on type %q", fieldName, typeName)
	}

	return nil
}

// fieldExistsInSchema reports whether the named field is declared on typeName
// in the composed supergraph schema.
func (g *gateway) fieldExistsInSchema(typeName, fieldName string, engine *executionEngine) bool {
	for _, def := range engine.superGraph.Schema.Definitions {
		objDef, ok := def.(*ast.ObjectTypeDefinition)
		if !ok || objDef.Name.String() != typeName {
			continue
		}
		for _, f := range objDef.Fields {
			if f.Name.String() == fieldName {
				return true
			}
		}
	}
	return false
}

func (g *gateway) getFieldTypeName(typeName, fieldName string, engine *executionEngine) string {
	for _, def := range engine.superGraph.Schema.Definitions {
		if objDef, ok := def.(*ast.ObjectTypeDefinition); ok {
			if objDef.Name.String() == typeName {
				for _, field := range objDef.Fields {
					if field.Name.String() == fieldName {
						return g.unwrapTypeName(field.Type)
					}
				}
			}
		}
	}
	return ""
}

func (g *gateway) unwrapTypeName(t ast.Type) string {
	switch typ := t.(type) {
	case *ast.NamedType:
		return typ.Name.String()
	case *ast.ListType:
		return g.unwrapTypeName(typ.Type)
	case *ast.NonNullType:
		return g.unwrapTypeName(typ.Type)
	}
	return ""
}
