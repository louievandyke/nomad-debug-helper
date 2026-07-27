//go:build !windows

package web

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup makes cmd the leader of a new process group and wires
// ctx cancellation to kill the whole group, not just the direct child.
// Needed because Grafana spawns its own plugin backend processes: verified
// live that killing only the `grafana server` process left
// gpx_infinity_darwin_arm64 (and Grafana's other bundled plugin backends)
// running as orphans after shutdown.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
