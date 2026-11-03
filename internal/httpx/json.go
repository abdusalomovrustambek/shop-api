package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

const maxBodyBytes = 1 << 20

const internalErrorBody = `{"error":{"code":"internal","message":"internal server error"}}`

// WriteJSON encodes v and writes it with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.Error("encode response", "err", err)
		status = http.StatusInternalServerError
		body = []byte(internalErrorBody)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(body, '\n')); err != nil {
		slog.Debug("write response", "err", err)
	}
}

// DecodeJSON reads one JSON value from the request body into dst. Unknown fields,
// trailing data, malformed JSON and bodies over 1 MiB are ErrInvalid.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return Errorf(ErrInvalid, "invalid JSON body: %v", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Errorf(ErrInvalid, "body must contain a single JSON value")
	}
	return nil
}
