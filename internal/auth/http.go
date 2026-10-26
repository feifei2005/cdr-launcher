package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"cdr-launcher/internal/netproxy"
)

var httpClient = netproxy.NewClient(30 * time.Second)

const userAgent = "cdr-launcher/1.0.0"

const maxResponseBytes = 2 << 20

func send(ctx context.Context, method, endpoint, contentType string, body []byte, headers map[string]string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func postForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	return send(ctx, http.MethodPost, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()), nil)
}

func postJSON(ctx context.Context, endpoint string, payload any, headers map[string]string) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	return send(ctx, http.MethodPost, endpoint, "application/json", body, headers)
}

func getJSON(ctx context.Context, endpoint string, headers map[string]string) (int, []byte, error) {
	return send(ctx, http.MethodGet, endpoint, "", nil, headers)
}

func truncate(raw []byte, limit int) string {
	text := string(raw)
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}
