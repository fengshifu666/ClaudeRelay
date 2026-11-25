package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"clauderelay/internal/config"
)

var errRequestTooLarge = errors.New("request body exceeds configured limit")

var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"proxy-connection":    {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

type Server struct {
	cfg     *config.Config
	baseURL *url.URL
	client  *http.Client
	version string
}

type modelEntry struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type modelCatalogResponse struct {
	Data    []modelEntry `json:"data"`
	HasMore bool         `json:"has_more"`
	FirstID *string      `json:"first_id"`
	LastID  *string      `json:"last_id"`
}

func New(cfg *config.Config, version string) (*Server, error) {
	baseURL, err := url.Parse(cfg.UpstreamBaseURL)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}

	return &Server{
		cfg:     cfg,
		baseURL: baseURL,
		client:  &http.Client{Transport: transport},
		version: version,
	}, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		s.handleHealth(w)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		s.handleModels(w)
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.proxy(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"service":  "ClaudeRelay",
		"version":  s.version,
		"pid":      os.Getpid(),
		"listen":   s.cfg.ListenAddress(),
		"upstream": s.cfg.UpstreamBaseURL,
		"aliases":  s.cfg.ModelAliases,
	})
}

func (s *Server) handleModels(w http.ResponseWriter) {
	models := make([]modelEntry, 0, len(s.cfg.Models))
	for _, model := range s.cfg.Models {
		models = append(models, modelEntry{
			Type:        "model",
			ID:          model.ID,
			DisplayName: model.DisplayName,
			CreatedAt:   model.CreatedAt,
		})
	}

	response := modelCatalogResponse{Data: models, HasMore: false}
	if len(models) > 0 {
		firstID := models[0].ID
		lastID := models[len(models)-1].ID
		response.FirstID = &firstID
		response.LastID = &lastID
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	target := *s.baseURL
	target.Path = joinURLPath(s.baseURL.Path, r.URL.Path)
	target.RawPath = ""
	target.RawQuery = r.URL.RawQuery

	var (
		body    io.Reader
		bodyLen int64 = r.ContentLength
	)
	if needsRewrite(r.Header.Get("Content-Type"), r.URL.Path) {
		buffered, err := readBody(r, s.cfg.MaxRequestBytes)
		if err != nil {
			if errors.Is(err, errRequestTooLarge) {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", err.Error())
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}

		rewritten, err := rewriteModel(buffered, r.Header.Get("Content-Type"), r.URL.Path, s.cfg)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "failed to decode JSON request body")
			return
		}
		body = bytes.NewReader(rewritten.body)
		bodyLen = int64(len(rewritten.body))
	} else {
		// No rewrite is needed, so stream the body straight through instead of
		// holding the whole thing in memory. Uploads can be large.
		body = r.Body
		if r.Body != nil {
			defer r.Body.Close()
		}
	}

	outbound, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), body)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "api_error", "failed to create upstream request")
		return
	}
	copyHeaders(outbound.Header, r.Header)
	outbound.Header.Del("Content-Length")
	outbound.ContentLength = bodyLen

	response, err := s.client.Do(outbound)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "api_error", "ClaudeRelay upstream error: "+err.Error())
		return
	}
	defer response.Body.Close()

	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = copyAndFlush(w, response.Body)
}

type rewrittenBody struct {
	body           []byte
	requestedModel string
	upstreamModel  string
}

// needsRewrite reports whether a request body has to be buffered and inspected
// for a model rewrite. Anything else is streamed straight to the upstream.
func needsRewrite(contentType, pathname string) bool {
	return isJSONContentType(contentType) && isMessagePath(pathname)
}

func rewriteModel(body []byte, contentType, pathname string, cfg *config.Config) (rewrittenBody, error) {
	result := rewrittenBody{body: body}
	if len(body) == 0 || !needsRewrite(contentType, pathname) {
		return result, nil
	}

	// Only the top-level "model" value is ever changed, so locate its byte span
	// and splice a new JSON string into the original body. Decoding the whole
	// request into a map and re-encoding it would double or triple peak memory
	// on large bodies (images, long histories) that are otherwise passed through
	// untouched.
	span, requested, ok, err := findTopLevelModelString(body)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, nil
	}

	upstream := cfg.ModelAliases[requested]
	if upstream == "" {
		upstream = cfg.DefaultUpstreamModel
	}
	if upstream == "" {
		upstream = requested
	}
	if upstream == requested {
		result.requestedModel = requested
		result.upstreamModel = requested
		return result, nil
	}

	encodedModel, err := json.Marshal(upstream)
	if err != nil {
		return result, err
	}

	spliced := make([]byte, 0, len(body)+len(encodedModel))
	spliced = append(spliced, body[:span.start]...)
	spliced = append(spliced, encodedModel...)
	spliced = append(spliced, body[span.end:]...)
	if !json.Valid(spliced) {
		return result, errors.New("model rewrite produced invalid JSON")
	}

	result.body = spliced
	result.requestedModel = requested
	result.upstreamModel = upstream
	return result, nil
}

type byteSpan struct {
	start int64
	end   int64
}

// findTopLevelModelString returns the byte span of the top-level "model" value
// and its decoded string value. When the key appears more than once, the last
// occurrence wins, matching the behavior of json.Unmarshal into a map.
func findTopLevelModelString(body []byte) (byteSpan, string, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil {
		return byteSpan{}, "", false, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		// Not a JSON object: nothing to rewrite.
		return byteSpan{}, "", false, nil
	}

	var (
		last       byteSpan
		value      string
		found      bool
		lastIsText bool
	)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return byteSpan{}, "", false, err
		}
		key, isString := keyToken.(string)
		isModel := isString && key == "model"

		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return byteSpan{}, "", false, err
		}

		if isModel {
			start := decoder.InputOffset() - int64(len(raw))
			last = byteSpan{start: start, end: start + int64(len(raw))}
			found = true
			var str string
			lastIsText = json.Unmarshal(raw, &str) == nil
			value = str
		}
	}

	if _, err := decoder.Token(); err != nil {
		return byteSpan{}, "", false, err
	}

	if !found || !lastIsText {
		return byteSpan{}, "", false, nil
	}
	if strings.TrimSpace(value) == "" {
		return byteSpan{}, "", false, nil
	}
	return last, value, true, nil
}

func isJSONContentType(value string) bool {
	return strings.Contains(strings.ToLower(value), "application/json")
}

func isMessagePath(path string) bool {
	return strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/messages/count_tokens")
}

func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	if r.ContentLength > maxBytes {
		return nil, errRequestTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errRequestTooLarge
	}
	return body, nil
}

func copyHeaders(dst, src http.Header) {
	drop := make(map[string]struct{}, len(hopByHopHeaders))
	for key := range hopByHopHeaders {
		drop[key] = struct{}{}
	}
	for _, connection := range src.Values("Connection") {
		for _, token := range strings.Split(connection, ",") {
			if token = strings.TrimSpace(strings.ToLower(token)); token != "" {
				drop[token] = struct{}{}
			}
		}
	}
	for key, values := range src {
		if _, blocked := drop[strings.ToLower(key)]; blocked {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyAndFlush(w http.ResponseWriter, reader io.Reader) (int64, error) {
	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			count, writeErr := w.Write(buffer[:n])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func joinURLPath(base, request string) string {
	if base == "" || base == "/" {
		return request
	}
	base = strings.TrimSuffix(base, "/")
	if request == "" {
		return base
	}
	if !strings.HasPrefix(request, "/") {
		request = "/" + request
	}
	return base + request
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeAPIError(w http.ResponseWriter, status int, kind, message string) {
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    kind,
			"message": message,
		},
	})
}
