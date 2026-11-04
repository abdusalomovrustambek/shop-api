package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain wraps h in mws; the first middleware is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// requestState lets code deep in the handler chain report facts (the user) to the log middleware.
type requestState struct {
	userID string
}

type stateKey struct{}

// SetUserID records the authenticated user for the request log line.
func SetUserID(ctx context.Context, id string) {
	if s, ok := ctx.Value(stateKey{}).(*requestState); ok {
		s.userID = id
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Logger writes one "request" line per request: method, path, route pattern, status,
// duration_ms, ip (from X-Forwarded-For) and user id. At debug level it also logs the
// method and path. Request headers are never logged: Authorization carries a live
// credential and Cookie may carry session secrets.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			state := &requestState{}
			req := r.WithContext(context.WithValue(r.Context(), stateKey{}, state))
			rec := &statusRecorder{ResponseWriter: w}

			if log.Enabled(req.Context(), slog.LevelDebug) {
				// Authorization must never be logged: it is a live credential.
				log.Debug("request headers", "method", r.Method, "path", r.URL.Path)
			}

			next.ServeHTTP(rec, req)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"route", req.Pattern,
				"status", status,
				"duration_ms", time.Since(start).Milliseconds(),
				"ip", clientIP(r),
				"user_id", state.userID,
			)
		})
	}
}

// clientIP returns the first X-Forwarded-For address, or the peer address without a port.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Recover turns a panic in a handler into a 500. The panic is printed to stderr in Go's
// own crash format: "panic: <value>", a blank line, then "goroutine N [running]:" and the stack.
func Recover(stderr io.Writer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				_, _ = fmt.Fprintf(stderr, "panic: %v\n\n%s", rec, debug.Stack())
				WriteError(w, errors.New("panic in handler"))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
