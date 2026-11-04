package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// logLines parses the JSON log lines written to buf.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("log line %q is not JSON: %v", raw, err)
		}
		lines = append(lines, m)
	}
	return lines
}

func newTestLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level}))
}

func TestLoggerWritesOneRequestLine(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouter(nil, nil)
	rt.Handle(http.MethodGet, "/api/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		SetUserID(r.Context(), "user-1")
		WriteJSON(w, http.StatusTeapot, map[string]string{"id": r.PathValue("id")})
	}, AuthNone)
	h := Chain(rt, Logger(newTestLogger(&logs, slog.LevelInfo)))

	req := httptest.NewRequest(http.MethodGet, "/api/things/42", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(httptest.NewRecorder(), req)

	lines := logLines(t, &logs)
	if len(lines) != 1 {
		t.Fatalf("%d log lines, want exactly 1: %s", len(lines), logs.String())
	}
	line := lines[0]
	want := map[string]any{
		"msg":     "request",
		"method":  "GET",
		"path":    "/api/things/42",
		"route":   "GET /api/things/{id}",
		"status":  float64(http.StatusTeapot),
		"ip":      "203.0.113.7",
		"user_id": "user-1",
	}
	for key, value := range want {
		if line[key] != value {
			t.Errorf("%s = %v, want %v", key, line[key], value)
		}
	}
	if d, ok := line["duration_ms"].(float64); !ok || d < 0 {
		t.Errorf("duration_ms = %v, want a number", line["duration_ms"])
	}
}

func TestLoggerFallsBackToThePeerAddress(t *testing.T) {
	var logs bytes.Buffer
	h := Chain(http.NotFoundHandler(), Logger(newTestLogger(&logs, slog.LevelInfo)))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope", nil))

	line := logLines(t, &logs)[0]
	if line["ip"] != "192.0.2.1" || line["status"] != float64(http.StatusNotFound) || line["user_id"] != "" {
		t.Errorf("line = %v, want the peer ip, status 404 and no user", line)
	}
}

func TestRecoverAnswers500AndPrintsTheCrashFormat(t *testing.T) {
	var logs, stderr bytes.Buffer
	rt := NewRouter(nil, nil)
	rt.Handle(http.MethodGet, "/boom", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}, AuthNone)
	rt.Handle(http.MethodGet, "/boom-error", func(http.ResponseWriter, *http.Request) {
		panic(fmt.Errorf("wrapped: %w", errors.New("inner")))
	}, AuthNone)
	h := Chain(rt, Logger(newTestLogger(&logs, slog.LevelInfo)), Recover(&stderr))

	for path, wantHead := range map[string]string{"/boom": "panic: boom\n\n", "/boom-error": "panic: wrapped: inner\n\n"} {
		stderr.Reset()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d, want 500", path, rec.Code)
		}
		wantBody := `{"error":{"code":"internal","message":"internal server error"}}`
		if strings.TrimSpace(rec.Body.String()) != wantBody {
			t.Errorf("%s: body = %s, want %s", path, rec.Body, wantBody)
		}
		out := stderr.String()
		if !strings.HasPrefix(out, wantHead+"goroutine ") {
			t.Errorf("%s: stderr starts with %q, want %q then a goroutine header", path, firstLines(out, 3), wantHead)
		}
		if !regexp.MustCompile(`(?m)^goroutine \d+ \[running\]:$`).MatchString(out) {
			t.Errorf("%s: stderr lacks the 'goroutine N [running]:' line: %s", path, out)
		}
	}

	lines := logLines(t, &logs)
	if got := lines[len(lines)-1]["status"]; got != float64(http.StatusInternalServerError) {
		t.Errorf("the request line of a panicking request has status %v, want 500", got)
	}
}

func TestRecoverLeavesNormalRequestsAlone(t *testing.T) {
	var stderr bytes.Buffer
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	}), Recover(&stderr))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK || stderr.Len() != 0 {
		t.Errorf("status %d, stderr %q, want 200 and nothing on stderr", rec.Code, stderr.String())
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}

func TestWriteErrorRendersTheErrorJSON(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"not found", Errorf(ErrNotFound, "order %d not found", 7), 404, "not_found", "order 7 not found"},
		{"invalid", Errorf(ErrInvalid, "bad limit"), 400, "invalid_request", "bad limit"},
		{"unprocessable", Errorf(ErrUnprocessable, "coupon expired"), 422, "unprocessable", "coupon expired"},
		{"conflict", Errorf(ErrConflict, "out of stock"), 409, "conflict", "out of stock"},
		{"unauthorized", Errorf(ErrUnauthorized, "no token"), 401, "unauthorized", "no token"},
		{"unavailable", Errorf(ErrUnavailable, "try later"), 503, "unavailable", "try later"},
		{"wrapped sentinel", fmt.Errorf("context: %w", Errorf(ErrNotFound, "gone")), 404, "not_found", "context: gone"},
		{"plain sentinel", ErrNotFound, 404, "not_found", "not found"},
		{"unknown error hides its text", errors.New("pq: password authentication failed"), 500, "internal", "internal server error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, tt.err)

			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %q is not JSON: %v", rec.Body, err)
			}
			if body.Error.Code != tt.code || body.Error.Message != tt.message {
				t.Errorf("error = %+v, want code %q message %q", body.Error, tt.code, tt.message)
			}
		})
	}
}

func TestFailLogsServerErrorsOnly(t *testing.T) {
	var logs bytes.Buffer
	log := newTestLogger(&logs, slog.LevelInfo)

	rec := httptest.NewRecorder()
	Fail(rec, log, "lookup failed", errors.New("db exploded"), "input", "x'y")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	lines := logLines(t, &logs)
	if len(lines) != 1 {
		t.Fatalf("%d log lines, want 1: %s", len(lines), logs.String())
	}
	line := lines[0]
	caller, _ := line["caller"].(string)
	if line["level"] != "ERROR" || line["msg"] != "lookup failed" || line["err"] != "db exploded" || line["input"] != "x'y" || !strings.HasPrefix(caller, "httpx/middleware_test.go:") {
		t.Errorf("line = %v", line)
	}

	logs.Reset()
	rec = httptest.NewRecorder()
	Fail(rec, log, "lookup failed", Errorf(ErrNotFound, "nope"))
	if rec.Code != http.StatusNotFound || logs.Len() != 0 {
		t.Errorf("a 404 gave status %d and log %q, want 404 and no log line", rec.Code, logs.String())
	}
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid", `{"name":"a"}`, false},
		{"unknown field", `{"name":"a","extra":1}`, true},
		{"malformed", `{"name":`, true},
		{"empty", ``, true},
		{"wrong type", `{"name":5}`, true},
		{"trailing data", `{"name":"a"}{"name":"b"}`, true},
		{"too large", `{"name":"` + strings.Repeat("a", 2<<20) + `"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			var p payload
			err := DecodeJSON(httptest.NewRecorder(), req, &p)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v is not ErrInvalid", err)
			}
		})
	}
}
