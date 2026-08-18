//go:build windows

package computer

import "os/exec"

func configureChildParentDeath(*exec.Cmd) {}
