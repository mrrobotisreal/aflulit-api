//go:build !(linux || darwin)

package asr

import (
	"context"
	"os/exec"
	"time"
)

// command on other platforms: plain CommandContext (kills the direct child
// only). The service deploys on Linux; this file only keeps `go vet ./...`
// honest everywhere.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}
