package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/n9te9/go-graphql-federation-gateway/gateway"
)

func TestFetchSDL_Success(t *testing.T) {
	wantSDL := "type Query { hello: String }"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"_service":{"sdl":"type Query { hello: String }"}}}`)) //nolint:errcheck
	}))
	defer srv.Close()
	got, err := gateway.FetchSDLForTest(srv.URL, &http.Client{}, gateway.RetryOption{Attempts: 1, Timeout: "5s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantSDL {
		t.Errorf("SDL mismatch: got %q, want %q", got, wantSDL)
	}
}

func TestFetchSDL_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := gateway.FetchSDLForTest(srv.URL, &http.Client{}, gateway.RetryOption{Attempts: 1, Timeout: "5s"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFetchSDL_EmptySDL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"_service":{"sdl":""}}}`)) //nolint:errcheck
	}))
	defer srv.Close()
	_, err := gateway.FetchSDLForTest(srv.URL, &http.Client{}, gateway.RetryOption{Attempts: 1, Timeout: "5s"})
	if err == nil {
		t.Fatal("expected error for empty SDL")
	}
}

func TestFetchSDL_Retry(t *testing.T) {
	wantSDL := "type Query { hello: String }"
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"_service":{"sdl":"type Query { hello: String }"}}}`)) //nolint:errcheck
	}))
	defer srv.Close()
	got, err := gateway.FetchSDLForTest(srv.URL, &http.Client{}, gateway.RetryOption{Attempts: 3, Timeout: "5s"})
	if err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if got != wantSDL {
		t.Errorf("SDL mismatch")
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestFetchSDL_RetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, err := gateway.FetchSDLForTest(srv.URL, &http.Client{}, gateway.RetryOption{Attempts: 2, Timeout: "5s"})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestFetchSDL_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"_service":{"sdl":"type Query { ok: Boolean }"}}}`)) //nolint:errcheck
	}))
	defer srv.Close()
	_, err := gateway.FetchSDLForTest(srv.URL, &http.Client{}, gateway.RetryOption{Attempts: 1, Timeout: "50ms"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
