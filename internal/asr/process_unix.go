//go:build linux || darwin

package asr

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// command starts the CLI in its own process group so cancellation kills the
// whole per-request tree (the Python CLI forks workers), then waits briefly
// before the GPU gate is released. Ported from Ilan's process_unix.go.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	return cmd
}
