package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/n9te9/go-graphql-federation-gateway/federation/executor"
	"github.com/n9te9/go-graphql-federation-gateway/federation/introspection"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
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

// ConnectionPoolSetting controls the HTTP connection pool for subgraph requests.
type ConnectionPoolSetting struct {
	// MaxIdleConnsPerHost is the maximum number of idle (keep-alive) connections
	// to keep per subgraph host. 0 uses the Go default (2). Recommended: 32.
	MaxIdleConnsPerHost int `yaml:"max_idle_conns_per_host"`
	// MaxConnsPerHost limits the total number of connections per host (0 = unlimited).
	MaxConnsPerHost int `yaml:"max_conns_per_host"`
	// IdleConnTimeout is how long an idle connection is kept in the pool before
	// being closed. Empty string or "0s" means no timeout. Default: "90s".
	IdleConnTimeout string `yaml:"idle_conn_timeout"`
}

// GatewayOption is the top-level configuration loaded from gateway.yaml.
type GatewayOption struct {
	Endpoint                    string                `yaml:"endpoint"`
	ServiceName                 string                `yaml:"service_name"`
	Port                        int                   `yaml:"port"`
	TimeoutDuration             string                `yaml:"timeout_duration"  default:"5s"`
	RequestTimeout              string                `yaml:"request_timeout"   default:"30s"`
	EnableHangOverRequestHeader bool                  `yaml:"enable_hang_over_request_header" default:"true"`
	Services                    []GatewayService      `yaml:"services"`
	Opentelemetry               OpentelemetrySetting  `yaml:"opentelemetry"`
	ConnectionPool              ConnectionPoolSetting `yaml:"connection_pool"`
	Admin                       AdminSetting          `yaml:"admin"`
	Introspection               IntrospectionSetting  `yaml:"introspection"`
}

// AdminSetting controls administrative HTTP endpoints (e.g. POST /{name}/apply).
type AdminSetting struct {
	// Auth holds authentication settings for admin endpoints.
	Auth AdminAuthSetting `yaml:"auth"`
}

// AdminAuthSetting holds the bearer token used to authenticate admin requests.
//
// Token sources, in order of precedence:
//  1. Environment variable GATEWAY_ADMIN_TOKEN (if set and non-empty).
//  2. The Token field below.
//
// If neither source yields a non-empty token, admin endpoints are disabled
// entirely and respond with 404 to all requests.
type AdminAuthSetting struct {
	Token string `yaml:"token"`
}

// IntrospectionSetting toggles GraphQL introspection support at the gateway.
// When Enable is true (default), the gateway answers __schema / __type queries
// from the composed supergraph. When false, introspection queries are rejected.
type IntrospectionSetting struct {
	Enable *bool `yaml:"enable"`
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

	// adminToken is the bearer token required by admin endpoints.
	// Empty string disables all admin endpoints.
	adminToken string

	// enableIntrospection controls whether __schema / __type queries are
	// answered from the composed supergraph (true) or rejected (false).
	enableIntrospection bool
}

var _ http.Handler = (*gateway)(nil)

// buildTransport constructs an http.Transport tuned with the given connection pool
// settings and optionally wraps it with OpenTelemetry instrumentation.
func buildTransport(pool ConnectionPoolSetting, otelEnabled bool) http.RoundTripper {
	maxIdle := pool.MaxIdleConnsPerHost
	if maxIdle == 0 {
		maxIdle = 32 // Default: much higher than http.DefaultTransport's 2
	}

	idleTimeout := 90 * time.Second
	if pool.IdleConnTimeout != "" {
		if d, err := time.ParseDuration(pool.IdleConnTimeout); err == nil {
			idleTimeout = d
		}
	}

	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   maxIdle,
		MaxConnsPerHost:       pool.MaxConnsPerHost,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if otelEnabled {
		return otelhttp.NewTransport(t)
	}
	return t
}

// NewGateway builds a gateway by fetching the SDL from every subgraph listed in
// settings, composing them into a SuperGraph, and wiring up the execution engine.
func NewGateway(settings GatewayOption) (*gateway, error) {
	httpClient := &http.Client{
		Timeout:   3 * time.Second,
		Transport: buildTransport(settings.ConnectionPool, settings.Opentelemetry.TracingSetting.Enable),
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

	// Admin token: env var overrides config.
	adminToken := settings.Admin.Auth.Token
	if env := os.Getenv("GATEWAY_ADMIN_TOKEN"); env != "" {
		adminToken = env
	}

	// Introspection defaults to enabled.
	enableIntrospection := true
	if settings.Introspection.Enable != nil {
		enableIntrospection = *settings.Introspection.Enable
	}

	gw := &gateway{
		graphQLEndpoint:             settings.Endpoint,
		serviceName:                 settings.ServiceName,
		requestTimeout:              requestTimeout,
		httpClient:                  httpClient,
		retryOptions:                retryOptions,
		enableComplementRequestId:   true,
		enableHangOverRequestHeader: settings.EnableHangOverRequestHeader,
		enableOpentelemetryTracing:  settings.Opentelemetry.TracingSetting.Enable,
		adminToken:                  adminToken,
		enableIntrospection:         enableIntrospection,
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
// POST /{name}/apply  → schema update endpoint (admin, requires bearer token)
// POST /*             → GraphQL endpoint
func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Route schema-update requests before the method check so apply always works.
	if r.Method == http.MethodPost {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasSuffix(path, "/apply") {
			name := strings.TrimSuffix(path, "/apply")
			if name != "" {
				g.handleAdminApply(w, r, name)
				return
			}
		}
	}

	if r.Method != http.MethodPost {
		writeGraphQLError(w, http.StatusMethodNotAllowed,
			"GraphQL endpoint only supports POST requests",
			ErrCodeMethodNotAllowed)
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
		writeGraphQLError(w, http.StatusBadRequest,
			"invalid request body: "+err.Error(),
			ErrCodeBadRequest)
		return
	}

	ctx := r.Context()
	if g.enableHangOverRequestHeader {
		ctx = executor.SetRequestHeaderToContext(ctx, r.Header)
	}

	// Fast path: reuse a cached plan when the query string is identical.
	// The cache is scoped to the current executionEngine, so it is automatically
	// invalidated whenever the schema is updated (old engine is GC'd).
	var plan *planner.PlanV2
	if cached, ok := engine.planner.PlanCache().Load(req.Query); ok {
		plan = cached.(*planner.PlanV2)
	} else {
		l := lexer.New(req.Query)
		p := parser.New(l)
		doc := p.ParseDocument()
		if errs := p.Errors(); len(errs) > 0 {
			gqlErrs := make([]GraphQLError, 0, len(errs))
			for _, e := range errs {
				gqlErrs = append(gqlErrs, newGraphQLError(e, ErrCodeGraphQLParseFailed))
			}
			writeGraphQLErrors(w, 0, gqlErrs...)
			return
		}

		// Introspection-only queries are answered from the composed supergraph
		// schema and never reach the planner.
		if op := getQueryOperation(doc); op != nil && isIntrospectionOnly(op) {
			g.handleIntrospection(w, doc, op, req.Variables, engine)
			return
		}

		// Validate @inaccessible fields using the snapshot engine.
		if err := g.validateAccessibility(doc, engine); err != nil {
			writeGraphQLError(w, 0, err.Error(), ErrCodeInaccessibleField)
			return
		}

		var err error
		plan, err = engine.planner.Plan(doc, req.Variables)
		if err != nil {
			writeGraphQLError(w, 0, err.Error(), ErrCodePlanningFailed)
			return
		}
		engine.planner.PlanCache().Store(req.Query, plan)
	}

	resp, err := engine.executor.Execute(ctx, plan, req.Variables)
	if err != nil {
		writeGraphQLError(w, 0, err.Error(), ErrCodeInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleAdminApply authenticates the request and delegates to handleApply.
// If admin auth is not configured, returns 404 to hide the endpoint entirely.
func (g *gateway) handleAdminApply(w http.ResponseWriter, r *http.Request, name string) {
	if g.adminToken == "" {
		// Admin endpoints are disabled. Return 404 so that the surface area
		// of the gateway is not advertised to unauthenticated callers.
		http.NotFound(w, r)
		return
	}

	provided := bearerToken(r.Header.Get("Authorization"))
	expected := g.adminToken
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		log.Printf("admin auth failed for %q from %s", name, r.RemoteAddr)
		w.Header().Set("WWW-Authenticate", `Bearer realm="gateway-admin"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	g.handleApply(w, name)
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
// Returns empty string if the header is missing or malformed.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) {
		return ""
	}
	if !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// handleIntrospection answers an introspection-only query from the composed
// supergraph schema, bypassing the planner and executor entirely.
func (g *gateway) handleIntrospection(
	w http.ResponseWriter,
	doc *ast.Document,
	op *ast.OperationDefinition,
	vars map[string]any,
	engine *executionEngine,
) {
	if !g.enableIntrospection {
		writeGraphQLError(w, 0,
			"introspection is disabled on this gateway",
			ErrCodeIntrospectionDisabled)
		return
	}

	resolver := introspection.NewResolver(engine.superGraph.Schema)
	data, errs := resolver.Resolve(doc, op, vars)

	resp := map[string]any{}
	if data != nil {
		resp["data"] = data
	}
	if len(errs) > 0 {
		gqlErrs := make([]GraphQLError, 0, len(errs))
		for _, e := range errs {
			gqlErrs = append(gqlErrs, newGraphQLError(e.Error(), ErrCodeGraphQLValidationFailed))
		}
		resp["errors"] = gqlErrs
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleApply processes a POST /{name}/apply request from a subgraph.
// It delegates to applySubgraph and returns an appropriate HTTP response.
//
// Authentication is performed by handleAdminApply before this is called.
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
	fragmentDefs := collectFragmentDefinitions(doc)
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

			visited := map[string]bool{}
			if err := g.validateSelectionSet(opDef.SelectionSet, rootTypeName, engine, fragmentDefs, visited); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *gateway) validateSelectionSet(
	selSet []ast.Selection,
	parentTypeName string,
	engine *executionEngine,
	fragmentDefs map[string]*ast.FragmentDefinition,
	visited map[string]bool,
) error {
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
				if err := g.validateSelectionSet(s.SelectionSet, nextTypeName, engine, fragmentDefs, visited); err != nil {
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
			if err := g.validateSelectionSet(s.SelectionSet, typeCondition, engine, fragmentDefs, visited); err != nil {
				return err
			}

		case *ast.FragmentSpread:
			name := s.Name.String()
			if visited[name] {
				// Cyclic fragment reference. GraphQL spec forbids this; the
				// planner / parser will surface the error elsewhere — here we
				// just stop recursing to avoid an infinite loop.
				continue
			}
			frag, ok := fragmentDefs[name]
			if !ok {
				// Unknown fragment name. Let the planner surface the error.
				continue
			}
			typeCondition := ""
			if frag.TypeCondition != nil {
				typeCondition = frag.TypeCondition.String()
			}
			if typeCondition == "" {
				typeCondition = parentTypeName
			}
			visited[name] = true
			if err := g.validateSelectionSet(frag.SelectionSet, typeCondition, engine, fragmentDefs, visited); err != nil {
				delete(visited, name)
				return err
			}
			delete(visited, name)
		}
	}

	return nil
}

// collectFragmentDefinitions extracts all named fragment definitions from a
// GraphQL document. It is used by validateAccessibility to resolve
// FragmentSpread references.
func collectFragmentDefinitions(doc *ast.Document) map[string]*ast.FragmentDefinition {
	fragments := make(map[string]*ast.FragmentDefinition)
	if doc == nil {
		return fragments
	}
	for _, def := range doc.Definitions {
		if fragDef, ok := def.(*ast.FragmentDefinition); ok {
			fragments[fragDef.Name.String()] = fragDef
		}
	}
	return fragments
}

// getQueryOperation returns the first query OperationDefinition in the document,
// or nil if no query operation is present.
func getQueryOperation(doc *ast.Document) *ast.OperationDefinition {
	if doc == nil {
		return nil
	}
	for _, def := range doc.Definitions {
		if op, ok := def.(*ast.OperationDefinition); ok {
			if op.Operation == ast.Query || op.Operation == "" {
				return op
			}
		}
	}
	return nil
}

// isIntrospectionOnly reports whether every top-level selection of op is one
// of the introspection meta-fields (__schema / __type / __typename).
func isIntrospectionOnly(op *ast.OperationDefinition) bool {
	if op == nil {
		return false
	}
	if len(op.SelectionSet) == 0 {
		return false
	}
	for _, sel := range op.SelectionSet {
		f, ok := sel.(*ast.Field)
		if !ok {
			return false
		}
		name := f.Name.String()
		if name != "__schema" && name != "__type" && name != "__typename" {
			return false
		}
	}
	return true
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
