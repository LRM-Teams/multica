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

// ErrPersistentRuntimeSessionBusy is deliberately returned instead of waiting.
// A claimed server task must never sit behind an in-process session queue:
// server dispatch leases have their own timeout. The poller must acquire a
// usable slot before claim.
var ErrPersistentRuntimeSessionBusy = errors.New("persistent runtime session busy")

// persistentRuntimeIdentity contains every input that may change native runtime
// behaviour. It is intentionally not just agent/runtime: task-birth execution
// config and the effective process environment are isolation boundaries.

type persistentRuntimeIdentity struct {
	AgentID       string
	RuntimeID     string
	ChatSessionID string
	Executable    string
	Model         string
	Thinking      string
	WorkDir       string
	SystemPrompt  string
	MCP           string
	CustomArgs    []string
	Environment   map[string]string
}

func (i persistentRuntimeIdentity) key() string {
	type canonical struct {
		AgentID, RuntimeID, ChatSessionID, Executable, Model, Thinking, WorkDir, SystemPrompt, MCP string
		CustomArgs                                                                                 []string
		Environment                                                                                [][2]string
	}
	envKeys := make([]string, 0, len(i.Environment))
	for k := range i.Environment {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	env := make([][2]string, 0, len(envKeys))
	for _, k := range envKeys {
		env = append(env, [2]string{k, i.Environment[k]})
	}
	payload, _ := json.Marshal(canonical{
		AgentID: i.AgentID, RuntimeID: i.RuntimeID, ChatSessionID: i.ChatSessionID, Executable: i.Executable,
		Model: i.Model, Thinking: i.Thinking, WorkDir: i.WorkDir,
		SystemPrompt: i.SystemPrompt, MCP: i.MCP,
		CustomArgs: append([]string(nil), i.CustomArgs...), Environment: env,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// persistentRuntimeSession is the lifecycle shell around a daemon-owned native
// runtime. The pool deliberately knows no provider protocol.
type persistentRuntimeSession struct {
	key       string
	identity  persistentRuntimeIdentity
	running   bool
	idleSince time.Time
	backend   agent.GrokACPBackend
}

type persistentRuntimePool struct {
	mu       sync.Mutex
	sessions map[string]*persistentRuntimeSession
}

func newPersistentRuntimePool() *persistentRuntimePool {
	return &persistentRuntimePool{sessions: make(map[string]*persistentRuntimeSession)}
}

// acquire returns a lease only when a session is idle or can be created now.
// It never waits, which keeps server claim/dispatch semantics safe.
func (p *persistentRuntimePool) acquire(identity persistentRuntimeIdentity, now time.Time) (*persistentRuntimeLease, error) {
	key := identity.key()
	p.mu.Lock()
	defer p.mu.Unlock()
	if current := p.sessions[key]; current != nil {
		if current.running {
			return nil, ErrPersistentRuntimeSessionBusy
		}
		current.running = true
		return &persistentRuntimeLease{pool: p, session: current}, nil
	}
	s := &persistentRuntimeSession{key: key, identity: identity, running: true, idleSince: now}
	p.sessions[key] = s
	return &persistentRuntimeLease{pool: p, session: s}, nil
}

type persistentRuntimeLease struct {
	pool    *persistentRuntimePool
	session *persistentRuntimeSession
	once    sync.Once
}

// release returns a healthy session to idle. Any runtime/ACP failure removes
// it so the next task starts a fresh native process rather than reusing an
// unknown state.
func (l *persistentRuntimeLease) release(healthy bool, now time.Time) {
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

func (p *persistentRuntimePool) evictIdle(before time.Time) int {
	p.mu.Lock()
	removed := 0
	backends := make([]agent.GrokACPBackend, 0)
	for key, session := range p.sessions {
		if !session.running && session.idleSince.Before(before) {
			delete(p.sessions, key)
			if session.backend != nil {
				backends = append(backends, session.backend)
			}
			removed++
		}
	}
	p.mu.Unlock()
	for _, backend := range backends {
		backend.Close()
	}
	return removed
}

func (p *persistentRuntimePool) closeAll() {
	p.mu.Lock()
	backends := make([]agent.GrokACPBackend, 0, len(p.sessions))
	for _, session := range p.sessions {
		if session.backend != nil {
			backends = append(backends, session.backend)
		}
	}
	p.sessions = make(map[string]*persistentRuntimeSession)
	p.mu.Unlock()
	for _, backend := range backends {
		backend.Close()
	}
}

func (p *persistentRuntimePool) evictChat(agentID, runtimeID, chatSessionID string) int {
	p.mu.Lock()
	backends := make([]agent.GrokACPBackend, 0)
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

func (d *Daemon) closePersistentRuntimes() {
	if d.persistentRuntimes != nil {
		d.persistentRuntimes.closeAll()
	}
}

func (d *Daemon) evictPersistentChatRuntime(task Task) {
	if task.ChatSessionID == "" {
		return
	}
	agentID := resolvedTaskAgentID(task)
	if d.persistentRuntimes != nil {
		d.persistentRuntimes.evictChat(agentID, task.RuntimeID, task.ChatSessionID)
	}
	if d.piPersistentRuntimes != nil {
		d.piPersistentRuntimes.evictChat(agentID, task.RuntimeID, task.ChatSessionID)
	}
}

func usesPersistentGrokChatRuntime(provider string, task Task) bool {
	return provider == "grok" && task.ChatSessionID != ""
}

// acquireGrokChatACPBackend binds the lease invariant to a retained Grok chat
// session. It is intentionally not used for issue tasks, which remain one-shot.
func (d *Daemon) acquireGrokChatACPBackend(identity persistentRuntimeIdentity, cfg agent.Config) (agent.Backend, func(bool), error) {
	if d.cfg.GrokPersistentIdleTTL > 0 {
		d.persistentRuntimes.evictIdle(time.Now().Add(-d.cfg.GrokPersistentIdleTTL))
	}
	lease, err := d.persistentRuntimes.acquire(identity, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if lease.session.backend == nil {
		lease.session.backend = agent.NewGrokACPBackend(cfg)
	}
	backend := lease.session.backend
	return backend, func(healthy bool) {
		if !healthy {
			lease.session.backend.Close()
		}
		lease.release(healthy, time.Now())
	}, nil
}

func newCanonicalGrokResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewGrokACPBackend(cfg)
	return backend, backend.Close, nil
}
