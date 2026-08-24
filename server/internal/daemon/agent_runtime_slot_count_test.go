package daemon

import (
	"testing"
)

func TestRuntimeSlotCountScalesByCPUAndClamps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   AgentProcessCapInputs
		want int
	}{
		{
			name: "2 cores * K=1 → floor 4",
			in:   AgentProcessCapInputs{NumCPU: 2, PerCPU: 1, Floor: 4, Ceil: 64},
			want: 4,
		},
		{
			name: "8 cores * K=1 → 8",
			in:   AgentProcessCapInputs{NumCPU: 8, PerCPU: 1, Floor: 4, Ceil: 64},
			want: 8,
		},
		{
			name: "32 cores * K=1 → ceil 64? 32",
			in:   AgentProcessCapInputs{NumCPU: 32, PerCPU: 1, Floor: 4, Ceil: 64},
			want: 32,
		},
		{
			name: "128 cores * K=1 → ceil 64",
			in:   AgentProcessCapInputs{NumCPU: 128, PerCPU: 1, Floor: 4, Ceil: 64},
			want: 64,
		},
		{
			name: "8 cores * K=2 → 16",
			in:   AgentProcessCapInputs{NumCPU: 8, PerCPU: 2, Floor: 4, Ceil: 64},
			want: 16,
		},
		{
			name: "absolute override wins",
			in: func() AgentProcessCapInputs {
				n := 7
				return AgentProcessCapInputs{NumCPU: 32, PerCPU: 1, Floor: 4, Ceil: 64, Absolute: &n}
			}(),
			want: 7,
		},
		{
			name: "absolute 0 = unlimited",
			in: func() AgentProcessCapInputs {
				n := 0
				return AgentProcessCapInputs{NumCPU: 8, Absolute: &n}
			}(),
			want: 0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveMaxAgentProcesses(tc.in); got != tc.want {
				t.Fatalf("resolveMaxAgentProcesses(%+v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoadAgentProcessCapInputsEnvOverride(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"MULTICA_MAX_AGENT_PROCESSES": "11",
	}
	in, err := loadAgentProcessCapInputs(func(k string) string { return env[k] }, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveMaxAgentProcesses(in); got != 11 {
		t.Fatalf("got %d, want 11", got)
	}
}

func TestResolveMaxAgentProcessesFromEnvDefaultsToUnlimited(t *testing.T) {
	t.Parallel()
	got, err := resolveMaxAgentProcessesFromEnv(func(string) string { return "" })
	if err != nil || got != 0 {
		t.Fatalf("default max Agent processes = %d, %v; want unlimited", got, err)
	}
}

func TestLoadAgentProcessCapInputsPerCPU(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"MULTICA_MAX_AGENT_PROCESSES_PER_CPU": "3",
	}
	in, err := loadAgentProcessCapInputs(func(k string) string { return env[k] }, 4)
	if err != nil {
		t.Fatal(err)
	}
	// 4*3=12
	if got := resolveMaxAgentProcesses(in); got != 12 {
		t.Fatalf("got %d, want 12", got)
	}
}

func TestLoadAgentProcessCapInputsRejectsBadValues(t *testing.T) {
	t.Parallel()
	if _, err := loadAgentProcessCapInputs(func(string) string { return "nope" }, 4); err == nil {
		// getenv returns nope for every key including MULTICA_MAX_AGENT_PROCESSES
		t.Fatal("expected error for non-integer absolute")
	}
	if _, err := loadAgentProcessCapInputs(func(k string) string {
		if k == "MULTICA_MAX_AGENT_PROCESSES_PER_CPU" {
			return "0"
		}
		return ""
	}, 4); err == nil {
		t.Fatal("expected error for non-positive per-cpu")
	}
}
