package cloudwork

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

const WorkerPath = "/api/v1/cloud-worker/"

// MachineRoute lists only the exact bearer-protected POST endpoints that do
// not need a browser Origin. Their own route guards still check credentials.
func MachineRoute(path string, static, managed bool) bool {
	switch path {
	case WorkerPath + "claim", WorkerPath + "heartbeat", WorkerPath + "complete":
		return static || managed
	case WorkerPath + "ready", ControllerPath + "reconcile", ControllerPath + "retired":
		return managed
	default:
		return false
	}
}

func (q *Queue) Routes(fresh func(http.ResponseWriter, *http.Request) bool) func(*http.ServeMux, func(string, http.HandlerFunc)) {
	return func(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
		protect("GET /api/v1/tasks/{id}/cloud-work", func(w http.ResponseWriter, r *http.Request) {
			j, err := q.Latest(r.Context(), r.PathValue("id"))
			result := map[string]any{"job": j}
			if err == nil && j != nil && j.Status == "queued" {
				var phase string
				phase, err = q.CaptionPhase(r.Context())
				if phase != "" {
					result["capacity_phase"] = phase
				}
			}
			reply(w, http.StatusOK, result, err)
		})
		protect("POST /api/v1/tasks/{id}/cloud-work", func(w http.ResponseWriter, r *http.Request) {
			if !fresh(w, r) {
				return
			}
			var body Authorize
			if !decode(w, r, &body) {
				reply(w, 0, nil, ErrBadInput)
				return
			}
			j, err := q.Enqueue(r.Context(), r.PathValue("id"), body)
			reply(w, http.StatusAccepted, map[string]any{"job": j}, err)
		})
		protect("POST /api/v1/cloud-work/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
			var body *struct{}
			if !decode(w, r, &body) || body == nil {
				reply(w, 0, nil, ErrBadInput)
				return
			}
			j, err := q.Cancel(r.Context(), r.PathValue("id"))
			reply(w, http.StatusOK, map[string]any{"job": j}, err)
		})
		protect("GET /api/v1/cloud-work/{id}/results", func(w http.ResponseWriter, r *http.Request) {
			files, err := q.Results(r.Context(), r.PathValue("id"))
			reply(w, http.StatusOK, map[string]any{"files": files}, err)
		})
		protect("GET /api/v1/projects/{id}/cloud-work-results", func(w http.ResponseWriter, r *http.Request) {
			results, err := q.ProjectResults(r.Context(), r.PathValue("id"))
			reply(w, http.StatusOK, map[string]any{"results": results}, err)
		})
		worker := func(path string, h http.HandlerFunc) { mux.HandleFunc("POST "+WorkerPath+path, q.guard(h)) }
		if q.managed() {
			worker("ready", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					SessionHash string `json:"session_sha256"`
				}
				if !decode(w, r, &in) {
					reply(w, 0, nil, ErrBadInput)
					return
				}
				if err := q.Register(r.Context(), in.SessionHash); errors.Is(err, ErrWorkerIdentity) {
					hidden(w, r)
				} else {
					reply(w, http.StatusOK, map[string]bool{"ready": true}, err)
				}
			})
			q.controllerRoutes(mux)
		}
		worker("claim", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Workload string `json:"workload"`
			}
			if !decode(w, r, &body) || body.Workload != Workload {
				reply(w, 0, nil, ErrBadInput)
				return
			}
			claim, err := q.Claim(r.Context())
			reply(w, http.StatusOK, map[string]any{"claim": claim}, err)
		})
		worker("heartbeat", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				ID    string `json:"job_id"`
				Lease string `json:"lease"`
			}
			if !decode(w, r, &body) {
				reply(w, 0, nil, ErrBadInput)
				return
			}
			stop, err := q.Heartbeat(r.Context(), body.ID, body.Lease)
			reply(w, http.StatusOK, map[string]bool{"stop": stop}, err)
		})
		worker("complete", func(w http.ResponseWriter, r *http.Request) {
			var body Complete
			if !decode(w, r, &body) {
				reply(w, 0, nil, ErrBadInput)
				return
			}
			j, err := q.Complete(r.Context(), body)
			reply(w, http.StatusOK, j, err)
		})
	}
}

func bearer(r *http.Request) (string, bool) {
	if len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("Cookie")) != 0 {
		return "", false
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !ValidToken(tok) {
		return "", false
	}
	return Hash([]byte(tok)), true
}

func hidden(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.NotFound(w, r)
}

func (q *Queue) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := bearer(r)
		gen := r.Header.Values(GenerationHeader)
		if !ok {
			hidden(w, r)
			return
		}
		if !q.managed() {
			if len(gen) != 0 || !sameHash(hash, q.tokenHash) {
				hidden(w, r)
				return
			}
			next(w, r)
			return
		}
		if len(gen) != 1 || sameHash(hash, q.controllerHash) {
			hidden(w, r)
			return
		}
		v, err := strconv.ParseInt(gen[0], 10, 64)
		if err != nil || v < 1 || strconv.FormatInt(v, 10) != gen[0] {
			hidden(w, r)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), workerIdentityKey{}, workerIdentity{v, hash})))
	}
}

func (q *Queue) controllerRoutes(mux *http.ServeMux) {
	protect := func(action string, next http.HandlerFunc) {
		mux.HandleFunc("POST "+ControllerPath+action, func(w http.ResponseWriter, r *http.Request) {
			hash, ok := bearer(r)
			if !ok || !sameHash(hash, q.controllerHash) || len(r.Header.Values(GenerationHeader)) != 0 {
				hidden(w, r)
				return
			}
			next(w, r)
		})
	}
	protect("reconcile", func(w http.ResponseWriter, r *http.Request) {
		var in ReconcileCapacity
		if !decode(w, r, &in) {
			reply(w, 0, nil, ErrBadInput)
			return
		}
		state, err := q.Plan(r.Context(), in)
		reply(w, http.StatusOK, state, err)
	})
	protect("retired", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Generation  int64  `json:"generation"`
			Reservation string `json:"reservation"`
		}
		if !decode(w, r, &in) {
			reply(w, 0, nil, ErrBadInput)
			return
		}
		state, err := q.ConfirmRetired(r.Context(), in.Generation, in.Reservation)
		reply(w, http.StatusOK, state, err)
	})
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	d.DisallowUnknownFields()
	var extra any
	return d.Decode(out) == nil && errors.Is(d.Decode(&extra), io.EOF)
}

func reply(w http.ResponseWriter, status int, data any, err error) {
	if err != nil {
		code := "internal"
		msg := "暂时无法更新云执行状态"
		switch {
		case errors.Is(err, ErrBadInput):
			status, code, msg = 400, "invalid_cloud_work", err.Error()
		case errors.Is(err, ErrMissing), errors.Is(err, ErrWorkerIdentity):
			status, code, msg = 404, "not_found", err.Error()
		case errors.Is(err, ErrConflict):
			status, code, msg = 409, "cloud_conflict", err.Error()
		case errors.Is(err, ErrCapacity):
			status, code, msg = 429, "cloud_capacity", err.Error()
		default:
			status = 500
			slog.Warn("cloud work database operation failed", "error", err)
		}
		data = map[string]any{"error": map[string]string{"code": code, "message": msg}}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
