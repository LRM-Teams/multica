package daemon

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// Agent process cap (#35): bound how many distinct agents may hold a live
// resident provider process on this daemon. Does NOT limit task concurrency
// (per-agent wake serialization already does that). Formula is CPU-based so
// a 2-core laptop and a 32-core host get different defaults without hardcoding 20.
const (
	// DefaultMaxAgentProcessesPerCPU is K in cap = NumCPU * K.
	DefaultMaxAgentProcessesPerCPU = 1
	// DefaultMaxAgentProcessesFloor is the minimum cap after scaling.
	DefaultMaxAgentProcessesFloor = 4
	// DefaultMaxAgentProcessesCeil is the maximum cap after scaling.
	DefaultMaxAgentProcessesCeil = 64
)

// AgentProcessCapInputs is the resolved input vector for resolveMaxAgentProcesses.
type AgentProcessCapInputs struct {
	NumCPU int
	PerCPU int // K; <=0 → DefaultMaxAgentProcessesPerCPU
	Floor  int // <=0 → DefaultMaxAgentProcessesFloor
	Ceil   int // <=0 → DefaultMaxAgentProcessesCeil
	// Absolute, when non-nil, replaces the scaled value entirely.
	// *0 means unlimited (pool treats max<=0 as unlimited).
	Absolute *int
}

// resolveMaxAgentProcesses returns the daemon-wide live-resident-agent process
// ceiling.
//
//	cap = clamp(NumCPU * K, FLOOR, CEIL)
//	if Absolute != nil: cap = *Absolute  (0 = unlimited)
//
// A non-positive result is treated as unlimited by the pool (maxAgentProcesses<=0).
func resolveMaxAgentProcesses(in AgentProcessCapInputs) int {
	if in.Absolute != nil {
		if *in.Absolute < 0 {
			return 0
		}
		return *in.Absolute
	}
	numCPU := in.NumCPU
	if numCPU <= 0 {
		numCPU = runtime.NumCPU()
	}
	if numCPU <= 0 {
		numCPU = 1
	}
	k := in.PerCPU
	if k <= 0 {
		k = DefaultMaxAgentProcessesPerCPU
	}
	floor := in.Floor
	if floor <= 0 {
		floor = DefaultMaxAgentProcessesFloor
	}
	ceil := in.Ceil
	if ceil <= 0 {
		ceil = DefaultMaxAgentProcessesCeil
	}
	if floor > ceil {
		// Defensive: misconfigured pair — prefer the more restrictive bound.
		floor, ceil = ceil, floor
	}
	cap := numCPU * k
	if cap < floor {
		cap = floor
	}
	if cap > ceil {
		cap = ceil
	}
	return cap
}

// loadAgentProcessCapInputs reads MULTICA_MAX_AGENT_PROCESSES (absolute) and
// MULTICA_MAX_AGENT_PROCESSES_PER_CPU (K) from the environment. Floor/Ceil stay
// at compile defaults for v1.
func loadAgentProcessCapInputs(getenv func(string) string, numCPU int) (AgentProcessCapInputs, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	in := AgentProcessCapInputs{
		NumCPU: numCPU,
		PerCPU: DefaultMaxAgentProcessesPerCPU,
		Floor:  DefaultMaxAgentProcessesFloor,
		Ceil:   DefaultMaxAgentProcessesCeil,
	}
	if raw := strings.TrimSpace(getenv("MULTICA_MAX_AGENT_PROCESSES")); raw != "" {
		n, err := parseAgentProcessCapEnvInt(raw, "MULTICA_MAX_AGENT_PROCESSES", 0, "non-negative")
		if err != nil {
			return AgentProcessCapInputs{}, err
		}
		in.Absolute = &n
	}
	if raw := strings.TrimSpace(getenv("MULTICA_MAX_AGENT_PROCESSES_PER_CPU")); raw != "" {
		n, err := parseAgentProcessCapEnvInt(raw, "MULTICA_MAX_AGENT_PROCESSES_PER_CPU", 1, "positive")
		if err != nil {
			return AgentProcessCapInputs{}, err
		}
		in.PerCPU = n
	}
	return in, nil
}

// parseAgentProcessCapEnvInt centralizes the two environment integer contracts
// used by loadAgentProcessCapInputs. Keeping the bound check in one place makes
// future cap knobs use the same parsing and error semantics.
func parseAgentProcessCapEnvInt(raw, name string, minimum int, requirement string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < minimum {
		return 0, fmt.Errorf("%s: invalid %s integer %q", name, requirement, raw)
	}
	return n, nil
}

// resolveMaxAgentProcessesFromEnv is the production entry. Resident Agent
// processes are unlimited unless an operator explicitly sets the absolute
// safety valve. CPU-scaled defaults are intentionally not applied here: they
// are not Raft's short-lived concurrent-start scheduler.
func resolveMaxAgentProcessesFromEnv(getenv func(string) string) (int, error) {
	in, err := loadAgentProcessCapInputs(getenv, runtime.NumCPU())
	if err != nil {
		return 0, err
	}
	if in.Absolute == nil {
		return 0, nil
	}
	return resolveMaxAgentProcesses(in), nil
}
