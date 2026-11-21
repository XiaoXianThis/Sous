//go:build !cgo || ((!darwin || !arm64) && (!linux || (!amd64 && !arm64) || !sous_linux_worker))

package cloudworker

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("此构建没有 Microsandbox 云执行后端；不会回退到实体机执行")

func PrepareImage(context.Context, string) error { return errUnsupported }

func Run(context.Context, *Client, Options) error {
	return errUnsupported
}
