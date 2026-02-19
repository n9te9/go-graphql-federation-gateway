package gateway

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/goccy/go-json"
)

// serviceSDLResponse is the response body from a subgraph's GraphQL endpoint
// when queried with `{ _service { sdl } }`.
type serviceSDLResponse struct {
	Data struct {
		Service struct {
			SDL string `json:"sdl"`
		} `json:"_service"`
	} `json:"data"`
}

// RetryOption defines the retry configuration for SDL fetching.
type RetryOption struct {
	Attempts int    `yaml:"attempts" default:"3"`
	Timeout  string `yaml:"timeout"  default:"5s"`
}

// fetchSDL fetches the SDL by sending { _service { sdl } } to the subgraph's GraphQL
// endpoint (host). It retries up to attempts times, each with a per-attempt timeout.
func fetchSDL(host string, httpClient *http.Client, retry RetryOption) (string, error) {
	attempts := retry.Attempts
	if attempts <= 0 {
		attempts = 1
	}

	timeoutDuration := 5 * time.Second
	if retry.Timeout != "" {
		if d, err := time.ParseDuration(retry.Timeout); err == nil {
			timeoutDuration = d
		}
	}

	body := []byte(`{"query":"{_service{sdl}}"}`)

	var lastErr error
	for i := 0; i < attempts; i++ {
		sdl, err := doFetchSDL(host, httpClient, body, timeoutDuration)
		if err == nil {
			return sdl, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("failed to fetch SDL from %s after %d attempt(s): %w", host, attempts, lastErr)
}

// doFetchSDL performs a single SDL fetch attempt with the given timeout.
// It POSTs the introspection query directly to host (which should be the subgraph's
// GraphQL endpoint, e.g. http://localhost:8101/query).
func doFetchSDL(host string, httpClient *http.Client, body []byte, timeout time.Duration) (string, error) {
	client := httpClient
	if timeout > 0 {
		client = &http.Client{
			Timeout:   timeout,
			Transport: httpClient.Transport,
		}
	}

	resp, err := client.Post(host, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, host)
	}

	var svcResp serviceSDLResponse
	if err := json.NewDecoder(resp.Body).Decode(&svcResp); err != nil {
		return "", fmt.Errorf("failed to decode SDL response: %w", err)
	}

	if svcResp.Data.Service.SDL == "" {
		return "", fmt.Errorf("empty SDL returned from %s", host)
	}

	return svcResp.Data.Service.SDL, nil
}
