//go:build !windows && !linux

package computer

import "os/exec"

func configureChildParentDeath(*exec.Cmd) {}
