package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// dryTransport prints each request instead of sending it and answers with a canned reply,
// so the shopping flow can run end to end without a network.
type dryTransport struct {
	out io.Writer
}

func (d dryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	line := req.Method + " " + req.URL.String()
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		line += " xff=" + xff
	}
	if req.Header.Get("Authorization") != "" {
		line += " auth=bearer"
	}
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read dry-run body: %w", err)
		}
		if len(raw) > 0 {
			line += " body=" + string(raw)
		}
	}
	if _, err := fmt.Fprintln(d.out, line); err != nil {
		return nil, fmt.Errorf("print dry-run request: %w", err)
	}

	status, body := cannedReply(req)
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func cannedReply(req *http.Request) (int, string) {
	path := req.URL.Path
	switch {
	case path == "/auth/register":
		return http.StatusCreated, `{}`
	case path == "/auth/login":
		return http.StatusOK, `{"token":"dry-run-token"}`
	case path == "/api/products" && req.URL.Query().Get("search") == "":
		return http.StatusOK, `{"items":[{"id":3},{"id":4},{"id":5}],"next_cursor":null}`
	case strings.HasPrefix(path, "/api/products"):
		return http.StatusOK, `{"items":[],"next_cursor":null}`
	case path == "/api/cart/items":
		return http.StatusCreated, `{}`
	case path == "/api/checkout":
		return http.StatusCreated, `{}`
	case path == "/api/orders":
		return http.StatusOK, `[]`
	case strings.HasPrefix(path, "/api/"):
		return http.StatusOK, `{}`
	default:
		return http.StatusNotFound, `{}`
	}
}
