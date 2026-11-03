// Package httpx holds the transport pieces shared by every feature package: routing,
// JSON helpers, error mapping and middleware.
package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
)

// Sentinel errors. Feature packages return errors that wrap one of them (see Errorf)
// and the handlers answer with the matching status through WriteError.
var (
	ErrNotFound      = errors.New("not found")
	ErrInvalid       = errors.New("invalid request")
	ErrUnprocessable = errors.New("unprocessable")
	ErrConflict      = errors.New("conflict")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrUnavailable   = errors.New("service unavailable")
)

// kindError carries a message for the client and the sentinel that picks the status.
type kindError struct {
	kind error
	msg  string
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }

// Errorf returns an error with the given client-facing message that maps to the status of kind.
func Errorf(kind error, format string, args ...any) error {
	return &kindError{kind: kind, msg: fmt.Sprintf(format, args...)}
}

// ErrorBody is the JSON shape of every error response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the content of ErrorBody.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusOf maps an error to its HTTP status and error code. Unknown errors are 500.
func StatusOf(err error) (int, string) {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, ErrInvalid):
		return http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrUnprocessable):
		return http.StatusUnprocessableEntity, "unprocessable"
	case errors.Is(err, ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, ErrUnavailable):
		return http.StatusServiceUnavailable, "unavailable"
	default:
		return http.StatusInternalServerError, "internal"
	}
}

// WriteError answers with the status of err and the {"error":{"code","message"}} body.
// Internal errors never reach the client: their message is generic.
func WriteError(w http.ResponseWriter, err error) {
	status, code := StatusOf(err)
	message := "internal server error"
	if status != http.StatusInternalServerError {
		message = err.Error()
	}
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// Fail writes the error response for err. Server errors are logged first, at error level,
// with the error, the caller of Fail and attrs (the inputs the failing call used).
func Fail(w http.ResponseWriter, log *slog.Logger, msg string, err error, attrs ...any) {
	if status, _ := StatusOf(err); status >= http.StatusInternalServerError {
		args := []any{"err", err, "caller", callerOf(2)}
		log.Error(msg, append(args, attrs...)...)
	}
	WriteError(w, err)
}

// callerOf returns "dir/file.go:line" of the function skip frames above callerOf.
func callerOf(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s/%s:%d", filepath.Base(filepath.Dir(file)), filepath.Base(file), line)
}
