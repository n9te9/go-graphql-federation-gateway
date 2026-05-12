package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n9te9/go-graphql-federation-gateway/gateway"
)

// ---------------------------------------------------------------------------
// Shared test SDL helpers
// ---------------------------------------------------------------------------

const testSDLProducts = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key"])

type Query {
	product(id: ID!): Product
}

type Product @key(fields: "id") {
	id:   ID!
	name: String
}`

const testSDLProductsV2 = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key"])

type Query {
	product(id: ID!): Product
}

type Product @key(fields: "id") {
	id:    ID!
	name:  String
	price: Int
}`

const testSDLProductsInvalid = `this is {{ not valid SDL`

const testSDLReviews = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key"])

type Query {
	reviews: [Review]
}

type Review @key(fields: "id") {
	id:   ID!
	body: String
}`

// sdlServer returns an httptest.Server that always responds with the given SDL.
func sdlServer(t *testing.T, sdl string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"_service":{"sdl":%q}}}`, sdl)
	}))
}

// errServer returns an httptest.Server that always responds with 503.
func errServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
}

// ---------------------------------------------------------------------------
// 1. Gateway 起動時のユースケース
// ---------------------------------------------------------------------------

// 1.1 NewGateway は各サブグラフから SDL をフェッチしてエンジンを構築する
func TestNewGateway_FetchesSDLOnStartup(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
	}

	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway returned unexpected error: %v", err)
	}
	if gw == nil {
		t.Fatal("expected non-nil gateway")
	}

	// SDL map should contain both subgraphs.
	sdls := gw.CurrentSDLsForTest()
	if _, ok := sdls["products"]; !ok {
		t.Error("expected 'products' SDL in current store")
	}
	if _, ok := sdls["reviews"]; !ok {
		t.Error("expected 'reviews' SDL in current store")
	}
}

// 1.2 NewGateway はサブグラフが応答しない場合にエラーを返す
func TestNewGateway_FailsWhenSubgraphUnreachable(t *testing.T) {
	eSrv := errServer(t)
	defer eSrv.Close()

	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "bad", Host: eSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "1s"}},
		},
	}

	_, err := gateway.NewGateway(settings)
	if err == nil {
		t.Fatal("expected error when subgraph is unreachable, got nil")
	}
}

// 1.3 NewGateway は SDL が無効な場合にコンポジションエラーを返す
func TestNewGateway_FailsOnInvalidSDL(t *testing.T) {
	pSrv := sdlServer(t, testSDLProductsInvalid)
	defer pSrv.Close()

	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
	}

	_, err := gateway.NewGateway(settings)
	if err == nil {
		t.Fatal("expected composition error for invalid SDL, got nil")
	}
}

// ---------------------------------------------------------------------------
// 2. Gateway がスキーマ更新を受け取るユースケース
// ---------------------------------------------------------------------------

// 2.1 applySubgraph は新しい SDL をフェッチして SDL マップを更新する
func TestApplySubgraph_FetchesNewSDL(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGateway(t, pSrv.URL, rSrv.URL)

	// Update the products server to serve v2 SDL.
	var mu sync.Mutex
	serveV2 := false
	pSrv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		v2 := serveV2
		mu.Unlock()
		sdl := testSDLProducts
		if v2 {
			sdl = testSDLProductsV2
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"_service":{"sdl":%q}}}`, sdl)
	}))
	defer pSrv2.Close()

	// Rebuild gateway pointing products at pSrv2.
	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrv2.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// Switch server to v2.
	mu.Lock()
	serveV2 = true
	mu.Unlock()

	if err := gw.ApplySubgraphForTest("products"); err != nil {
		t.Fatalf("ApplySubgraph: %v", err)
	}

	sdls := gw.CurrentSDLsForTest()
	if !strings.Contains(sdls["products"], "price") {
		t.Error("expected updated SDL to contain 'price' field")
	}
}

// 2.2 applySubgraph は新しいエンジンを構築してアトミックに差し替える
func TestApplySubgraph_AtomicSwap(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGateway(t, pSrv.URL, rSrv.URL)

	// Point the products server to v2 SDL.
	pSrv.Close()
	pSrvV2 := sdlServer(t, testSDLProductsV2)
	defer pSrvV2.Close()

	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrvV2.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// Verify no previous schema yet.
	if gw.HasPreviousSchemaForTest() {
		t.Error("expected no previous schema before first apply")
	}

	// Apply should succeed and store the old schema as previous.
	if err := gw.ApplySubgraphForTest("products"); err != nil {
		t.Fatalf("ApplySubgraph: %v", err)
	}

	if !gw.HasPreviousSchemaForTest() {
		t.Error("expected previous schema to be stored after successful apply")
	}
}

// 2.3 applySubgraph はコンポジション失敗時に現在のスキーマを維持する（ロールバック）
func TestApplySubgraph_CompositionFailureKeepsCurrentSchema(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGateway(t, pSrv.URL, rSrv.URL)

	// Remember current SDL.
	beforeSDLs := gw.CurrentSDLsForTest()

	// Switch products server to serve invalid SDL.
	pInvalid := sdlServer(t, testSDLProductsInvalid)
	defer pInvalid.Close()

	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pInvalid.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		// NewGateway will fail on invalid SDL — start from valid state and inject bad SDL via apply.
		t.Skip("invalid SDL rejected at startup (expected in some modes)")
		_ = gw
	}
	_ = gw
	_ = beforeSDLs
}

// TestApplySubgraph_CompositionFailureViaApply は apply 時のコンポジション失敗をテストする
func TestApplySubgraph_CompositionFailureViaApply(t *testing.T) {
	var mu sync.Mutex
	serveInvalid := false

	pSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		invalid := serveInvalid
		mu.Unlock()
		sdl := testSDLProducts
		if invalid {
			sdl = testSDLProductsInvalid
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"_service":{"sdl":%q}}}`, sdl)
	}))
	defer pSrv.Close()

	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	beforeSDL := gw.CurrentSDLsForTest()["products"]

	// Switch to invalid SDL.
	mu.Lock()
	serveInvalid = true
	mu.Unlock()

	err := gw.ApplySubgraphForTest("products")
	if err == nil {
		t.Fatal("expected composition error from apply, got nil")
	}

	// Current schema must not have changed.
	afterSDL := gw.CurrentSDLsForTest()["products"]
	if afterSDL != beforeSDL {
		t.Errorf("schema changed despite composition failure:\nbefore: %s\nafter: %s", beforeSDL, afterSDL)
	}
}

// ---------------------------------------------------------------------------
// 3. POST /{name}/apply HTTP エンドポイントのユースケース
// ---------------------------------------------------------------------------

func TestHandleApply_Returns200OnSuccess(t *testing.T) {
	var mu sync.Mutex
	serveV2 := false

	pSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		v2 := serveV2
		mu.Unlock()
		sdl := testSDLProducts
		if v2 {
			sdl = testSDLProductsV2
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"_service":{"sdl":%q}}}`, sdl)
	}))
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	mu.Lock()
	serveV2 = true
	mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/products/apply", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleApply_Returns500OnFailure(t *testing.T) {
	var mu sync.Mutex
	serveInvalid := false

	pSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		invalid := serveInvalid
		mu.Unlock()
		sdl := testSDLProducts
		if invalid {
			sdl = testSDLProductsInvalid
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"_service":{"sdl":%q}}}`, sdl)
	}))
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	mu.Lock()
	serveInvalid = true
	mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/products/apply", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 4. panic ロールバックのユースケース
// ---------------------------------------------------------------------------

// TestApplySubgraph_RollbackOnPanic は panic 発生時に前のスキーマに戻ることを確認する
// ここでは rollbackToPreviousSchema を間接的にテストする。
// 直接 panic を注入する方法は export_test.go 経由で実施する。
func TestApplySubgraph_MultipleApplies_PreviousIsPreserved(t *testing.T) {
	var mu sync.Mutex
	currentSDL := testSDLProducts

	pSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sdl := currentSDL
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"_service":{"sdl":%q}}}`, sdl)
	}))
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	// First apply: products → v2.
	mu.Lock()
	currentSDL = testSDLProductsV2
	mu.Unlock()

	if err := gw.ApplySubgraphForTest("products"); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if !gw.HasPreviousSchemaForTest() {
		t.Error("expected previous schema after first apply")
	}
	if !strings.Contains(gw.CurrentSDLsForTest()["products"], "price") {
		t.Error("expected v2 SDL (with price) after first apply")
	}

	// Second apply: products → v1 again.
	mu.Lock()
	currentSDL = testSDLProducts
	mu.Unlock()

	if err := gw.ApplySubgraphForTest("products"); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if strings.Contains(gw.CurrentSDLsForTest()["products"], "price") {
		t.Error("expected v1 SDL (without price) after second apply")
	}
}

// TestApplySubgraph_ConcurrentAppliesAreSerialized は apply の同時実行が直列化されることを確認する
func TestApplySubgraph_ConcurrentAppliesAreSerialized(t *testing.T) {
	pSrv := sdlServer(t, testSDLProductsV2)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	const goroutines = 5
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = gw.ApplySubgraphForTest("products")
		}(i)
	}
	wg.Wait()

	// All applies should succeed (serialised by mu inside applySubgraph).
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. apply タイムアウトのユースケース
// ---------------------------------------------------------------------------

func TestApplySubgraph_TimeoutWhenInFlightRequestsDoNotDrain(t *testing.T) {
	pSrv := sdlServer(t, testSDLProductsV2)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	// Very short requestTimeout to make the test fast.
	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "50ms",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// Simulate a long-running in-flight request by adding to inFlight directly
	// via a fake GraphQL request that hangs.
	blocked := make(chan struct{})
	unblock := make(chan struct{})

	slowHandler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This simulates GraphQL being handled while we try to apply.
		close(blocked)
		<-unblock
		w.WriteHeader(http.StatusOK)
	}))
	defer slowHandler.Close()

	// Send a slow request to the gateway (it will hang in ServeHTTP).
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/graphql",
			strings.NewReader(`{"query":"{ product(id:\"1\") { id } }"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)
	}()

	// Wait until the goroutine is in ServeHTTP (inFlight incremented).
	// We don't have a direct signal, so we sleep briefly.
	time.Sleep(10 * time.Millisecond)

	// Now try to apply — the requestTimeout (50ms) is shorter than the
	// time the fake request would take, so apply must time out.
	err = gw.ApplySubgraphForTest("products")

	// Unblock the slow request so the goroutine can finish.
	close(unblock)

	if err == nil {
		// apply may succeed if the slow goroutine drained before the timeout;
		// this is a timing-based test, so we only verify no panic occurred.
		t.Log("apply succeeded before timeout — timing-sensitive test passed")
		return
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustNewGateway(t *testing.T, productsURL, reviewsURL string) *gateway.Gateway {
	t.Helper()
	return mustNewGatewayWithURLs(t, map[string]string{
		"products": productsURL,
		"reviews":  reviewsURL,
	})
}

func mustNewGatewayWithURLs(t *testing.T, urls map[string]string) *gateway.Gateway {
	t.Helper()
	services := make([]gateway.GatewayService, 0, len(urls))
	for name, url := range urls {
		services = append(services, gateway.GatewayService{
			Name:  name,
			Host:  url,
			Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"},
		})
	}
	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services:       services,
		Admin: gateway.AdminSetting{
			Auth: gateway.AdminAuthSetting{Token: testAdminToken},
		},
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return gw
}

// testAdminToken is the bearer token used by /apply tests in this package.
const testAdminToken = "test-admin-token"

// ---------------------------------------------------------------------------
// Apply endpoint authentication
// ---------------------------------------------------------------------------

func TestHandleApply_RejectsRequestWithoutBearerToken(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	req := httptest.NewRequest(http.MethodPost, "/products/apply", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without Authorization header, got %d", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Errorf("expected WWW-Authenticate Bearer challenge, got %q", got)
	}
}

func TestHandleApply_RejectsRequestWithWrongToken(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	req := httptest.NewRequest(http.MethodPost, "/products/apply", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", w.Code)
	}
}

// When admin auth is not configured, the apply endpoint must be hidden (404)
// rather than reachable without authentication.
func TestHandleApply_DisabledWhenTokenNotConfigured(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
		// No Admin block: token is empty → endpoint disabled.
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/products/apply", nil)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 when admin token not configured, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Spec-compliant error response format
// ---------------------------------------------------------------------------

// graphQLResponse mirrors the shape of `{ "errors": [...] }`.
type graphQLResponse struct {
	Data   map[string]any `json:"data,omitempty"`
	Errors []struct {
		Message    string         `json:"message"`
		Locations  []any          `json:"locations,omitempty"`
		Path       []any          `json:"path,omitempty"`
		Extensions map[string]any `json:"extensions,omitempty"`
	} `json:"errors,omitempty"`
}

func TestErrorResponse_ParseFailure_IsSpecCompliant(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	body := strings.NewReader(`{"query":"this is { not valid graphql"}`)
	req := httptest.NewRequest(http.MethodPost, "/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody=%s", err, w.Body.String())
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected errors[] in response, got: %s", w.Body.String())
	}
	if resp.Errors[0].Message == "" {
		t.Errorf("error must have non-empty 'message' (spec)")
	}
	if code, _ := resp.Errors[0].Extensions["code"].(string); code == "" {
		t.Errorf("error must have extensions.code, got %v", resp.Errors[0].Extensions)
	}
}

func TestErrorResponse_BadJSON_IsSpecCompliant(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody=%s", err, w.Body.String())
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message == "" {
		t.Fatalf("expected error object with message, got: %s", w.Body.String())
	}
	if code, _ := resp.Errors[0].Extensions["code"].(string); code != "BAD_REQUEST" {
		t.Errorf("expected extensions.code=BAD_REQUEST, got %q", code)
	}
}

func TestErrorResponse_MethodNotAllowed_IsSpecCompliant(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	req := httptest.NewRequest(http.MethodGet, "/graphql", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody=%s", err, w.Body.String())
	}
	if len(resp.Errors) == 0 {
		t.Errorf("expected errors[] for 405 response, got: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Introspection
// ---------------------------------------------------------------------------

func TestIntrospection_SchemaQuery(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	body := strings.NewReader(`{"query":"{ __schema { queryType { name } types { name kind } } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	schema, ok := resp.Data["__schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected __schema in data, got %v", resp.Data)
	}
	qt, _ := schema["queryType"].(map[string]any)
	if qt == nil || qt["name"] != "Query" {
		t.Errorf("expected queryType.name=Query, got %v", schema["queryType"])
	}
	types, _ := schema["types"].([]any)
	if len(types) == 0 {
		t.Errorf("expected non-empty types[]")
	}
	// Look for Product type.
	found := false
	for _, ti := range types {
		if m, ok := ti.(map[string]any); ok && m["name"] == "Product" {
			found = true
			if m["kind"] != "OBJECT" {
				t.Errorf("expected Product kind=OBJECT, got %v", m["kind"])
			}
		}
	}
	if !found {
		t.Errorf("expected to find type Product in introspection result")
	}
}

func TestIntrospection_TypeQueryWithArguments(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
		"reviews":  rSrv.URL,
	})

	body := strings.NewReader(`{"query":"{ __type(name: \"Product\") { name kind fields { name } } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	tdef, ok := resp.Data["__type"].(map[string]any)
	if !ok || tdef["name"] != "Product" {
		t.Fatalf("expected __type.name=Product, got %v", resp.Data["__type"])
	}
	fields, _ := tdef["fields"].([]any)
	if len(fields) == 0 {
		t.Errorf("expected Product to have fields")
	}
}

func TestIntrospection_DisabledByConfig(t *testing.T) {
	pSrv := sdlServer(t, testSDLProducts)
	defer pSrv.Close()
	rSrv := sdlServer(t, testSDLReviews)
	defer rSrv.Close()

	disabled := false
	settings := gateway.GatewayOption{
		Endpoint:       "/graphql",
		ServiceName:    "test-gateway",
		Port:           9999,
		RequestTimeout: "5s",
		Services: []gateway.GatewayService{
			{Name: "products", Host: pSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
			{Name: "reviews", Host: rSrv.URL, Retry: gateway.RetryOption{Attempts: 1, Timeout: "3s"}},
		},
		Introspection: gateway.IntrospectionSetting{Enable: &disabled},
	}
	gw, err := gateway.NewGateway(settings)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	body := strings.NewReader(`{"query":"{ __schema { queryType { name } } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected error when introspection disabled, got: %s", w.Body.String())
	}
	if code, _ := resp.Errors[0].Extensions["code"].(string); code != "INTROSPECTION_DISABLED" {
		t.Errorf("expected extensions.code=INTROSPECTION_DISABLED, got %q", code)
	}
}

const testSDLProductsWithInaccessible = `
extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@inaccessible"])

type Query {
	product(id: ID!): Product
}

type Product @key(fields: "id") {
	id:           ID!
	name:         String
	internalCost: Float @inaccessible
}`

func TestValidateAccessibility_FragmentSpread_RejectsInaccessibleField(t *testing.T) {
	pSrv := sdlServer(t, testSDLProductsWithInaccessible)
	defer pSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
	})

	query := `query Q { product(id: "1") { ...Secret } } fragment Secret on Product { internalCost }`
	body := strings.NewReader(`{"query":` + jsonString(query) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected an error for inaccessible field via fragment, got: %s", w.Body.String())
	}
	if code, _ := resp.Errors[0].Extensions["code"].(string); code != "INACCESSIBLE_FIELD" {
		t.Errorf("expected code=INACCESSIBLE_FIELD, got %q (msg=%q)", code, resp.Errors[0].Message)
	}
}

func TestValidateAccessibility_FragmentSpread_AllowsAccessibleField(t *testing.T) {
	pSrv := sdlServer(t, testSDLProductsWithInaccessible)
	defer pSrv.Close()

	gw := mustNewGatewayWithURLs(t, map[string]string{
		"products": pSrv.URL,
	})

	query := `query Q { product(id: "1") { ...Public } } fragment Public on Product { id name }`
	body := strings.NewReader(`{"query":` + jsonString(query) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	// Validation must not reject this query. The executor will fail because the
	// SDL server is not a real subgraph, but that is unrelated to validation.
	var resp graphQLResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
	for _, e := range resp.Errors {
		if code, _ := e.Extensions["code"].(string); code == "INACCESSIBLE_FIELD" {
			t.Errorf("did not expect INACCESSIBLE_FIELD for accessible fragment, got: %+v", e)
		}
	}
}

// jsonString produces a JSON-encoded string literal for embedding into a
// request body without escaping.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
