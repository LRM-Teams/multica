// Package turntransport provides the task-scoped credential and metadata
// boundary used by daemon-managed agent CLI invocations.
//
// Provider processes are long-lived and therefore must receive only stable
// agent-scoped environment. The fixed CLI wrapper prepared here resolves the
// current turn at invocation time, after the daemon has atomically published
// its envelope and token file.
package turntransport

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// EnvelopePathEnv is set only by the fixed daemon-managed CLI wrapper.
	// The provider process itself must not receive this variable.
	EnvelopePathEnv = "MULTICA_CURRENT_TURN_ENVELOPE"
	// AttemptPathEnv points the task-scoped CLI at the daemon-owned marker
	// used to distinguish an actual visible-transport attempt from a deliberate
	// no-reply completion. It belongs to the current turn, never the reusable
	// provider process identity.
	AttemptPathEnv = "MULTICA_TRANSPORT_ATTEMPT_PATH"

	envelopeVersion = 1
	envelopeName    = "current-turn.json"
	attemptName     = "transport-attempt"
	maxEnvelopeSize = 64 << 10
)

var turnEnvironmentKeys = map[string]struct{}{
	"MULTICA_TASK_ID":                        {},
	"MULTICA_RUN_ID":                         {},
	"MULTICA_TASK_SLOT":                      {},
	"MULTICA_AGENT_INBOX_EVENT_ID":           {},
	"MULTICA_AGENT_INBOX_DELIVERY_ID":        {},
	"MULTICA_AGENT_INBOX_LEASE_TOKEN":        {},
	"MULTICA_MEMBER_ID":                      {},
	"MULTICA_PROJECT_ID":                     {},
	"MULTICA_CHANNEL_ID":                     {},
	"MULTICA_AUTOPILOT_RUN_ID":               {},
	"MULTICA_AUTOPILOT_ID":                   {},
	"MULTICA_QUICK_CREATE_TASK_ID":           {},
	"MULTICA_QUICK_CREATE_ATTACHMENT_IDS":    {},
	"MULTICA_QUICK_CREATE_SOURCE_CHANNEL_ID": {},
	"MULTICA_QUICK_CREATE_SOURCE_MESSAGE_ID": {},
	AttemptPathEnv:                           {},
}

var stableMulticaEnvironmentKeys = map[string]struct{}{
	"MULTICA_SERVER_URL":   {},
	"MULTICA_DAEMON_PORT":  {},
	"MULTICA_WORKSPACE_ID": {},
	"MULTICA_AGENT_NAME":   {},
	"MULTICA_AGENT_ID":     {},
	"MULTICA_AGENT_ROOT":   {},
}

var transportLocks sync.Map // absolute transport root -> *sync.Mutex

// Transport is an agent-scoped, fixed CLI transport. Its wrapper and current
// envelope paths do not change between turns.
type Transport struct {
	root        string
	wrapperPath string
}

// Binding identifies one published turn. Generation is an unguessable fencing
// value; a late terminal path may unbind only the generation it created.
type Binding struct {
	Root       string
	TurnID     string
	Generation string
	TokenFile  string
}

type envelope struct {
	Version     int               `json:"version"`
	TurnID      string            `json:"turn_id"`
	Generation  string            `json:"generation"`
	TokenFile   string            `json:"token_file"`
	Environment map[string]string `json:"environment"`
}

// Prepare creates or refreshes the fixed wrapper under root. Re-preparing the
// same root keeps the wrapper path stable while allowing a daemon release to
// point it at the newly running multica binary.
func Prepare(root, binaryPath string) (*Transport, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	binaryPath = filepath.Clean(strings.TrimSpace(binaryPath))
	if root == "." || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("transport root must be an absolute path")
	}
	if binaryPath == "." || !filepath.IsAbs(binaryPath) {
		return nil, fmt.Errorf("multica binary path must be an absolute path")
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return nil, fmt.Errorf("create transport bin dir: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("chmod transport root: %w", err)
	}
	if err := os.Chmod(binDir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod transport bin dir: %w", err)
	}

	wrapperPath := filepath.Join(binDir, "multica")
	body := wrapperBody(filepath.Join(root, envelopeName), binaryPath)
	if err := writeFileAtomic(wrapperPath, []byte(body), 0o700); err != nil {
		return nil, fmt.Errorf("write fixed cli wrapper: %w", err)
	}

	return &Transport{
		root:        root,
		wrapperPath: wrapperPath,
	}, nil
}

// Root returns the fixed agent-scoped transport root.
func (t *Transport) Root() string {
	if t == nil {
		return ""
	}
	return t.root
}

// WrapperPath returns the fixed wrapper path prepended to the provider PATH.
func (t *Transport) WrapperPath() string {
	if t == nil {
		return ""
	}
	return t.wrapperPath
}

// CurrentEnvelopePath returns the atomically replaced current-turn locator.
func (t *Transport) CurrentEnvelopePath() string {
	if t == nil {
		return ""
	}
	return filepath.Join(t.root, envelopeName)
}

// Bind writes the token before atomically publishing the turn envelope.
// Rebinding replaces any crash-stale generation and removes its secret files.
func (t *Transport) Bind(turnID, token string, environment map[string]string) (Binding, error) {
	if t == nil || t.root == "" {
		return Binding{}, fmt.Errorf("transport is not prepared")
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return Binding{}, fmt.Errorf("turn_id is required")
	}
	if token == "" {
		return Binding{}, fmt.Errorf("turn token is required")
	}
	turnEnv, err := validateTurnEnvironment(environment)
	if err != nil {
		return Binding{}, err
	}
	if turnEnv["MULTICA_TASK_ID"] != turnID {
		return Binding{}, fmt.Errorf("current-turn MULTICA_TASK_ID must match turn_id")
	}

	unlock := lockRoot(t.root)
	defer unlock()

	generation, err := randomGeneration()
	if err != nil {
		return Binding{}, fmt.Errorf("create binding generation: %w", err)
	}
	generationDir := filepath.Join(t.root, "turns", generation)
	if err := os.MkdirAll(generationDir, 0o700); err != nil {
		return Binding{}, fmt.Errorf("create turn generation dir: %w", err)
	}
	if err := os.Chmod(generationDir, 0o700); err != nil {
		_ = os.RemoveAll(generationDir)
		return Binding{}, fmt.Errorf("chmod turn generation dir: %w", err)
	}

	tokenFile := filepath.Join(generationDir, "token")
	if err := writeFileAtomic(tokenFile, []byte(token), 0o600); err != nil {
		_ = os.RemoveAll(generationDir)
		return Binding{}, fmt.Errorf("write turn token: %w", err)
	}

	current := envelope{
		Version:     envelopeVersion,
		TurnID:      turnID,
		Generation:  generation,
		TokenFile:   tokenFile,
		Environment: turnEnv,
	}
	raw, err := json.Marshal(current)
	if err != nil {
		_ = os.RemoveAll(generationDir)
		return Binding{}, fmt.Errorf("marshal turn envelope: %w", err)
	}
	if err := writeFileAtomic(t.CurrentEnvelopePath(), raw, 0o600); err != nil {
		_ = os.RemoveAll(generationDir)
		return Binding{}, fmt.Errorf("publish turn envelope: %w", err)
	}

	if err := removeOtherGenerations(t.root, generation); err != nil {
		_ = os.Remove(t.CurrentEnvelopePath())
		_ = os.RemoveAll(generationDir)
		return Binding{}, fmt.Errorf("clean superseded turn generations: %w", err)
	}
	return Binding{
		Root:       t.root,
		TurnID:     turnID,
		Generation: generation,
		TokenFile:  tokenFile,
	}, nil
}

// Unbind removes current-turn authority only when binding still owns the
// published generation. It returns false when a newer turn superseded binding.
func Unbind(binding Binding) (bool, error) {
	root := filepath.Clean(strings.TrimSpace(binding.Root))
	if root == "." || !filepath.IsAbs(root) || !validGeneration(binding.Generation) {
		return false, fmt.Errorf("invalid binding")
	}

	unlock := lockRoot(root)
	defer unlock()

	currentPath := filepath.Join(root, envelopeName)
	current, err := readEnvelope(currentPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := removeGeneration(root, binding.Generation); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read current turn envelope: %w", err)
	}
	if current.Generation != binding.Generation {
		if err := removeGeneration(root, binding.Generation); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := os.Remove(currentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove current turn envelope: %w", err)
	}
	if err := removeGeneration(root, binding.Generation); err != nil {
		return false, err
	}
	return true, nil
}

// Recover removes crash-stale current-turn authority and generation secrets
// while preserving the fixed wrapper. It is intended for daemon startup before
// the agent is allowed to claim a new turn.
func Recover(root string) error {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || !filepath.IsAbs(root) {
		return fmt.Errorf("transport root must be an absolute path")
	}
	unlock := lockRoot(root)
	defer unlock()

	if err := os.Remove(filepath.Join(root, envelopeName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale current turn envelope: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "turns")); err != nil {
		return fmt.Errorf("remove stale turn generations: %w", err)
	}
	return nil
}

// SplitEnvironment separates stable provider-process environment from
// current-turn CLI environment. It rejects raw token transport so credentials
// cannot accidentally remain in a reusable process identity.
func SplitEnvironment(environment map[string]string) (stable, currentTurn map[string]string, err error) {
	stable = make(map[string]string, len(environment))
	currentTurn = make(map[string]string)
	for key, value := range environment {
		switch key {
		case "MULTICA_TOKEN", "MULTICA_TOKEN_FILE", EnvelopePathEnv:
			return nil, nil, fmt.Errorf("%s must not be part of provider process environment", key)
		}
		if IsTurnEnvironmentKey(key) {
			currentTurn[key] = value
			continue
		}
		if strings.HasPrefix(key, "MULTICA_") {
			if _, ok := stableMulticaEnvironmentKeys[key]; !ok {
				return nil, nil, fmt.Errorf("unclassified Multica environment key %s", key)
			}
		}
		stable[key] = value
	}
	return stable, currentTurn, nil
}

// IsTurnEnvironmentKey reports whether key belongs in the invocation-time
// current-turn envelope rather than the reusable provider process.
func IsTurnEnvironmentKey(key string) bool {
	_, ok := turnEnvironmentKeys[key]
	return ok
}

// AttemptPath returns the task-scoped marker path under a prepared per-run
// transport directory.
func AttemptPath(root string) string {
	return filepath.Join(root, attemptName)
}

// RecordAttemptFromEnvironment durably records that the agent invoked a
// visible message transport command. The marker is written before the HTTP
// request, so connection failures and server errors remain distinguishable
// from an intentional no-reply completion.
func RecordAttemptFromEnvironment() error {
	path := filepath.Clean(strings.TrimSpace(os.Getenv(AttemptPathEnv)))
	if path == "." || !filepath.IsAbs(path) || filepath.Base(path) != attemptName {
		if strings.TrimSpace(os.Getenv(AttemptPathEnv)) == "" {
			return nil
		}
		return fmt.Errorf("invalid transport attempt path")
	}
	if err := writeFileAtomic(path, []byte("attempted\n"), 0o600); err != nil {
		return fmt.Errorf("record transport attempt: %w", err)
	}
	return nil
}

// ApplyFromEnvironment loads the envelope selected by the fixed wrapper and
// applies only the explicit current-turn allowlist to this CLI process. A
// missing marker is a no-op so normal human CLI execution is unchanged.
func ApplyFromEnvironment() error {
	path := strings.TrimSpace(os.Getenv(EnvelopePathEnv))
	if path == "" {
		return nil
	}
	_ = os.Unsetenv(EnvelopePathEnv)
	if !filepath.IsAbs(path) || filepath.Base(path) != envelopeName {
		return fmt.Errorf("invalid current-turn envelope path")
	}

	current, err := readEnvelope(path)
	if err != nil {
		return fmt.Errorf("load current-turn envelope: %w", err)
	}
	root := filepath.Dir(path)
	expectedToken := filepath.Join(root, "turns", current.Generation, "token")
	if filepath.Clean(current.TokenFile) != expectedToken {
		return fmt.Errorf("current-turn token path is outside its generation")
	}
	if err := requirePrivateRegularFile(current.TokenFile); err != nil {
		return fmt.Errorf("validate current-turn token: %w", err)
	}
	turnEnv, err := validateTurnEnvironment(current.Environment)
	if err != nil {
		return err
	}
	if turnEnv["MULTICA_TASK_ID"] != current.TurnID {
		return fmt.Errorf("current-turn MULTICA_TASK_ID does not match turn_id")
	}

	for key := range turnEnvironmentKeys {
		_ = os.Unsetenv(key)
	}
	_ = os.Unsetenv("MULTICA_TOKEN")
	_ = os.Unsetenv("MULTICA_TOKEN_FILE")
	for key, value := range turnEnv {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set current-turn environment %s: %w", key, err)
		}
	}
	if err := os.Setenv("MULTICA_TOKEN_FILE", current.TokenFile); err != nil {
		return fmt.Errorf("set current-turn token file: %w", err)
	}
	return nil
}

func validateTurnEnvironment(environment map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(environment))
	for key, value := range environment {
		if !IsTurnEnvironmentKey(key) {
			return nil, fmt.Errorf("current-turn environment key %s is not allowed", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("current-turn environment value for %s contains NUL", key)
		}
		out[key] = value
	}
	return out, nil
}

func readEnvelope(path string) (envelope, error) {
	if err := requirePrivateRegularFile(path); err != nil {
		return envelope{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return envelope{}, err
	}
	if info.Size() > maxEnvelopeSize {
		return envelope{}, fmt.Errorf("envelope exceeds %d bytes", maxEnvelopeSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return envelope{}, err
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, maxEnvelopeSize+1))
	decoder.DisallowUnknownFields()
	var current envelope
	if err := decoder.Decode(&current); err != nil {
		return envelope{}, fmt.Errorf("decode: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return envelope{}, err
	}
	if current.Version != envelopeVersion {
		return envelope{}, fmt.Errorf("unsupported version %d", current.Version)
	}
	if strings.TrimSpace(current.TurnID) == "" {
		return envelope{}, fmt.Errorf("turn_id is required")
	}
	if !validGeneration(current.Generation) {
		return envelope{}, fmt.Errorf("invalid generation")
	}
	return current, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("decode: multiple JSON values")
	}
	return fmt.Errorf("decode trailing data: %w", err)
}

func requirePrivateRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must not be group/world accessible", filepath.Base(path))
	}
	return nil
}

func removeOtherGenerations(root, keep string) error {
	turnsDir := filepath.Join(root, "turns")
	entries, err := os.ReadDir(turnsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(turnsDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func removeGeneration(root, generation string) error {
	if !validGeneration(generation) {
		return fmt.Errorf("invalid generation")
	}
	if err := os.RemoveAll(filepath.Join(root, "turns", generation)); err != nil {
		return fmt.Errorf("remove turn generation: %w", err)
	}
	return nil
}

func randomGeneration() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func validGeneration(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func lockRoot(root string) func() {
	value, _ := transportLocks.LoadOrStore(root, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func wrapperBody(envelopePath, binaryPath string) string {
	keys := make([]string, 0, len(turnEnvironmentKeys)+2)
	keys = append(keys, "MULTICA_TOKEN", "MULTICA_TOKEN_FILE")
	for key := range turnEnvironmentKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var body bytes.Buffer
	body.WriteString("#!/bin/sh\n")
	body.WriteString("unset ")
	body.WriteString(strings.Join(keys, " "))
	body.WriteByte('\n')
	body.WriteString("export ")
	body.WriteString(EnvelopePathEnv)
	body.WriteByte('=')
	body.WriteString(shellQuote(envelopePath))
	body.WriteByte('\n')
	body.WriteString("exec ")
	body.WriteString(shellQuote(binaryPath))
	body.WriteString(" \"$@\"\n")
	return body.String()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
