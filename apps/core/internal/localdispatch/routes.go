package localdispatch

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

func (q *Queue) Routes(_ *http.ServeMux, protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/tasks/{id}/sandbox-dispatch", func(w http.ResponseWriter, r *http.Request) {
		v, err := q.latest(r.Context(), r.PathValue("id"))
		respond(w, http.StatusOK, map[string]any{"dispatch": v}, err)
	})
	protect("POST /api/v1/tasks/{id}/sandbox-dispatch", func(w http.ResponseWriter, r *http.Request) {
		if !emptyObject(w, r) {
			respond(w, 0, nil, errInvalid)
			return
		}
		v, err := q.enqueue(r.Context(), r.PathValue("id"))
		respond(w, http.StatusAccepted, map[string]any{"dispatch": v}, err)
	})
	protect("POST /api/v1/sandbox-dispatch/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !emptyObject(w, r) {
			respond(w, 0, nil, errInvalid)
			return
		}
		v, err := q.cancel(r.Context(), r.PathValue("id"))
		respond(w, http.StatusOK, map[string]any{"dispatch": v}, err)
	})
}

func emptyObject(w http.ResponseWriter, r *http.Request) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	var body *struct{}
	var extra json.RawMessage
	return d.Decode(&body) == nil && body != nil && errors.Is(d.Decode(&extra), io.EOF)
}

func respond(w http.ResponseWriter, status int, data any, err error) {
	if err != nil {
		code, msg := "internal", "无法更新沙盒请求"
		switch {
		case errors.Is(err, errNotFound):
			status, code, msg = 404, "not_found", err.Error()
		case errors.Is(err, errInvalid):
			status, code, msg = 400, "invalid", err.Error()
		case errors.Is(err, errBusy), errors.Is(err, errConflict), errors.Is(err, errLimit), errors.Is(err, errIneligible):
			status, code, msg = 409, "conflict", err.Error()
		default:
			status = 500
			slog.Warn("sandbox dispatch transaction failed", "error", err)
		}
		data = map[string]any{"error": map[string]string{"code": code, "message": msg}}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
