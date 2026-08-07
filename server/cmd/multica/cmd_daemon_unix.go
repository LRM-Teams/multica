//go:build !windows

package main

import (
	"context"
	"os/signal"
	"syscall"
)

// notifyShutdownContext returns a context cancelled on the interrupts the
// resident process should treat as shutdown signals on this platform.
func notifyShutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
}
