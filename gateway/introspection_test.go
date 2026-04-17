package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/gateway"
)

// newIntrospectionGateway spins up two subgraph SDL servers and returns a
// fully-built gateway. `enableIntrospection` toggles the new option.
func newIntrospectionGateway(t *testing.T, enableIntrospection bool) *gateway.Gateway {
	t.Helper()
	pSrv := sdlServer(t, testSDLProducts)
	t.Cleanup(pSrv.Close)
	rSrv := sdlServer(t, testSDLReviews)
	t.Cleanup(rSrv.Close)

	settings := gateway.GatewayOption{
		Endpoint:            "/graphql",
		ServiceName:         "test-gateway",
		Port:                9999,
		RequestTimeout:      "5s",
		EnableIntrospection: enableIntrospection,
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return gw
}

func postGraphQL(t *testing.T, h http.Handler, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("invalid JSON response: %v\nbody=%s", err, raw)
	}
	return out
}

// 5.1 enable_introspection=true のとき __schema クエリがメタデータを返す
func TestIntrospection_EnabledReturnsSchema(t *testing.T) {
	gw := newIntrospectionGateway(t, true)

	resp := postGraphQL(t, gw, `{ __schema { queryType { name } types { name kind } } }`)

	if _, hasErr := resp["errors"]; hasErr {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data: %v", resp)
	}
	schema, ok := data["__schema"].(map[string]any)
	if !ok {
		t.Fatalf("missing __schema: %v", data)
	}
	qt, ok := schema["queryType"].(map[string]any)
	if !ok || qt["name"] != "Query" {
		t.Errorf("expected queryType.name=Query, got %v", schema["queryType"])
	}
	types, ok := schema["types"].([]any)
	if !ok || len(types) == 0 {
		t.Fatalf("expected non-empty types array, got %v", schema["types"])
	}
	// Product should be in the composed schema.
	found := false
	for _, tt := range types {
		m := tt.(map[string]any)
		if m["name"] == "Product" && m["kind"] == "OBJECT" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Product OBJECT type in introspection result")
	}
}

// 5.2 enable_introspection=true のとき __type(name:) が該当型を返す
func TestIntrospection_TypeLookup(t *testing.T) {
	gw := newIntrospectionGateway(t, true)

	resp := postGraphQL(t, gw, `{ __type(name: "Product") { name kind fields { name type { kind name ofType { name } } } } }`)
	if _, hasErr := resp["errors"]; hasErr {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	data := resp["data"].(map[string]any)
	typ, ok := data["__type"].(map[string]any)
	if !ok {
		t.Fatalf("expected __type object, got %v", data["__type"])
	}
	if typ["name"] != "Product" || typ["kind"] != "OBJECT" {
		t.Errorf("expected Product OBJECT, got %v", typ)
	}
	fields, _ := typ["fields"].([]any)
	if len(fields) == 0 {
		t.Errorf("expected non-empty fields for Product")
	}
}

// 5.3 enable_introspection=false のとき introspection クエリはエラーを返す
func TestIntrospection_DisabledReturnsError(t *testing.T) {
	gw := newIntrospectionGateway(t, false)

	resp := postGraphQL(t, gw, `{ __schema { queryType { name } } }`)

	errs, ok := resp["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("expected errors when introspection disabled, got %v", resp)
	}
	msg, _ := errs[0].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "introspection") {
		t.Errorf("expected introspection error message, got %q", msg)
	}
}

// 5.4 enable_introspection=false でも非 introspection クエリは通常通り処理される
// (本ケースでは planner の挙動確認にとどめ、サブグラフ応答までは検証しない。)
func TestIntrospection_DisabledDoesNotBlockRegularQueries(t *testing.T) {
	gw := newIntrospectionGateway(t, false)

	// Parse success; executor may fail because the subgraph stub doesn't serve
	// this query. We only assert the request is NOT rejected with the
	// INTROSPECTION_DISABLED error.
	resp := postGraphQL(t, gw, `{ product(id: "1") { id name } }`)
	if errs, ok := resp["errors"].([]any); ok {
		for _, e := range errs {
			if m, ok := e.(map[string]any); ok {
				if ext, ok := m["extensions"].(map[string]any); ok {
					if ext["code"] == "INTROSPECTION_DISABLED" {
						t.Fatalf("regular query was blocked by introspection guard: %v", resp)
					}
				}
			}
		}
	}
}
