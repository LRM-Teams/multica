//go:build windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// sigBreak is a Windows attention (break) signal used as an interrupt.
const sigBreak = syscall.Signal(0x15)

// notifyShutdownContext returns a context cancelled on the interrupts the
// resident process should treat as shutdown signals on this platform.
func notifyShutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, sigBreak)
}
