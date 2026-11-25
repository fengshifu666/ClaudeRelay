package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const minimalConfig = `{
  "listenPort": 17863,
  "upstreamBaseUrl": "http://127.0.0.1:7863"
}`

func TestLoadDefaultsResponseHeaderTimeout(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ResponseHeaderTimeout(); got != DefaultResponseHeaderTimeoutSeconds {
		t.Fatalf("ResponseHeaderTimeout = %d, want %d", got, DefaultResponseHeaderTimeoutSeconds)
	}
}

func TestLoadExplicitResponseHeaderTimeout(t *testing.T) {
	cfg, err := Load(writeConfig(t, `{
  "listenPort": 17863,
  "upstreamBaseUrl": "http://127.0.0.1:7863",
  "responseHeaderTimeoutSeconds": 5
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ResponseHeaderTimeout(); got != 5 {
		t.Fatalf("ResponseHeaderTimeout = %d, want 5", got)
	}
}

func TestLoadZeroDisablesResponseHeaderTimeout(t *testing.T) {
	cfg, err := Load(writeConfig(t, `{
  "listenPort": 17863,
  "upstreamBaseUrl": "http://127.0.0.1:7863",
  "responseHeaderTimeoutSeconds": 0
}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ResponseHeaderTimeout(); got != 0 {
		t.Fatalf("ResponseHeaderTimeout = %d, want 0 (disabled)", got)
	}
}

func TestLoadRejectsNegativeResponseHeaderTimeout(t *testing.T) {
	if _, err := Load(writeConfig(t, `{
  "listenPort": 17863,
  "upstreamBaseUrl": "http://127.0.0.1:7863",
  "responseHeaderTimeoutSeconds": -1
}`)); err == nil {
		t.Fatalf("Load accepted a negative responseHeaderTimeoutSeconds")
	}
}

func TestListenAddress(t *testing.T) {
	cfg := &Config{ListenHost: "127.0.0.1", ListenPort: 17863}
	if got := cfg.ListenAddress(); got != "127.0.0.1:17863" {
		t.Fatalf("ListenAddress = %q", got)
	}
}
