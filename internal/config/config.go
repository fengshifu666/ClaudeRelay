package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

const DefaultMaxRequestBytes int64 = 200 << 20

// DefaultResponseHeaderTimeoutSeconds caps how long the relay waits for the
// upstream to start responding. Streamed responses send their headers almost
// immediately, so a stalled upstream is detectable within a minute. Set the
// config field to 0 to disable the limit.
const DefaultResponseHeaderTimeoutSeconds = 60

type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	CreatedAt   string `json:"createdAt"`
}

type Config struct {
	ListenHost           string            `json:"listenHost"`
	ListenPort           int               `json:"listenPort"`
	UpstreamBaseURL      string            `json:"upstreamBaseUrl"`
	MaxRequestBytes      int64             `json:"maxRequestBytes"`
	DefaultUpstreamModel string            `json:"defaultUpstreamModel"`
	ModelAliases         map[string]string `json:"modelAliases"`
	Models               []Model           `json:"models"`
	// ResponseHeaderTimeoutSeconds: nil uses the default, 0 disables the
	// limit, and a positive value sets it.
	ResponseHeaderTimeoutSeconds *int `json:"responseHeaderTimeoutSeconds"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.ListenHost == "" {
		cfg.ListenHost = "127.0.0.1"
	}
	if cfg.ListenPort < 1 || cfg.ListenPort > 65535 {
		return nil, fmt.Errorf("listenPort must be between 1 and 65535")
	}
	if cfg.MaxRequestBytes <= 0 {
		cfg.MaxRequestBytes = DefaultMaxRequestBytes
	}
	if cfg.ResponseHeaderTimeoutSeconds == nil {
		def := DefaultResponseHeaderTimeoutSeconds
		cfg.ResponseHeaderTimeoutSeconds = &def
	}
	if *cfg.ResponseHeaderTimeoutSeconds < 0 {
		return nil, fmt.Errorf("responseHeaderTimeoutSeconds must be 0 or greater")
	}
	if cfg.ModelAliases == nil {
		cfg.ModelAliases = make(map[string]string)
	}
	for i := range cfg.Models {
		cfg.Models[i].ID = strings.TrimSpace(cfg.Models[i].ID)
		cfg.Models[i].DisplayName = strings.TrimSpace(cfg.Models[i].DisplayName)
		if cfg.Models[i].ID == "" {
			return nil, fmt.Errorf("models[%d].id is empty", i)
		}
		if cfg.Models[i].DisplayName == "" {
			cfg.Models[i].DisplayName = cfg.Models[i].ID
		}
	}

	upstream, err := url.Parse(cfg.UpstreamBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstreamBaseUrl: %w", err)
	}
	if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return nil, fmt.Errorf("upstreamBaseUrl scheme must be http or https")
	}
	if upstream.Host == "" {
		return nil, fmt.Errorf("upstreamBaseUrl host is empty")
	}
	if upstream.RawQuery != "" || upstream.Fragment != "" {
		return nil, fmt.Errorf("upstreamBaseUrl must not contain query or fragment")
	}

	return &cfg, nil
}

func (c *Config) ListenAddress() string {
	return net.JoinHostPort(c.ListenHost, fmt.Sprintf("%d", c.ListenPort))
}

// ResponseHeaderTimeout returns the configured wait for upstream response
// headers in seconds. Zero means no limit.
func (c *Config) ResponseHeaderTimeout() int {
	if c.ResponseHeaderTimeoutSeconds == nil {
		return DefaultResponseHeaderTimeoutSeconds
	}
	return *c.ResponseHeaderTimeoutSeconds
}
