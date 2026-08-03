package researchrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxContractJSONBytes = 16 << 10

type runConfigPatch struct {
	MaxTasks              *int     `json:"max_tasks"`
	MaxParallelTasks      *int     `json:"max_parallel_tasks"`
	MaxAttemptsPerTask    *int     `json:"max_attempts_per_task"`
	MaxSnapshotBytes      *int     `json:"max_snapshot_bytes"`
	MaxResultBytes        *int     `json:"max_result_bytes"`
	MaxRunSeconds         *int     `json:"max_run_seconds"`
	TaskTimeoutSeconds    *int     `json:"task_timeout_seconds"`
	StaleAfterSeconds     *int     `json:"stale_after_seconds"`
	MarginalGainThreshold *float64 `json:"marginal_gain_threshold"`
	MarginalGainRounds    *int     `json:"marginal_gain_rounds"`
}

func resolveRunConfig(current RunConfig, raw json.RawMessage) (RunConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return current, validateRunConfig(current)
	}
	if len(raw) > maxContractJSONBytes {
		return RunConfig{}, fmt.Errorf("%w: run_limits exceeds %d bytes", ErrInvalidContract, maxContractJSONBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var patch runConfigPatch
	if err := decoder.Decode(&patch); err != nil {
		return RunConfig{}, fmt.Errorf("%w: invalid run_limits: %v", ErrInvalidContract, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RunConfig{}, fmt.Errorf("%w: run_limits must contain exactly one JSON object", ErrInvalidContract)
	}
	if patch.MaxTasks != nil {
		current.MaxTasks = *patch.MaxTasks
	}
	if patch.MaxParallelTasks != nil {
		current.MaxParallelTasks = *patch.MaxParallelTasks
	}
	if patch.MaxAttemptsPerTask != nil {
		current.MaxAttemptsPerTask = *patch.MaxAttemptsPerTask
	}
	if patch.MaxSnapshotBytes != nil {
		current.MaxSnapshotBytes = *patch.MaxSnapshotBytes
	}
	if patch.MaxResultBytes != nil {
		current.MaxResultBytes = *patch.MaxResultBytes
	}
	if patch.MaxRunSeconds != nil {
		current.MaxRunSeconds = *patch.MaxRunSeconds
	}
	if patch.TaskTimeoutSeconds != nil {
		current.TaskTimeoutSeconds = *patch.TaskTimeoutSeconds
	}
	if patch.StaleAfterSeconds != nil {
		current.StaleAfterSeconds = *patch.StaleAfterSeconds
	}
	if patch.MarginalGainThreshold != nil {
		current.MarginalGainThreshold = *patch.MarginalGainThreshold
	}
	if patch.MarginalGainRounds != nil {
		current.MarginalGainRounds = *patch.MarginalGainRounds
	}
	return current, validateRunConfig(current)
}

func resolveContractObject(current, replacement json.RawMessage, field string) (json.RawMessage, error) {
	resolved := replacement
	if len(bytes.TrimSpace(resolved)) == 0 {
		resolved = current
	}
	if len(resolved) > maxContractJSONBytes {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidContract, field, maxContractJSONBytes)
	}
	var object map[string]any
	if json.Unmarshal(resolved, &object) != nil || object == nil {
		return nil, fmt.Errorf("%w: %s must be a JSON object", ErrInvalidContract, field)
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w: encode %s: %v", ErrInvalidContract, field, err)
	}
	return canonical, nil
}

func validateRunConfig(config RunConfig) error {
	switch {
	case config.MaxTasks < 1 || config.MaxTasks > 2000:
		return fmt.Errorf("%w: max_tasks must be in [1,2000]", ErrInvalidContract)
	case config.MaxParallelTasks < 1 || config.MaxParallelTasks > 100 || config.MaxParallelTasks > config.MaxTasks:
		return fmt.Errorf("%w: max_parallel_tasks must be in [1,100] and no greater than max_tasks", ErrInvalidContract)
	case config.MaxAttemptsPerTask < 1 || config.MaxAttemptsPerTask > 10:
		return fmt.Errorf("%w: max_attempts_per_task must be in [1,10]", ErrInvalidContract)
	case config.MaxSnapshotBytes < 1024 || config.MaxSnapshotBytes > 1<<20:
		return fmt.Errorf("%w: max_snapshot_bytes must be in [1024,1048576]", ErrInvalidContract)
	case config.MaxResultBytes < 16<<10 || config.MaxResultBytes > 2<<20:
		return fmt.Errorf("%w: max_result_bytes must be in [16384,2097152]", ErrInvalidContract)
	case config.MaxSnapshotBytes > config.MaxResultBytes:
		return fmt.Errorf("%w: max_snapshot_bytes cannot exceed max_result_bytes", ErrInvalidContract)
	case config.MaxRunSeconds < 60 || config.MaxRunSeconds > 7*24*60*60:
		return fmt.Errorf("%w: max_run_seconds must be in [60,604800]", ErrInvalidContract)
	case config.TaskTimeoutSeconds < 30 || config.TaskTimeoutSeconds > 86400 || config.TaskTimeoutSeconds > config.MaxRunSeconds:
		return fmt.Errorf("%w: task_timeout_seconds must be in [30,86400] and no greater than max_run_seconds", ErrInvalidContract)
	case config.StaleAfterSeconds < 30 || config.StaleAfterSeconds > 86400:
		return fmt.Errorf("%w: stale_after_seconds must be in [30,86400]", ErrInvalidContract)
	case config.MarginalGainThreshold <= 0 || config.MarginalGainThreshold > 1:
		return fmt.Errorf("%w: marginal_gain_threshold must be in (0,1]", ErrInvalidContract)
	case config.MarginalGainRounds < 1 || config.MarginalGainRounds > 20:
		return fmt.Errorf("%w: marginal_gain_rounds must be in [1,20]", ErrInvalidContract)
	default:
		return nil
	}
}
