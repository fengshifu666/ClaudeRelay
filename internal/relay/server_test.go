package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
