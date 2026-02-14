//go:build !linux

package reversebin

import "os/exec"

func configureDetectorProcAttrs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
}

func configureBackendProcAttrs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
}
