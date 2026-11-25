package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clauderelay/internal/config"
)

func TestRewriteModelUsesAlias(t *testing.T) {
	cfg := &config.Config{
		DefaultUpstreamModel: "fallback-model",
		ModelAliases: map[string]string{
			"claude-fable-5": "cn:deepseek-v4.1-flash",
		},
	}
	input := []byte(`{"model":"claude-fable-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)

	result, err := rewriteModel(input, "application/json; charset=utf-8", "/v1/messages", cfg)
	if err != nil {
		t.Fatalf("rewriteModel returned error: %v", err)
	}
	if result.requestedModel != "claude-fable-5" {
		t.Fatalf("requestedModel = %q", result.requestedModel)
	}
	if result.upstreamModel != "cn:deepseek-v4.1-flash" {
		t.Fatalf("upstreamModel = %q", result.upstreamModel)
	}

	var payload map[string]any
	if err := json.Unmarshal(result.body, &payload); err != nil {
		t.Fatalf("rewritten body is invalid JSON: %v", err)
	}
	if payload["model"] != "cn:deepseek-v4.1-flash" {
		t.Fatalf("body model = %#v", payload["model"])
	}
	if payload["max_tokens"] != float64(64) {
		t.Fatalf("max_tokens was not preserved: %#v", payload["max_tokens"])
	}
}

func TestRewriteModelUsesDefaultForUnknownAlias(t *testing.T) {
	cfg := &config.Config{
		DefaultUpstreamModel: "fallback-model",
		ModelAliases:         map[string]string{},
	}
	input := []byte(`{"model":"unknown-model","messages":[]}`)

	result, err := rewriteModel(input, "application/json", "/v1/messages", cfg)
	if err != nil {
		t.Fatalf("rewriteModel returned error: %v", err)
	}
	if result.upstreamModel != "fallback-model" {
		t.Fatalf("upstreamModel = %q", result.upstreamModel)
	}
}

func TestRewriteModelIgnoresNonMessagePath(t *testing.T) {
	cfg := &config.Config{
		DefaultUpstreamModel: "fallback-model",
		ModelAliases:         map[string]string{"claude-fable-5": "mapped-model"},
	}
	input := []byte(`{"model":"claude-fable-5"}`)

	result, err := rewriteModel(input, "application/json", "/v1/other", cfg)
	if err != nil {
		t.Fatalf("rewriteModel returned error: %v", err)
	}
	if string(result.body) != string(input) {
		t.Fatalf("body changed unexpectedly: %s", result.body)
	}
	if result.upstreamModel != "" {
		t.Fatalf("upstreamModel should be empty, got %q", result.upstreamModel)
	}
}

func TestModelsExposeDisplayName(t *testing.T) {
	server := &Server{
		cfg: &config.Config{
			ListenHost: "127.0.0.1",
			ListenPort: 17863,
			Models: []config.Model{
				{ID: "claude-fable-5", DisplayName: "Fable 5.2", CreatedAt: "2025-01-01T00:00:00Z"},
			},
		},
		version: "test",
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	recorder := httptest.NewRecorder()
	server.serveHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}

	var response modelCatalogResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("model count = %d", len(response.Data))
	}
	if response.Data[0].ID != "claude-fable-5" || response.Data[0].DisplayName != "Fable 5.2" {
		t.Fatalf("unexpected model: %#v", response.Data[0])
	}
}

func TestRewriteModelPreservesOtherBytes(t *testing.T) {
	cfg := &config.Config{
		ModelAliases: map[string]string{"claude-fable-5": "upstream-model"},
	}
	// Deliberately unusual formatting: key order, spacing, and number forms
	// must all survive untouched, because only the model value is spliced.
	input := []byte(`{ "max_tokens" : 1e3 , "model" : "claude-fable-5" , "messages" : [ { "role" : "user" , "content" : "hi" } ] }`)

	result, err := rewriteModel(input, "application/json", "/v1/messages", cfg)
	if err != nil {
		t.Fatalf("rewriteModel returned error: %v", err)
	}
	want := `{ "max_tokens" : 1e3 , "model" : "upstream-model" , "messages" : [ { "role" : "user" , "content" : "hi" } ] }`
	if string(result.body) != want {
		t.Fatalf("body = %s\nwant    %s", result.body, want)
	}
	if !json.Valid(result.body) {
		t.Fatalf("rewritten body is invalid JSON")
	}
	if result.requestedModel != "claude-fable-5" || result.upstreamModel != "upstream-model" {
		t.Fatalf("requested=%q upstream=%q", result.requestedModel, result.upstreamModel)
	}
}

func TestRewriteModelLastDuplicateWins(t *testing.T) {
	cfg := &config.Config{
		ModelAliases: map[string]string{
			"first-model":  "first-upstream",
			"second-model": "second-upstream",
		},
	}
	input := []byte(`{"model":"first-model","model":"second-model","messages":[]}`)

	result, err := rewriteModel(input, "application/json", "/v1/messages", cfg)
	if err != nil {
		t.Fatalf("rewriteModel returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.body, &payload); err != nil {
		t.Fatalf("rewritten body is invalid JSON: %v", err)
	}
	if payload["model"] != "second-upstream" {
		t.Fatalf("model = %#v", payload["model"])
	}
}

func TestRewriteModelSkipsNonStringOrMissingModel(t *testing.T) {
	cfg := &config.Config{
		ModelAliases:         map[string]string{"claude-fable-5": "upstream-model"},
		DefaultUpstreamModel: "fallback-model",
	}
	for _, input := range []string{
		`{"model":123,"messages":[]}`,
		`{"model":null,"messages":[]}`,
		`{"messages":[]}`,
		`["not","an","object"]`,
	} {
		result, err := rewriteModel([]byte(input), "application/json", "/v1/messages", cfg)
		if err != nil {
			t.Fatalf("%s: rewriteModel returned error: %v", input, err)
		}
		if string(result.body) != input {
			t.Fatalf("%s: body changed to %s", input, result.body)
		}
		if result.upstreamModel != "" {
			t.Fatalf("%s: upstreamModel = %q", input, result.upstreamModel)
		}
	}
}

func TestRewriteModelEmptyModelKeepsBody(t *testing.T) {
	cfg := &config.Config{
		DefaultUpstreamModel: "fallback-model",
		ModelAliases:         map[string]string{},
	}
	input := `{"model":"  ","messages":[]}`

	result, err := rewriteModel([]byte(input), "application/json", "/v1/messages", cfg)
	if err != nil {
		t.Fatalf("rewriteModel returned error: %v", err)
	}
	if string(result.body) != input {
		t.Fatalf("body changed to %s", result.body)
	}
}

func TestProxyRewritesModelAndForwardsHeaders(t *testing.T) {
	var (
		gotModel     string
		gotAPIKey    string
		gotPath      string
		gotRawQuery  string
		responseBody = `{"type":"message","content":[]}`
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotAPIKey = r.Header.Get("x-api-key")
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		gotModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseBody))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		UpstreamBaseURL:      upstream.URL,
		MaxRequestBytes:      1 << 20,
		DefaultUpstreamModel: "fallback-model",
		ModelAliases:         map[string]string{"claude-fable-5": "cn:deepseek-v4.1-flash"},
	}
	server, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	front := httptest.NewServer(server.Handler())
	defer front.Close()

	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/messages?beta=true",
		strings.NewReader(`{"model":"claude-fable-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-test-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if string(body) != responseBody {
		t.Fatalf("response body = %s", body)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotRawQuery != "beta=true" {
		t.Fatalf("upstream query = %q", gotRawQuery)
	}
	if gotAPIKey != "sk-test-key" {
		t.Fatalf("x-api-key was not forwarded: %q", gotAPIKey)
	}
	if gotModel != "cn:deepseek-v4.1-flash" {
		t.Fatalf("upstream model = %q", gotModel)
	}
}

func TestProxyStreamsNonRewrittenBody(t *testing.T) {
	const payload = "raw-upload-bytes-that-are-not-json"

	received := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		UpstreamBaseURL: upstream.URL,
		// A tiny limit proves the body is not buffered through readBody on
		// paths that do not need a rewrite.
		MaxRequestBytes: 4,
	}
	server, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	front := httptest.NewServer(server.Handler())
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/files", "application/octet-stream", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := <-received; got != payload {
		t.Fatalf("upstream body = %q", got)
	}
}

func TestProxyRejectsOversizedRewriteBody(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		UpstreamBaseURL: upstream.URL,
		MaxRequestBytes: 64,
	}
	server, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	front := httptest.NewServer(server.Handler())
	defer front.Close()

	big := `{"model":"claude-fable-5","messages":[{"role":"user","content":"` + strings.Repeat("x", 256) + `"}]}`
	resp, err := http.Post(front.URL+"/v1/messages", "application/json", strings.NewReader(big))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if called {
		t.Fatalf("upstream should not have been called")
	}
}

func TestProxyTimesOutWhenUpstreamStalls(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never respond until the test finishes
	}))
	defer upstream.Close()
	defer close(release)

	one := 1
	cfg := &config.Config{
		UpstreamBaseURL:              upstream.URL,
		MaxRequestBytes:              1 << 20,
		ResponseHeaderTimeoutSeconds: &one,
	}
	server, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	front := httptest.NewServer(server.Handler())
	defer front.Close()

	start := time.Now()
	resp, err := http.Post(front.URL+"/v1/files", "application/octet-stream", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("request took %v; the timeout did not fire", elapsed)
	}
}

func TestProxyZeroTimeoutWaitsForSlowUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	zero := 0
	cfg := &config.Config{
		UpstreamBaseURL:              upstream.URL,
		MaxRequestBytes:              1 << 20,
		ResponseHeaderTimeoutSeconds: &zero,
	}
	server, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	front := httptest.NewServer(server.Handler())
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/files", "application/octet-stream", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; a disabled timeout should not fail a slow upstream", resp.StatusCode)
	}
}
