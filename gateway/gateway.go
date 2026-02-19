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
	"github.com/n9te9/graphql-parser/ast"
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

	// Validate @inaccessible fields
	if err := g.validateAccessibility(doc); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{
				{
					"message":    err.Error(),
					"extensions": map[string]string{"code": "INACCESSIBLE_FIELD"},
				},
			},
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

// validateAccessibility validates that no @inaccessible fields are queried.
func (g *gateway) validateAccessibility(doc *ast.Document) error {
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

			if err := g.validateSelectionSet(opDef.SelectionSet, rootTypeName); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateSelectionSet recursively validates selections.
func (g *gateway) validateSelectionSet(selSet []ast.Selection, parentTypeName string) error {
	if selSet == nil {
		return nil
	}

	for _, sel := range selSet {
		switch s := sel.(type) {
		case *ast.Field:
			fieldName := s.Name.String()

			// Skip introspection fields
			if fieldName == "__typename" || fieldName == "__schema" || fieldName == "__type" {
				continue
			}

			// Check if field is inaccessible
			if err := g.checkFieldAccessibility(parentTypeName, fieldName); err != nil {
				return err
			}

			// Get the field type for recursive validation
			nextTypeName := g.getFieldTypeName(parentTypeName, fieldName)
			if nextTypeName != "" {
				if err := g.validateSelectionSet(s.SelectionSet, nextTypeName); err != nil {
					return err
				}
			}

		case *ast.FragmentSpread:
			// Handle fragment spreads
			// For now, skip validation in fragments
			// TODO: Implement fragment validation

		case *ast.InlineFragment:
			// Handle inline fragments
			typeCondition := ""
			if s.TypeCondition != nil {
				typeCondition = s.TypeCondition.String()
			}
			if typeCondition == "" {
				typeCondition = parentTypeName
			}
			if err := g.validateSelectionSet(s.SelectionSet, typeCondition); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkFieldAccessibility checks if a field is inaccessible.
func (g *gateway) checkFieldAccessibility(typeName, fieldName string) error {
	for _, subGraph := range g.superGraph.SubGraphs {
		if entity, exists := subGraph.GetEntity(typeName); exists {
			if field, ok := entity.Fields[fieldName]; ok {
				if field.IsInaccessible() {
					return fmt.Errorf("Cannot query field \"%s\" on type \"%s\"", fieldName, typeName)
				}
			}
		}

		// Also check non-entity types in the schema
		for _, def := range subGraph.Schema.Definitions {
			if objDef, ok := def.(*ast.ObjectTypeDefinition); ok {
				if objDef.Name.String() == typeName {
					for _, f := range objDef.Fields {
						if f.Name.String() == fieldName {
							// Check for @inaccessible directive
							for _, d := range f.Directives {
								if d.Name == "inaccessible" {
									return fmt.Errorf("Cannot query field \"%s\" on type \"%s\"", fieldName, typeName)
								}
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// getFieldTypeName returns the type name of a field.
func (g *gateway) getFieldTypeName(typeName, fieldName string) string {
	for _, def := range g.superGraph.Schema.Definitions {
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

// unwrapTypeName extracts the base type name from a type.
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
