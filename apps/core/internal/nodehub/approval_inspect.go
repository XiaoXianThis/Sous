package nodehub

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// ApprovalInspect binds the request to the stored approval, never model args.
func (h *Hub) ApprovalInspect(ctx context.Context, id, tool string, args json.RawMessage) (any, error) {
	a, err := h.log.Store.Approval(ctx, id)
	if err != nil {
		return nil, err
	}
	r, err := h.log.Store.Run(ctx, a.RunID)
	if err != nil {
		return nil, err
	}
	if a.Status != "pending" || r.Status != "running" || r.Paused || !h.Online(r.NodeID) {
		return nil, fmt.Errorf("approval/run no longer active")
	}
	if a.Facts == nil || a.Facts.Version != 1 {
		return nil, fmt.Errorf("node has no approval inspection capability")
	}
	switch tool {
	case "inspect":
		var v struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &v); err != nil {
			return nil, err
		}
		return h.Look(ctx, r.NodeID, proto.LookArgs{Command: v.Command, Dir: r.Workdir, RunID: r.ID, ApprovalID: id})
	case "read_file":
		var v proto.ApprovalReadArgs
		if err := json.Unmarshal(args, &v); err != nil {
			return nil, err
		}
		v.RunID = r.ID
		v.ApprovalID = id
		raw, err := h.Request(ctx, r.NodeID, proto.OpApprovalRead, v, 20*time.Second)
		if err != nil {
			return nil, err
		}
		var res proto.ApprovalReadResult
		err = json.Unmarshal(raw, &res)
		return res, err
	}
	return nil, fmt.Errorf("unknown approval tool")
}
