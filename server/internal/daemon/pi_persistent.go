package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

var ErrPiPersistentSessionBusy = errors.New("Pi persistent session busy")

// piPersistentIdentity includes every task-birth input that can affect native
// Pi behaviour. A changed value must create a new child, never reuse an older
// chat context under a misleading configuration.
type piPersistentIdentity struct {
	AgentID, RuntimeID, ChatSessionID, Executable, Model, Thinking, WorkDir, SystemPrompt, MCP string
	CustomArgs                                                                                 []string
	Environment                                                                                map[string]string
}

func (i piPersistentIdentity) key() string {
	type canonical struct {
		AgentID, RuntimeID, ChatSessionID, Executable, Model, Thinking, WorkDir, SystemPrompt, MCP string
		CustomArgs                                                                                 []string
		Environment                                                                                [][2]string
	}
	keys := make([]string, 0, len(i.Environment))
	for key := range i.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([][2]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, [2]string{key, i.Environment[key]})
	}
	payload, _ := json.Marshal(canonical{
		AgentID: i.AgentID, RuntimeID: i.RuntimeID, ChatSessionID: i.ChatSessionID, Executable: i.Executable,
		Model: i.Model, Thinking: i.Thinking, WorkDir: i.WorkDir, SystemPrompt: i.SystemPrompt, MCP: i.MCP,
		CustomArgs: append([]string(nil), i.CustomArgs...), Environment: env,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

type piPersistentSession struct {
	key       string
	identity  piPersistentIdentity
	running   bool
	idleSince time.Time
	backend   agent.PiRPCBackend
}

type piPersistentPool struct {
	mu       sync.Mutex
	sessions map[string]*piPersistentSession
}

func newPiPersistentPool() *piPersistentPool {
	return &piPersistentPool{sessions: make(map[string]*piPersistentSession)}
}

func (p *piPersistentPool) acquire(identity piPersistentIdentity, now time.Time) (*piPersistentLease, error) {
	key := identity.key()
	p.mu.Lock()
	defer p.mu.Unlock()
	if session := p.sessions[key]; session != nil {
		if session.running {
			return nil, ErrPiPersistentSessionBusy
		}
		session.running = true
		return &piPersistentLease{pool: p, session: session}, nil
	}
	session := &piPersistentSession{key: key, identity: identity, running: true, idleSince: now}
	p.sessions[key] = session
	return &piPersistentLease{pool: p, session: session}, nil
}

type piPersistentLease struct {
	pool    *piPersistentPool
	session *piPersistentSession
	once    sync.Once
}

func (l *piPersistentLease) release(healthy bool, now time.Time) {
	l.once.Do(func() {
		l.pool.mu.Lock()
		defer l.pool.mu.Unlock()
		if l.pool.sessions[l.session.key] != l.session {
			return
		}
		if !healthy {
			delete(l.pool.sessions, l.session.key)
			return
		}
		l.session.running = false
		l.session.idleSince = now
	})
}

func (p *piPersistentPool) evictIdle(before time.Time) {
	p.mu.Lock()
	backends := make([]agent.PiRPCBackend, 0)
	for key, session := range p.sessions {
		if !session.running && session.idleSince.Before(before) {
			delete(p.sessions, key)
			if session.backend != nil {
				backends = append(backends, session.backend)
			}
		}
	}
	p.mu.Unlock()
	for _, backend := range backends {
		backend.Close()
	}
}

func (p *piPersistentPool) closeAll() {
	p.mu.Lock()
	backends := make([]agent.PiRPCBackend, 0, len(p.sessions))
	for _, session := range p.sessions {
		if session.backend != nil {
			backends = append(backends, session.backend)
		}
	}
	p.sessions = make(map[string]*piPersistentSession)
	p.mu.Unlock()
	for _, backend := range backends {
		backend.Close()
	}
}

func (p *piPersistentPool) forceTerminateAll() error {
	p.mu.Lock()
	backends := make([]agent.PiRPCBackend, 0, len(p.sessions))
	for _, session := range p.sessions {
		if session.running && session.backend != nil {
			backends = append(backends, session.backend)
		}
	}
	p.mu.Unlock()
	for _, backend := range backends {
		killable, ok := backend.(agent.ResidentRuntimeForceKillable)
		if !ok {
			return ErrPiPersistentSessionBusy
		}
		if err := killable.ForceKill(); err != nil {
			return err
		}
	}
	return nil
}

func (p *piPersistentPool) evictChat(agentID, runtimeID, chatSessionID string) int {
	p.mu.Lock()
	backends := make([]agent.PiRPCBackend, 0)
	removed := 0
	for key, session := range p.sessions {
		identity := session.identity
		if identity.AgentID != agentID || identity.RuntimeID != runtimeID || identity.ChatSessionID != chatSessionID {
			continue
		}
		delete(p.sessions, key)
		if session.backend != nil {
			backends = append(backends, session.backend)
		}
		removed++
	}
	p.mu.Unlock()
	for _, backend := range backends {
		backend.Close()
	}
	return removed
}

func usesPersistentPiChatRuntime(provider string, task Task) bool {
	profile, err := taskExecutionProfile(task)
	return err == nil && profile == executionProfileFull && provider == "pi" && task.ChatSessionID != ""
}

func (d *Daemon) acquirePiChatRPCBackend(identity piPersistentIdentity, cfg agent.Config) (agent.Backend, func(bool), error) {
	if d.cfg.PiPersistentIdleTTL > 0 {
		d.piPersistentRuntimes.evictIdle(time.Now().Add(-d.cfg.PiPersistentIdleTTL))
	}
	lease, err := d.piPersistentRuntimes.acquire(identity, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if lease.session.backend == nil {
		lease.session.backend = agent.NewPiRPCBackend(cfg)
	}
	backend := lease.session.backend
	return backend, func(healthy bool) {
		if !healthy {
			lease.session.backend.Close()
		}
		lease.release(healthy, time.Now())
	}, nil
}

func newCanonicalPiResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewPiRPCBackend(cfg)
	return backend, backend.Close, nil
}
