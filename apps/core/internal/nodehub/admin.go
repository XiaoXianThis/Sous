package nodehub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Remote administration of a machine's agents (work/specs/agent-admin): the
// HTTP API turns each call into a node.request and waits for the answer.

var (
	ErrOffline     = errors.New("机器不在线")
	ErrUnsupported = errors.New("这台机器上的 sous 版本太旧，不支持远程管理，请先更新")
	ErrTimeout     = errors.New("机器没有及时回答")
)

const (
	requestTimeout = 20 * time.Second
	probeTimeout   = 150 * time.Second // npx may first download the agent's ACP adapter
	detectTimeout  = 90 * time.Second
	maxBodyBytes   = 2 << 20
)

// AdminState is the live state of remote administration for a node.
func (h *Hub) AdminState(nodeID string) string {
	c := h.conn(nodeID)
	switch {
	case c == nil:
		return ""
	case !c.canAdmin:
		return "unsupported"
	case !c.adminOn:
		return "off"
	}
	return "on"
}

// Request sends node.request and waits for the node's answer.
func (h *Hub) Request(ctx context.Context, nodeID, op string, args any, timeout time.Duration) (json.RawMessage, error) {
	c := h.conn(nodeID)
	if c == nil {
		return nil, ErrOffline
	}
	if !c.canAdmin {
		return nil, ErrUnsupported
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	f := proto.New(proto.TNodeRequest, proto.NodeRequest{Op: op, Args: raw})
	ch := make(chan proto.NodeResponse, 1)
	c.mu.Lock()
	c.pending[f.ID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, f.ID)
		c.mu.Unlock()
	}()
	c.send(f)
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case resp := <-ch:
		if !resp.OK {
			if resp.Error == nil {
				return nil, &proto.NodeError{Code: proto.ErrCodeFailed, Message: "机器报告失败"}
			}
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-c.ctx.Done():
		return nil, ErrOffline
	case <-t.C:
		return nil, ErrTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Hub) adminRoutes(protect func(string, http.HandlerFunc)) {
	protect("POST /api/v1/nodes/{id}/agents/detect", h.adminDetect)
	protect("POST /api/v1/nodes/{id}/agents/{agent}/probe", h.adminProbe)
	protect("POST /api/v1/nodes/{id}/agents/{agent}/install", h.adminJob(proto.OpInstall))
	protect("POST /api/v1/nodes/{id}/agents/{agent}/login", h.adminJob(proto.OpLogin))
	protect("POST /api/v1/nodes/{id}/agents/{agent}/logout", h.adminJob(proto.OpLogout))
	protect("PUT /api/v1/nodes/{id}/agents/{agent}/defaults", h.adminDefaults)
	protect("PUT /api/v1/nodes/{id}/agents/{agent}/env", h.adminEnv)
	protect("GET /api/v1/nodes/{id}/jobs/{job}", h.adminJobOp(proto.OpJobTail))
	protect("POST /api/v1/nodes/{id}/jobs/{job}/input", h.adminJobOp(proto.OpJobInput))
	protect("POST /api/v1/nodes/{id}/jobs/{job}/resize", h.adminJobOp(proto.OpJobResize))
	protect("POST /api/v1/nodes/{id}/jobs/{job}/cancel", h.adminJobOp(proto.OpJobCancel))
	protect("GET /api/v1/nodes/{id}/files/{agent}/{file}", h.adminReadFile)
	protect("PUT /api/v1/nodes/{id}/files/{agent}/{file}", h.adminWriteFile)
}

// WriteAdminError is adminError for other packages.
func WriteAdminError(w http.ResponseWriter, err error) { adminError(w, err) }

// adminError maps a request failure to an HTTP answer.
func adminError(w http.ResponseWriter, err error) {
	var ne *proto.NodeError
	switch {
	case errors.Is(err, ErrOffline):
		writeError(w, http.StatusConflict, "offline", err.Error())
	case errors.Is(err, ErrUnsupported):
		writeError(w, http.StatusConflict, "unsupported", err.Error())
	case errors.Is(err, ErrTimeout):
		writeError(w, http.StatusGatewayTimeout, "timeout", err.Error())
	case errors.As(err, &ne):
		status := map[string]int{
			proto.ErrCodeDisabled: http.StatusForbidden, proto.ErrCodeConflict: http.StatusConflict,
			proto.ErrCodeInvalid: http.StatusBadRequest, proto.ErrCodeNotFound: http.StatusNotFound,
			proto.ErrCodeBusy: http.StatusConflict,
		}[ne.Code]
		if status == 0 {
			status = http.StatusBadGateway
		}
		writeError(w, status, ne.Code, ne.Message)
	default:
		writeError(w, http.StatusBadGateway, "failed", err.Error())
	}
}

// known checks that the node exists and is not revoked.
func (h *Hub) known(w http.ResponseWriter, r *http.Request) bool {
	n, err := h.log.Store.Node(r.Context(), r.PathValue("id"))
	if err != nil || n.Revoked {
		writeError(w, http.StatusNotFound, "not_found", "机器不存在")
		return false
	}
	return true
}

func decodeBody(r *http.Request, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func relay(w http.ResponseWriter, res json.RawMessage) {
	if len(res) == 0 {
		res = json.RawMessage(`{"ok":true}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(res)
}

func (h *Hub) adminDetect(w http.ResponseWriter, r *http.Request) {
	if !h.known(w, r) {
		return
	}
	res, err := h.Request(r.Context(), r.PathValue("id"), proto.OpDetect, struct{}{}, detectTimeout)
	if err != nil {
		adminError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"agents":` + string(res) + `}`))
}

func (h *Hub) adminProbe(w http.ResponseWriter, r *http.Request) {
	if !h.known(w, r) {
		return
	}
	res, err := h.Request(r.Context(), r.PathValue("id"), proto.OpProbe, proto.AgentArgs{Agent: r.PathValue("agent")}, probeTimeout)
	if err != nil {
		adminError(w, err)
		return
	}
	relay(w, res)
}

func (h *Hub) adminJob(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.known(w, r) {
			return
		}
		var body struct {
			Upgrade bool `json:"upgrade"`
			Cols    int  `json:"cols"`
			Rows    int  `json:"rows"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", err.Error())
			return
		}
		nodeID, agent := r.PathValue("id"), r.PathValue("agent")
		res, err := h.Request(r.Context(), nodeID, op, proto.AgentArgs{Agent: agent, Upgrade: body.Upgrade, Cols: body.Cols, Rows: body.Rows}, requestTimeout)
		if err != nil {
			adminError(w, err)
			return
		}
		var js proto.JobStarted
		if json.Unmarshal(res, &js) == nil && js.JobID != "" {
			ev := event.New(event.AgentJobStarted, event.ActorUser, "", event.AgentJobPayload{
				NodeID: nodeID, JobID: js.JobID, Kind: js.Kind, Agent: js.Agent, Command: js.Command})
			if _, err := h.log.Append(r.Context(), ev); err != nil {
				internal(w, err)
				return
			}
		}
		relay(w, res)
	}
}

func (h *Hub) adminJobOp(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.known(w, r) {
			return
		}
		var body struct {
			Data string `json:"data"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if r.Method != http.MethodGet {
			if err := decodeBody(r, &body); err != nil {
				writeError(w, http.StatusBadRequest, "bad_json", err.Error())
				return
			}
		}
		if len(body.Data) > 64*1024 {
			writeError(w, http.StatusBadRequest, "too_large", "输入太长")
			return
		}
		res, err := h.Request(r.Context(), r.PathValue("id"), op, proto.JobArgs{JobID: r.PathValue("job"), Owner: h.owner(r), Data: body.Data, Cols: body.Cols, Rows: body.Rows}, requestTimeout)
		if err != nil {
			adminError(w, err)
			return
		}
		relay(w, res)
	}
}

func (h *Hub) configChanged(ctx context.Context, nodeID, agent, what string) {
	h.log.Append(ctx, event.New(event.NodeConfigChanged, event.ActorUser, "", event.NodeConfigPayload{NodeID: nodeID, Agent: agent, What: what}))
}

func (h *Hub) adminDefaults(w http.ResponseWriter, r *http.Request) {
	if !h.known(w, r) {
		return
	}
	var body struct {
		Options map[string]string `json:"options"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	nodeID, agent := r.PathValue("id"), r.PathValue("agent")
	if _, err := h.Request(r.Context(), nodeID, proto.OpSetDefaults, proto.AgentArgs{Agent: agent, Options: body.Options}, requestTimeout); err != nil {
		adminError(w, err)
		return
	}
	h.configChanged(r.Context(), nodeID, agent, "defaults")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Hub) adminEnv(w http.ResponseWriter, r *http.Request) {
	if !h.known(w, r) {
		return
	}
	var body struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := decodeBody(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_json", "需要变量名")
		return
	}
	nodeID, agent := r.PathValue("id"), r.PathValue("agent")
	args := proto.AgentArgs{Agent: agent, Name: body.Name, Value: strings.TrimSpace(body.Value)}
	if _, err := h.Request(r.Context(), nodeID, proto.OpSetEnv, args, requestTimeout); err != nil {
		adminError(w, err)
		return
	}
	h.configChanged(r.Context(), nodeID, agent, "env:"+body.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Hub) adminReadFile(w http.ResponseWriter, r *http.Request) {
	if !h.known(w, r) {
		return
	}
	args := proto.ConfigArgs{Agent: r.PathValue("agent"), File: r.PathValue("file")}
	res, err := h.Request(r.Context(), r.PathValue("id"), proto.OpConfigRead, args, requestTimeout)
	if err != nil {
		adminError(w, err)
		return
	}
	relay(w, res)
}

func (h *Hub) adminWriteFile(w http.ResponseWriter, r *http.Request) {
	if !h.known(w, r) {
		return
	}
	var body struct {
		Content    string `json:"content"`
		BaseSHA256 string `json:"base_sha256"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	nodeID := r.PathValue("id")
	args := proto.ConfigArgs{Agent: r.PathValue("agent"), File: r.PathValue("file"), Content: body.Content, BaseSHA256: body.BaseSHA256}
	res, err := h.Request(r.Context(), nodeID, proto.OpConfigWrite, args, requestTimeout)
	if err != nil {
		adminError(w, err)
		return
	}
	h.configChanged(r.Context(), nodeID, args.Agent, "file:"+args.File)
	relay(w, res)
}
