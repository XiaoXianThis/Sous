package cloudworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
)

type Options struct {
	State, Runtime, MSB, Firmware, Image string
	Once                                 bool
}

type Client struct {
	origin, token, bootstrap string
	generation               int64
	http                     *http.Client
}

var errLease = errors.New("云工作区未接受此租约；保留本机检查点，不会重跑")

func NewClient(origin, token, caPath, socket string, generation int64) (*Client, error) {
	if generation < 0 || ((generation == 0 || token != "") && !cloudwork.ValidToken(token)) {
		return nil, errors.New("需要私有 worker 密钥和有效代次")
	}
	client, err := cloudwork.MachineHTTP(origin, caPath, socket)
	if err != nil {
		return nil, err
	}
	c := &Client{origin: origin, generation: generation, http: client}
	if generation > 0 {
		c.bootstrap = token
	} else {
		c.token = token
	}
	return c, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }

// First try the durable session, so a lost registration reply or a restart
// after the bootstrap expired does not require issuing a fresh identity.
func (c *Client) ready(ctx context.Context) error {
	var reply struct {
		Ready bool `json:"ready"`
	}
	data := map[string]string{"session_sha256": cloudwork.Hash([]byte(c.token))}
	err := c.do(ctx, "ready", data, &reply)
	if errors.Is(err, errLease) && c.bootstrap != "" {
		session := c.token
		c.token = c.bootstrap
		err = c.do(ctx, "ready", data, &reply)
		c.token = session
	}
	if err != nil {
		return err
	}
	if !reply.Ready {
		return errors.New("云工作区没有确认 worker 已就绪")
	}
	c.bootstrap = ""
	return nil
}
func (c *Client) Claim(ctx context.Context) (*cloudwork.Claim, error) {
	var v struct {
		Claim *cloudwork.Claim `json:"claim"`
	}
	err := c.do(ctx, "claim", map[string]string{"workload": cloudwork.Workload}, &v)
	return v.Claim, err
}
func (c *Client) Heartbeat(ctx context.Context, id, lease string) (bool, error) {
	var v struct {
		Stop bool `json:"stop"`
	}
	err := c.do(ctx, "heartbeat", map[string]string{"job_id": id, "lease": lease}, &v)
	return v.Stop, err
}
func (c *Client) Complete(ctx context.Context, result cloudwork.Complete) error {
	var v cloudwork.Job
	if err := c.do(ctx, "complete", result, &v); err != nil {
		return err
	}
	if v.ID != result.JobID || v.Status != result.Outcome || v.Files != len(result.Files) || v.ExitCode != result.ExitCode {
		return errors.New("云工作区回执不匹配，保留检查点")
	}
	return nil
}
func (c *Client) do(ctx context.Context, action string, data, out any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+cloudwork.WorkerPath+action, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+c.token)
	if c.generation > 0 {
		r.Header.Set(cloudwork.GenerationHeader, strconv.FormatInt(c.generation, 10))
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return errors.New("连接云工作区失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 409 || resp.StatusCode == 404 {
		return errLease
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("云工作区 worker 请求失败（HTTP %d）", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return errors.New("云工作区回执无效")
	}
	return nil
}
