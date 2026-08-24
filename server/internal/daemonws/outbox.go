package daemonws

import "sync"

const (
	clientOutboxMaxPriorityFrames = 64
	clientOutboxMaxPriorityBytes  = 1 << 20
	clientOutboxMaxFrames         = 4096
	clientOutboxMaxBytes          = 16 << 20
)

// clientOutbox is a connection-local serialization buffer. Its limits are a
// memory-safety budget, not an Agent-count contract: desired Agent state lives
// outside the socket and is reconciled again after reconnect.
//
// A full outbox rejects only the new frame. It never closes an otherwise
// healthy WebSocket merely because producers briefly outran its single writer.
type clientOutbox struct {
	mu            sync.Mutex
	priority      [][]byte
	normal        [][]byte
	priorityBytes int
	normalBytes   int
	wake          chan struct{}
	closed        bool
}

func newClientOutbox() *clientOutbox {
	return &clientOutbox{wake: make(chan struct{}, 1)}
}

func (outbox *clientOutbox) enqueue(frame []byte, priority bool) bool {
	if outbox == nil || len(frame) == 0 {
		return false
	}
	outbox.mu.Lock()
	if outbox.closed {
		outbox.mu.Unlock()
		return false
	}
	if priority {
		if len(outbox.priority) >= clientOutboxMaxPriorityFrames || outbox.priorityBytes+len(frame) > clientOutboxMaxPriorityBytes {
			outbox.mu.Unlock()
			return false
		}
		outbox.priority = append(outbox.priority, frame)
		outbox.priorityBytes += len(frame)
	} else {
		if len(outbox.normal) >= clientOutboxMaxFrames || outbox.normalBytes+len(frame) > clientOutboxMaxBytes {
			outbox.mu.Unlock()
			return false
		}
		outbox.normal = append(outbox.normal, frame)
		outbox.normalBytes += len(frame)
	}
	outbox.mu.Unlock()
	outbox.signal()
	return true
}

func (outbox *clientOutbox) pop() ([]byte, bool) {
	if outbox == nil {
		return nil, false
	}
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	if len(outbox.priority) > 0 {
		frame := outbox.priority[0]
		outbox.priority[0] = nil
		outbox.priority = outbox.priority[1:]
		outbox.priorityBytes -= len(frame)
		return frame, true
	}
	if len(outbox.normal) == 0 {
		return nil, false
	}
	frame := outbox.normal[0]
	outbox.normal[0] = nil
	outbox.normal = outbox.normal[1:]
	outbox.normalBytes -= len(frame)
	return frame, true
}

func (outbox *clientOutbox) close() {
	if outbox == nil {
		return
	}
	outbox.mu.Lock()
	outbox.closed = true
	outbox.priority = nil
	outbox.normal = nil
	outbox.priorityBytes = 0
	outbox.normalBytes = 0
	outbox.mu.Unlock()
	outbox.signal()
}

func (outbox *clientOutbox) signal() {
	select {
	case outbox.wake <- struct{}{}:
	default:
	}
}
