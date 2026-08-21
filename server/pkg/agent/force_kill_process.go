package agent

import (
	"errors"
	"os"
)

// forceKillProcess sends SIGKILL (or platform equivalent) and treats an
// already-reaped process as success. ForceKill closes stdin before Kill on
// several backends; that alone can make the child exit, letting Execute()'s
// goroutine Wait() before Kill runs. os.Process.Kill then returns
// os.ErrProcessDone ("process already finished") even though the desired
// outcome — process dead — already happened. beginResidentTermination surfaces
// ForceKill's error to callers, so treating ErrProcessDone as failure would
// make a successful interrupt look like a failed restart (#105 path).
func forceKillProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	err := process.Kill()
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
