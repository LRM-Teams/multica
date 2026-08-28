package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/problemevolution"
)

// Exit codes the external evolver uses to convey batch meaning (spec §19.3.4).
const (
	evolverExitOK                  = 0
	evolverExitAllCandidatesFailed = 10
	evolverExitInputRejected       = 20
	evolverExitInfrastructure      = 30
)

// evolverEventBatchSize bounds how many events are posted in one request so a
// chatty evolver cannot build an unbounded in-memory backlog.
const evolverEventBatchSize = 32

// problemEvolutionClaim is the server's response to a successful claim.
type problemEvolutionClaim struct {
	Run struct {
		ID          string `json:"id"`
		WorkspaceID string `json:"workspace_id"`
		Mode        string `json:"mode"`
		Status      string `json:"status"`
	} `json:"run"`
	ClaimToken string                        `json:"claim_token"`
	Input      problemevolution.EvolverInput `json:"input"`
}

// problemEvolutionEventAck is what the server reports back for an event batch.
type problemEvolutionEventAck struct {
	Accepted      int   `json:"accepted"`
	Duplicates    int   `json:"duplicates"`
	Rejected      int   `json:"rejected"`
	LatestSeq     int64 `json:"latest_seq"`
	GraphVersion  int64 `json:"graph_version"`
	StopRequested bool  `json:"stop_requested"`
}

// problemEvolutionEnabled reports whether this machine can execute runs. The
// evolution algorithm is an external program; without a configured path the
// daemon must not advertise the capability.
func (d *Daemon) problemEvolutionEnabled() bool {
	return strings.TrimSpace(d.cfg.ProblemEvolutionEvolverPath) != ""
}

// pollProblemEvolution claims at most one run for the runtime and executes it.
// Called from the heartbeat path; a run already executing for this runtime
// short-circuits so one runtime never drives two batches at once.
func (d *Daemon) pollProblemEvolution(ctx context.Context, rt Runtime) {
	if !d.problemEvolutionEnabled() {
		return
	}
	if !d.beginProblemEvolutionRun(rt.ID) {
		return
	}
	claimed := false
	defer func() {
		if !claimed {
			d.finishProblemEvolutionRun(rt.ID)
		}
	}()
	claim, err := d.client.ClaimProblemEvolutionRun(ctx, rt.ID)
	if err != nil {
		d.logger.Debug("problem evolution claim failed", "runtime_id", rt.ID, "error", err)
		return
	}
	if claim == nil || claim.Run.ID == "" {
		return
	}
	claimed = true
	go func() {
		defer d.finishProblemEvolutionRun(rt.ID)
		d.runProblemEvolutionBatch(context.WithoutCancel(ctx), rt, *claim)
	}()
}

// runProblemEvolutionBatch executes one external evolver invocation for the
// claimed run and reports its outcome.
func (d *Daemon) runProblemEvolutionBatch(ctx context.Context, rt Runtime, claim problemEvolutionClaim) {
	d.logger.Info("problem evolution batch started", "runtime_id", rt.ID, "run_id", claim.Run.ID)
	workdir, err := d.prepareProblemEvolutionWorkdir(claim)
	if err != nil {
		d.failProblemEvolutionRun(ctx, rt, claim, "workdir_preparation_failed")
		return
	}
	batchTimeout := d.cfg.ProblemEvolutionBatchTimeout
	if seconds := claim.Input.Budget.BatchTimeoutSeconds; seconds > 0 {
		requested := time.Duration(seconds) * time.Second
		if requested < batchTimeout {
			batchTimeout = requested
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, batchTimeout)
	defer cancel()

	args := append([]string{}, d.cfg.ProblemEvolutionEvolverArgs...)
	args = append(args, "--input", filepath.Join(workdir, problemevolution.InputFileName), "--workdir", workdir)
	cmd := exec.CommandContext(runCtx, d.cfg.ProblemEvolutionEvolverPath, args...)
	cmd.Dir = workdir
	// The evolver runs with a scrubbed environment: no Multica token, no
	// hidden-answer material, no secret capability. Scoring happens through the
	// evaluator command declared in input.json.
	cmd.Env = problemEvolutionEnv(workdir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.failProblemEvolutionRun(ctx, rt, claim, "evolver_stdout_unavailable")
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		d.failProblemEvolutionRun(ctx, rt, claim, "evolver_stderr_unavailable")
		return
	}
	if err := cmd.Start(); err != nil {
		d.failProblemEvolutionRun(ctx, rt, claim, "evolver_start_failed")
		return
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), problemevolution.MaxEventLineBytes)
		for scanner.Scan() {
			d.logger.Debug("problem evolution evolver stderr", "run_id", claim.Run.ID, "line", problemevolution.TruncateFreeText(scanner.Text()))
		}
	}()

	heartbeatCtx, stopHeartbeat := context.WithCancel(runCtx)
	stopRequested := make(chan struct{})
	go d.problemEvolutionHeartbeatLoop(heartbeatCtx, rt, claim, stopRequested, cmd)

	forwardErr := d.forwardProblemEvolutionEvents(runCtx, rt, claim, stdout, cmd)
	waitErr := cmd.Wait()
	stopHeartbeat()
	<-stderrDone

	if forwardErr != nil {
		d.logger.Warn("problem evolution event forwarding failed", "run_id", claim.Run.ID, "error", forwardErr)
	}
	d.settleProblemEvolutionBatch(ctx, rt, claim, waitErr)
}

// settleProblemEvolutionBatch maps the process outcome onto the run's terminal
// state, following the exit-code contract.
func (d *Daemon) settleProblemEvolutionBatch(ctx context.Context, rt Runtime, claim problemEvolutionClaim, waitErr error) {
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			d.failProblemEvolutionRun(ctx, rt, claim, "evolver_wait_failed")
			return
		}
	}
	switch exitCode {
	case evolverExitOK, evolverExitAllCandidatesFailed:
		version := d.problemEvolutionEvolverVersion(ctx)
		if err := d.client.CompleteProblemEvolutionRun(ctx, rt.ID, claim.Run.ID, claim.ClaimToken, "", version); err != nil {
			d.logger.Warn("problem evolution completion failed", "run_id", claim.Run.ID, "error", err)
		}
	case evolverExitInputRejected:
		d.failProblemEvolutionRun(ctx, rt, claim, "evolver_input_rejected")
	case evolverExitInfrastructure:
		d.failProblemEvolutionRun(ctx, rt, claim, "evolver_infrastructure_error")
	default:
		// A signalled process reports -1 here: that is the stop/timeout path,
		// and the server turns a stopping run into cancelled.
		if exitCode < 0 {
			d.failProblemEvolutionRun(ctx, rt, claim, "evolver_terminated")
			return
		}
		d.failProblemEvolutionRun(ctx, rt, claim, fmt.Sprintf("evolver_exit_%d", exitCode))
	}
}

// forwardProblemEvolutionEvents reads NDJSON from the evolver and posts valid
// events in batches. Unknown event types are dropped locally so the platform
// never stores an event shape it does not understand.
func (d *Daemon) forwardProblemEvolutionEvents(ctx context.Context, rt Runtime, claim problemEvolutionClaim, stdout interface{ Read([]byte) (int, error) }, cmd *exec.Cmd) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), problemevolution.MaxEventLineBytes)
	batch := make([]problemevolution.EvolverEvent, 0, evolverEventBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ack, err := d.client.ReportProblemEvolutionEvents(ctx, rt.ID, claim.Run.ID, claim.ClaimToken, batch)
		batch = batch[:0]
		if err != nil {
			return err
		}
		if ack != nil && ack.StopRequested {
			d.terminateProblemEvolutionProcess(cmd)
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event problemevolution.EvolverEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			d.logger.Debug("problem evolution event dropped: not JSON", "run_id", claim.Run.ID)
			continue
		}
		if err := event.Validate(); err != nil {
			d.logger.Debug("problem evolution event dropped", "run_id", claim.Run.ID, "error", err)
			continue
		}
		if err := event.ValidatePayload(); err != nil {
			d.logger.Debug("problem evolution event payload dropped", "run_id", claim.Run.ID, "error", err)
			continue
		}
		batch = append(batch, event)
		if len(batch) >= evolverEventBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// A line over the cap is reported as a bounded progress note rather
		// than parsed, so an oversized artifact dump cannot stall the run.
		d.logger.Warn("problem evolution stdout scan failed", "run_id", claim.Run.ID, "error", err)
	}
	return flush()
}

// problemEvolutionHeartbeatLoop keeps the claim alive and relays stop intent by
// terminating the child process.
func (d *Daemon) problemEvolutionHeartbeatLoop(ctx context.Context, rt Runtime, claim problemEvolutionClaim, stopRequested chan struct{}, cmd *exec.Cmd) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	stopSignalled := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stop, err := d.client.HeartbeatProblemEvolutionRun(ctx, rt.ID, claim.Run.ID, claim.ClaimToken)
			if err != nil {
				d.logger.Debug("problem evolution heartbeat failed", "run_id", claim.Run.ID, "error", err)
				continue
			}
			if stop && !stopSignalled {
				stopSignalled = true
				close(stopRequested)
				d.terminateProblemEvolutionProcess(cmd)
			}
		}
	}
}

// terminateProblemEvolutionProcess asks the evolver to exit, then kills it
// after the graceful drain window.
func (d *Daemon) terminateProblemEvolutionProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	drain := d.cfg.ProblemEvolutionGracefulDrain
	if drain <= 0 {
		drain = DefaultProblemEvolutionGracefulDrain
	}
	process := cmd.Process
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// Platforms without SIGTERM delivery go straight to the hard kill.
		_ = process.Kill()
		return
	}
	go func() {
		timer := time.NewTimer(drain)
		defer timer.Stop()
		<-timer.C
		_ = process.Kill()
	}()
}

// prepareProblemEvolutionWorkdir creates the per-run directory and writes
// input.json. The evolver may only write inside this directory.
func (d *Daemon) prepareProblemEvolutionWorkdir(claim problemEvolutionClaim) (string, error) {
	root := filepath.Join(os.TempDir(), "multica-problem-evolution", claim.Run.ID)
	if err := os.MkdirAll(filepath.Join(root, problemevolution.DefaultArtifactDir), 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "eval"), 0o700); err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(claim.Input, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, problemevolution.InputFileName), encoded, 0o600); err != nil {
		return "", err
	}
	return root, nil
}

// problemEvolutionEnv builds the scrubbed environment for the child process.
func problemEvolutionEnv(workdir string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + workdir,
		"MULTICA_PROBLEM_EVOLUTION_WORKDIR=" + workdir,
	}
	if lang := os.Getenv("LANG"); lang != "" {
		env = append(env, "LANG="+lang)
	}
	return env
}

// problemEvolutionEvolverVersion asks the external program to identify itself
// so a run can record what produced it. Failure is not fatal; the run is then
// reported as not exactly replayable.
func (d *Daemon) problemEvolutionEvolverVersion(ctx context.Context) string {
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := append([]string{}, d.cfg.ProblemEvolutionEvolverArgs...)
	args = append(args, "--version")
	output, err := exec.CommandContext(versionCtx, d.cfg.ProblemEvolutionEvolverPath, args...).Output()
	if err != nil {
		return ""
	}
	return problemevolution.TruncateFreeText(strings.TrimSpace(string(output)))
}

func (d *Daemon) failProblemEvolutionRun(ctx context.Context, rt Runtime, claim problemEvolutionClaim, reason string) {
	if err := d.client.FailProblemEvolutionRun(ctx, rt.ID, claim.Run.ID, claim.ClaimToken, reason); err != nil {
		d.logger.Warn("problem evolution failure report failed", "runtime_id", rt.ID, "run_id", claim.Run.ID, "reason", reason, "error", err)
	}
}

// splitEvolverArgs parses the extra-arguments env var. Arguments are
// whitespace-separated; an evolver needing quoting should be wrapped in a
// script instead of encoding a shell grammar here.
func splitEvolverArgs(raw string) []string {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func (d *Daemon) beginProblemEvolutionRun(runtimeID string) bool {
	d.problemEvolutionMu.Lock()
	defer d.problemEvolutionMu.Unlock()
	if d.activeProblemEvolutionRuns == nil {
		d.activeProblemEvolutionRuns = make(map[string]struct{})
	}
	if _, active := d.activeProblemEvolutionRuns[runtimeID]; active {
		return false
	}
	d.activeProblemEvolutionRuns[runtimeID] = struct{}{}
	return true
}

func (d *Daemon) finishProblemEvolutionRun(runtimeID string) {
	d.problemEvolutionMu.Lock()
	defer d.problemEvolutionMu.Unlock()
	delete(d.activeProblemEvolutionRuns, runtimeID)
}
