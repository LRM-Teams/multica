package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/multica-ai/multica/server/internal/memorysignal"
)

// frictionTrackerForTask returns the per-task friction tracker, creating it on
// first use. The task drain loop is the only writer, so the tracker itself
// needs no internal locking.
func (d *Daemon) frictionTrackerForTask(taskID string) *memorysignal.FrictionTracker {
	taskID = strings.TrimSpace(taskID)
	if d == nil || taskID == "" {
		return nil
	}
	value, _ := d.taskFriction.LoadOrStore(taskID, memorysignal.NewFrictionTracker())
	return value.(*memorysignal.FrictionTracker)
}

// takeTaskFrictionVector removes and returns the task's friction vector.
// Taking (not peeking) keeps the map from accumulating finished tasks.
func (d *Daemon) takeTaskFrictionVector(taskID string) memorysignal.FrictionVector {
	taskID = strings.TrimSpace(taskID)
	if d == nil || taskID == "" {
		return memorysignal.FrictionVector{}
	}
	value, ok := d.taskFriction.LoadAndDelete(taskID)
	if !ok {
		return memorysignal.FrictionVector{}
	}
	return value.(*memorysignal.FrictionTracker).Vector()
}

// frictionToolInputHash builds a stable identity for one tool call's input.
// encoding/json sorts map keys, so identical inputs hash identically.
func frictionToolInputHash(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}
