//go:build windows

package web

import "os/exec"

// configureProcessGroup is a no-op on Windows: process-group kill semantics
// differ enough from POSIX (job objects vs. process groups) that this needs
// its own implementation if Windows support becomes a real target. Cancelling
// ctx still kills the direct grafana process; any plugin-backend children it
// spawned may be left running, the same limitation vault-debug-helper's own
// "TODO track the PIDs too" already carries unresolved.
func configureProcessGroup(cmd *exec.Cmd) {}
