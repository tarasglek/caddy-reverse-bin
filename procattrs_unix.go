//go:build !linux && !windows

package reversebin

import (
	"os/exec"
	"syscall"
)

func configureDetectorProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func configureBackendProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
