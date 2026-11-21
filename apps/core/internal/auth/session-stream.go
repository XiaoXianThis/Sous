package auth

import (
	"context"
	"net/http"
	"time"
)

// SessionStream applies the same login requirement as an ordinary request,
// and ties a long-lived local stream to the session that opened it. HTTP
// middleware otherwise checks cookies only during the WebSocket handshake.
func (a *Auth) SessionStream(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(a.cookieName(SessionCookie))
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		id := hash(c.Value)
		a.streamMu.Lock()
		var deadline time.Time
		if session, ok := a.desktop.session(id); ok {
			deadline = session.expires
			err = nil
		} else {
			deadline, err = a.st.SessionExpires(r.Context(), id)
		}
		if err != nil {
			a.streamMu.Unlock()
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		if a.streams[id] == nil {
			a.streams[id] = make(map[context.Context]context.CancelFunc)
		}
		a.streams[id][ctx] = cancel
		a.streamMu.Unlock()
		defer func() {
			cancel()
			a.streamMu.Lock()
			delete(a.streams[id], ctx)
			if len(a.streams[id]) == 0 {
				delete(a.streams, id)
			}
			a.streamMu.Unlock()
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Auth) endStreams(id string) {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	for _, cancel := range a.streams[id] {
		cancel()
	}
}
