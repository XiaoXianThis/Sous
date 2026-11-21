// Package cloudwork is the hosted workspace's authorized execution plane. It
// cannot address a personal Node, host directory, or the legacy run protocol.
package cloudwork

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/localrun"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	MaxBytes  = 1 << 20
	MaxFiles  = 64
	MaxScript = 8192
	Workload  = "microsandbox-shell-v1"
)

var (
	ErrBadInput = errors.New("云执行材料或授权无效")
	ErrMissing  = errors.New("找不到云执行或任务")
	ErrConflict = errors.New("任务、授权或运行状态已改变，请刷新后重试")
	ErrCapacity = errors.New("云执行队列或工作区存储已达预览上限")
)

type Bundle map[string][]byte

func (files Bundle) Validate(empty bool) error {
	if len(files) > MaxFiles || !empty && len(files) == 0 {
		return ErrBadInput
	}
	fold := cases.Fold()
	names := make(map[string]bool, len(files))
	dirs := map[string]string{}
	bytes := 0
	for p, b := range files {
		if !localrun.PortablePath(p) {
			return ErrBadInput
		}
		bytes += len(b)
		if bytes > MaxBytes {
			return ErrBadInput
		}
		key := fold.String(norm.NFC.String(p))
		if names[key] {
			return ErrBadInput
		}
		names[key] = true
		original := p
		for {
			i := strings.LastIndexByte(original, '/')
			if i < 0 {
				break
			}
			original = original[:i]
			folded := fold.String(norm.NFC.String(original))
			if d, ok := dirs[folded]; ok && d != original {
				return ErrBadInput
			}
			dirs[folded] = original
		}
	}
	for d := range dirs {
		if names[d] {
			return ErrBadInput
		}
	}
	return nil
}

func ValidScript(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= MaxScript && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func Hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

// Job is the browser view. Contents and authorizations are not polled.
type Job struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	BaseJobID string `json:"base_job_id,omitempty"`
	Files     int    `json:"files"`
	ExitCode  int    `json:"exit_code"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type Authorize struct {
	RequestID string `json:"request_id"`
	Revision  int64  `json:"task_revision"`
	Script    string `json:"script"`
	Files     Bundle `json:"files,omitempty"`
	BaseJobID string `json:"base_job_id,omitempty"`
}

type Claim struct {
	JobID    string `json:"job_id"`
	Workload string `json:"workload"`
	Lease    string `json:"lease"`
	Script   string `json:"script"`
	Files    Bundle `json:"files"`
}

type Complete struct {
	JobID    string `json:"job_id"`
	Lease    string `json:"lease"`
	Outcome  string `json:"outcome"`
	ExitCode int    `json:"exit_code"`
	Files    Bundle `json:"files,omitempty"`
}

// Validate checks the same durable completion envelope on the worker before
// recovery, and on the Hub before a receipt can change the queue.
func (c Complete) Validate() error {
	if !ValidToken(c.Lease) || c.JobID == "" || len(c.JobID) > 64 {
		return ErrBadInput
	}
	switch c.Outcome {
	case "succeeded", "failed":
		if c.ExitCode < 0 || c.ExitCode > 255 || (c.Outcome == "succeeded") != (c.ExitCode == 0) {
			return ErrBadInput
		}
	case "cancelled", "interrupted":
		if len(c.Files) != 0 || c.ExitCode != -1 {
			return ErrBadInput
		}
	default:
		return ErrBadInput
	}
	if err := c.Files.Validate(true); err != nil {
		return err
	}
	return nil
}

func ValidToken(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}
