package agent

import "os"

// processLiveness captures the provider child started for a Session. Keeping
// the OS probe inside pkg/agent lets the daemon reason about runtime liveness
// without depending on exec.Cmd or provider-specific implementations.
func processLiveness(process *os.Process) func() bool {
	return func() bool {
		return processAlive(process)
	}
}
