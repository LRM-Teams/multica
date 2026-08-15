package computer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
)

// BindingChildRunFunc is the Computer-owned in-process DaemonCore. cmd wires
// daemon.RunBindingChild here so computer does not import daemon.
type BindingChildRunFunc func(context.Context, BindingChildBootstrap, func(BindingChildReady) error) error

// InProcessBinding is one Workspace Binding running in the Computer process.
type InProcessBinding struct {
	cancel    context.CancelFunc
	readyDone chan struct{}
	done      chan error

	mu       sync.Mutex
	ready    BindingChildReady
	readyErr error
	exitErr  error
}

func StartInProcessBinding(bootstrap BindingChildBootstrap, run BindingChildRunFunc) (*InProcessBinding, error) {
	bootstrap, err := bootstrap.validated()
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, errors.New("in-process Binding runner is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	child := &InProcessBinding{cancel: cancel, readyDone: make(chan struct{}), done: make(chan error, 1)}
	go func() {
		err := run(ctx, bootstrap, func(ready BindingChildReady) error {
			if ready.PID == 0 {
				ready.PID = os.Getpid()
			}
			child.mu.Lock()
			child.ready = ready
			child.mu.Unlock()
			select {
			case <-child.readyDone:
			default:
				close(child.readyDone)
			}
			return nil
		})
		child.mu.Lock()
		child.exitErr = err
		if err != nil && child.ready.WorkspaceID == "" {
			child.readyErr = err
		}
		child.mu.Unlock()
		select {
		case <-child.readyDone:
		default:
			close(child.readyDone)
		}
		child.done <- err
	}()
	return child, nil
}

func (child *InProcessBinding) PID() int { return os.Getpid() }

func (child *InProcessBinding) Wait() RunnerExitClass {
	if child == nil {
		return RunnerExitCrash
	}
	err := <-child.done
	if err == nil || errors.Is(err, context.Canceled) {
		return RunnerExitGraceful
	}
	return RunnerExitCrash
}

func (child *InProcessBinding) Stop() error {
	if child == nil {
		return nil
	}
	child.cancel()
	return nil
}

func (child *InProcessBinding) AwaitReady(ctx context.Context) (BindingChildReady, error) {
	if child == nil {
		return BindingChildReady{}, errors.New("in-process Binding is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return BindingChildReady{}, fmt.Errorf("wait for in-process Binding Ready: %w", ctx.Err())
	case <-child.readyDone:
	}
	child.mu.Lock()
	defer child.mu.Unlock()
	if child.readyErr != nil {
		return BindingChildReady{}, child.readyErr
	}
	if child.ready.WorkspaceID == "" {
		return BindingChildReady{}, errors.New("in-process Binding did not publish Ready")
	}
	return child.ready, nil
}
