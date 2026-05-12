package introspection_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/introspection"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

const productsSDL = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key"])

type Query {
	product(id: ID!): Product
}

type Product @key(fields: "id") {
	id:    ID!
	name:  String
	price: Int
}`

func mustSuperGraph(t *testing.T) *graph.SuperGraphV2 {
	t.Helper()
	sg, err := graph.NewSubGraphV2("products", []byte(productsSDL), "http://example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2: %v", err)
	}
	super, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2: %v", err)
	}
	return super
}

func parseQuery(t *testing.T, q string) (*ast.Document, *ast.OperationDefinition) {
	t.Helper()
	doc := parser.New(lexer.New(q)).ParseDocument()
	for _, def := range doc.Definitions {
		if op, ok := def.(*ast.OperationDefinition); ok {
			return doc, op
		}
	}
	t.Fatalf("no operation in query: %s", q)
	return nil, nil
}

func TestResolver_Schema_QueryType(t *testing.T) {
	super := mustSuperGraph(t)
	r := introspection.NewResolver(super.Schema)

	doc, op := parseQuery(t, `{ __schema { queryType { name } } }`)
	data, errs := r.Resolve(doc, op, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	schema, _ := data["__schema"].(map[string]any)
	if schema == nil {
		t.Fatalf("expected __schema, got %v", data)
	}
	qt, _ := schema["queryType"].(map[string]any)
	if qt == nil || qt["name"] != "Query" {
		t.Errorf("expected queryType.name=Query, got %v", schema["queryType"])
	}
}

func TestResolver_Type_FieldsAndKind(t *testing.T) {
	super := mustSuperGraph(t)
	r := introspection.NewResolver(super.Schema)

	doc, op := parseQuery(t, `{ __type(name: "Product") { name kind fields { name type { name kind } } } }`)
	data, errs := r.Resolve(doc, op, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	tdef, _ := data["__type"].(map[string]any)
	if tdef == nil {
		t.Fatalf("expected __type, got %v", data)
	}
	if tdef["name"] != "Product" {
		t.Errorf("expected name=Product, got %v", tdef["name"])
	}
	if tdef["kind"] != "OBJECT" {
		t.Errorf("expected kind=OBJECT, got %v", tdef["kind"])
	}
	fields, _ := tdef["fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("expected fields on Product")
	}
	// Verify at least one field has its type resolved.
	for _, f := range fields {
		fm := f.(map[string]any)
		if fm["name"] == "id" {
			ty, _ := fm["type"].(map[string]any)
			if ty == nil || ty["kind"] != "NON_NULL" {
				t.Errorf("expected id field type kind=NON_NULL, got %v", ty)
			}
		}
	}
}

func TestResolver_TypeNotFound_ReturnsNull(t *testing.T) {
	super := mustSuperGraph(t)
	r := introspection.NewResolver(super.Schema)

	doc, op := parseQuery(t, `{ __type(name: "DoesNotExist") { name } }`)
	data, errs := r.Resolve(doc, op, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if data["__type"] != nil {
		t.Errorf("expected __type=null for unknown type, got %v", data["__type"])
	}
}

func TestResolver_TopLevelTypename(t *testing.T) {
	super := mustSuperGraph(t)
	r := introspection.NewResolver(super.Schema)

	doc, op := parseQuery(t, `{ __typename }`)
	data, errs := r.Resolve(doc, op, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if data["__typename"] != "Query" {
		t.Errorf("expected __typename=Query, got %v", data["__typename"])
	}
}

func TestResolver_FragmentSpread(t *testing.T) {
	super := mustSuperGraph(t)
	r := introspection.NewResolver(super.Schema)

	q := `
		query Q { __schema { ...S } }
		fragment S on __Schema { queryType { name } }
	`
	doc, op := parseQuery(t, q)
	data, errs := r.Resolve(doc, op, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	schema, _ := data["__schema"].(map[string]any)
	qt, _ := schema["queryType"].(map[string]any)
	if qt == nil || qt["name"] != "Query" {
		t.Errorf("fragment spread did not expand: %v", schema)
	}
}

func TestResolver_NilSchema_ReturnsError(t *testing.T) {
	r := introspection.NewResolver(nil)
	doc, op := parseQuery(t, `{ __schema { queryType { name } } }`)
	_, errs := r.Resolve(doc, op, nil)
	if len(errs) == 0 {
		t.Errorf("expected error for nil schema")
	}
}
