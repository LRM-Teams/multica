//go:build linux

package computer

import (
	"os/exec"
	"syscall"
)

func configureChildParentDeath(cmd *exec.Cmd) {
	if cmd != nil && cmd.SysProcAttr != nil {
		cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
	}
}
