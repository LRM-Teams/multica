package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	agentAppInboxStateVersion = 3
	agentAppInboxPreviewLimit = 120
	reminderDueClass          = "due"
)

var positiveDecimalPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

type AgentAppInboxSourceRef struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision string `json:"revision,omitempty"`
}

type AgentAppInboxPrimaryAction struct {
	Kind      string `json:"kind"`
	CommandID string `json:"commandId"`
}

type AgentAppInboxItem struct {
	Source            string                           `json:"source"`
	ItemID            string                           `json:"itemId"`
	AppID             string                           `json:"appId"`
	NotificationClass string                           `json:"notificationClass"`
	SourceRef         AgentAppInboxSourceRef           `json:"sourceRef"`
	PrimaryAction     AgentAppInboxPrimaryAction       `json:"primaryAction"`
	ActionCLI         string                           `json:"actionCli"`
	Retention         string                           `json:"retention"`
	Title             string                           `json:"title,omitempty"`
	Summary           string                           `json:"summary,omitempty"`
	CreatedAtMS       int64                            `json:"createdAtMs"`
	Message           *protocol.AgentMessageProjection `json:"message,omitempty"`
}

type AgentAppInboxAcknowledgedSource struct {
	AppID             string                 `json:"appId"`
	NotificationClass string                 `json:"notificationClass"`
	SourceRef         AgentAppInboxSourceRef `json:"sourceRef"`
	ItemID            string                 `json:"itemId"`
	AcknowledgedAtMS  int64                  `json:"acknowledgedAtMs"`
	OwnerAgentID      string                 `json:"ownerAgentId,omitempty"`
}

type AgentAppInboxAckIntent struct {
	AppID             string                 `json:"appId"`
	NotificationClass string                 `json:"notificationClass"`
	SourceRef         AgentAppInboxSourceRef `json:"sourceRef"`
	ItemID            string                 `json:"itemId"`
	AckAttemptID      string                 `json:"ackAttemptId"`
	CreatedAtMS       int64                  `json:"createdAtMs"`
	OwnerAgentID      string                 `json:"ownerAgentId,omitempty"`
}

type agentAppInboxState struct {
	Version             int                               `json:"version"`
	Items               []AgentAppInboxItem               `json:"items"`
	AcknowledgedSources []AgentAppInboxAcknowledgedSource `json:"acknowledgedSources"`
	AckIntents          []AgentAppInboxAckIntent          `json:"ackIntents"`
}

type AgentAppInboxMintInput struct {
	AppID             string
	NotificationClass string
	SourceRef         AgentAppInboxSourceRef
	Title             string
	Summary           string
	Message           *protocol.AgentMessageProjection
}

type AgentAppInboxStore struct {
	mu                     sync.Mutex
	ownerAgentID           string
	path                   string
	items                  map[string]AgentAppInboxItem
	identityIndex          map[string]string
	acknowledgedSources    map[string]AgentAppInboxAcknowledgedSource
	ackIntents             map[string]AgentAppInboxAckIntent
	now                    func() time.Time
	beforeAck              func(AgentAppInboxItem) bool
	beforeServerAuthorized func(AgentAppInboxItem, AgentAppInboxAckIntent) bool
	writeState             func(string, []byte) error
}

type agentAppInboxMemoryState struct {
	items               map[string]AgentAppInboxItem
	identityIndex       map[string]string
	acknowledgedSources map[string]AgentAppInboxAcknowledgedSource
	ackIntents          map[string]AgentAppInboxAckIntent
}

func newAgentAppInboxStore(ownerAgentID, path string) *AgentAppInboxStore {
	return &AgentAppInboxStore{
		ownerAgentID:        ownerAgentID,
		path:                path,
		items:               make(map[string]AgentAppInboxItem),
		identityIndex:       make(map[string]string),
		acknowledgedSources: make(map[string]AgentAppInboxAcknowledgedSource),
		ackIntents:          make(map[string]AgentAppInboxAckIntent),
		now:                 time.Now,
		writeState:          writeDaemonStateAtomically,
	}
}

func (s *AgentAppInboxStore) Restore() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read agent app inbox: %w", err)
	}
	var state agentAppInboxState
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("decode agent app inbox: %w", err)
	}
	if state.Version != agentAppInboxStateVersion || state.Items == nil || state.AcknowledgedSources == nil || state.AckIntents == nil {
		return errors.New("agent app inbox persistence envelope invalid")
	}
	items := make(map[string]AgentAppInboxItem, len(state.Items))
	index := make(map[string]string, len(state.Items))
	for _, item := range state.Items {
		if err := validatePersistedInboxItem(item); err != nil {
			return err
		}
		identity := agentAppInboxIdentity(item.AppID, item.NotificationClass, item.SourceRef)
		if _, duplicate := index[identity]; duplicate {
			return errors.New("agent app inbox persisted identity duplicated")
		}
		items[item.ItemID] = item
		index[identity] = item.ItemID
	}
	acknowledged := make(map[string]AgentAppInboxAcknowledgedSource, len(state.AcknowledgedSources))
	for _, source := range state.AcknowledgedSources {
		if err := validatePersistedAcknowledgedSource(source); err != nil {
			return err
		}
		acknowledged[agentAppInboxIdentity(source.AppID, source.NotificationClass, source.SourceRef)] = source
	}
	intents := make(map[string]AgentAppInboxAckIntent, len(state.AckIntents))
	for _, intent := range state.AckIntents {
		if err := validatePersistedAckIntent(intent); err != nil {
			return err
		}
		item, ok := items[intent.ItemID]
		if !ok || agentAppInboxIdentity(item.AppID, item.NotificationClass, item.SourceRef) != agentAppInboxIdentity(intent.AppID, intent.NotificationClass, intent.SourceRef) {
			return errors.New("agent app inbox ACK intent item binding invalid")
		}
		intents[intent.ItemID] = intent
	}
	s.items = items
	s.identityIndex = index
	s.acknowledgedSources = acknowledged
	s.ackIntents = intents
	return nil
}

func (s *AgentAppInboxStore) Mint(input AgentAppInboxMintInput) (AgentAppInboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input.AppID = strings.TrimSpace(input.AppID)
	input.NotificationClass = strings.TrimSpace(input.NotificationClass)
	if input.AppID != reminderInboxAppID && input.AppID != agentInboxAppID {
		return AgentAppInboxItem{}, fmt.Errorf("unknown appId: %s", input.AppID)
	}
	if input.AppID == reminderInboxAppID && input.NotificationClass != reminderDueClass || input.AppID == agentInboxAppID && input.NotificationClass != "message" {
		return AgentAppInboxItem{}, fmt.Errorf("unknown notificationClass for %s: %s", input.AppID, input.NotificationClass)
	}
	ref := input.SourceRef
	var err error
	if input.AppID == reminderInboxAppID {
		ref, err = normalizeReminderSourceRef(input.SourceRef)
	} else if input.Message == nil || input.Message.ID == "" || ref.Kind != "message" || ref.ID != input.Message.ID || ref.Revision == "" {
		err = errors.New("message inbox sourceRef and projection are invalid")
	}
	if err != nil {
		return AgentAppInboxItem{}, err
	}
	title, err := sanitizeAgentInboxPreview(input.Title)
	if err != nil {
		return AgentAppInboxItem{}, fmt.Errorf("invalid title: %w", err)
	}
	summary, err := sanitizeAgentInboxPreview(input.Summary)
	if err != nil {
		return AgentAppInboxItem{}, fmt.Errorf("invalid summary: %w", err)
	}
	identity := agentAppInboxIdentity(input.AppID, input.NotificationClass, ref)
	itemID := reminderInboxItemID(ref)
	if input.AppID == agentInboxAppID {
		itemID = "message:" + ref.ID + ":" + ref.Revision
	}
	createdAt := s.now().UnixMilli()
	if existingID := s.identityIndex[identity]; existingID != "" {
		createdAt = s.items[existingID].CreatedAtMS
		itemID = existingID
	}
	item := AgentAppInboxItem{
		Source:            "app",
		ItemID:            itemID,
		AppID:             input.AppID,
		NotificationClass: input.NotificationClass,
		SourceRef:         ref,
		PrimaryAction:     AgentAppInboxPrimaryAction{Kind: "run_command", CommandID: "reminder.ack"},
		ActionCLI:         "multica reminder ack --id " + shortReminderID(ref.ID) + " --revision " + ref.Revision,
		Retention:         "until_explicit_ack",
		Title:             title,
		Summary:           summary,
		CreatedAtMS:       createdAt,
		Message:           input.Message,
	}
	if input.AppID == agentInboxAppID {
		item.PrimaryAction = AgentAppInboxPrimaryAction{Kind: "run_command", CommandID: "message.check"}
		item.Retention = "until_explicit_ack"
	}
	previous := s.captureMemoryStateLocked()
	s.items[itemID] = item
	s.identityIndex[identity] = itemID
	delete(s.acknowledgedSources, identity)
	delete(s.ackIntents, itemID)
	if err := s.persistLocked(); err != nil {
		s.restoreMemoryStateLocked(previous)
		return AgentAppInboxItem{}, err
	}
	return item, nil
}

func (s *AgentAppInboxStore) List() []AgentAppInboxItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]AgentAppInboxItem, 0, len(s.items))
	for _, item := range s.items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAtMS > result[j].CreatedAtMS || result[i].CreatedAtMS == result[j].CreatedAtMS && result[i].ItemID < result[j].ItemID
	})
	return result
}

// ListMessageItems returns the durable message projection owned by this app
// inbox. Callers receive copies and must use Ack to retire an item.
func (s *AgentAppInboxStore) ListMessageItems() []AgentAppInboxItem {
	items := s.List()
	result := make([]AgentAppInboxItem, 0, len(items))
	for _, item := range items {
		if item.AppID == agentInboxAppID && item.NotificationClass == "message" && item.Message != nil {
			result = append(result, item)
		}
	}
	return result
}

func (s *AgentAppInboxStore) Item(itemID string) (AgentAppInboxItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[itemID]
	return item, ok
}

func (s *AgentAppInboxStore) ListAcknowledgedSources() []AgentAppInboxAcknowledgedSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]AgentAppInboxAcknowledgedSource, 0, len(s.acknowledgedSources))
	for _, source := range s.acknowledgedSources {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].AcknowledgedAtMS > result[j].AcknowledgedAtMS || result[i].AcknowledgedAtMS == result[j].AcknowledgedAtMS && result[i].ItemID < result[j].ItemID
	})
	return result
}

func (s *AgentAppInboxStore) ListAckIntents() []AgentAppInboxAckIntent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]AgentAppInboxAckIntent, 0, len(s.ackIntents))
	for _, intent := range s.ackIntents {
		result = append(result, intent)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAtMS < result[j].CreatedAtMS || result[i].CreatedAtMS == result[j].CreatedAtMS && result[i].ItemID < result[j].ItemID
	})
	return result
}

func (s *AgentAppInboxStore) Ack(itemID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[itemID]
	if !ok || s.beforeAck != nil && !s.beforeAck(item) {
		return false
	}
	previous := s.captureMemoryStateLocked()
	s.acknowledgeLocked(item)
	if s.persistLocked() != nil {
		s.restoreMemoryStateLocked(previous)
		return false
	}
	return true
}

func (s *AgentAppInboxStore) BeginServerAuthorizedAckIntent(itemID, attemptID string) *AgentAppInboxAckIntent {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[itemID]
	if !ok {
		return nil
	}
	if existing, ok := s.ackIntents[itemID]; ok {
		copy := existing
		return &copy
	}
	intent := AgentAppInboxAckIntent{
		AppID: item.AppID, NotificationClass: item.NotificationClass, SourceRef: item.SourceRef,
		ItemID: item.ItemID, AckAttemptID: attemptID, CreatedAtMS: s.now().UnixMilli(), OwnerAgentID: s.ownerAgentID,
	}
	previous := s.captureMemoryStateLocked()
	s.ackIntents[itemID] = intent
	if s.persistLocked() != nil {
		s.restoreMemoryStateLocked(previous)
		return nil
	}
	return &intent
}

func (s *AgentAppInboxStore) CompleteServerAuthorizedAck(itemID, attemptID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, itemOK := s.items[itemID]
	intent, intentOK := s.ackIntents[itemID]
	if !itemOK || !intentOK || intent.AckAttemptID != attemptID || agentAppInboxIdentity(item.AppID, item.NotificationClass, item.SourceRef) != agentAppInboxIdentity(intent.AppID, intent.NotificationClass, intent.SourceRef) {
		return false
	}
	if s.beforeServerAuthorized != nil && !s.beforeServerAuthorized(item, intent) {
		return false
	}
	previous := s.captureMemoryStateLocked()
	s.acknowledgeLocked(item)
	delete(s.ackIntents, itemID)
	if s.persistLocked() != nil {
		s.restoreMemoryStateLocked(previous)
		return false
	}
	return true
}

func (s *AgentAppInboxStore) ClearServerAuthorizedAckIntent(itemID, attemptID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.ackIntents[itemID]
	if !ok || intent.AckAttemptID != attemptID {
		return false
	}
	previous := s.captureMemoryStateLocked()
	delete(s.ackIntents, itemID)
	if s.persistLocked() != nil {
		s.restoreMemoryStateLocked(previous)
		return false
	}
	return true
}

func (s *AgentAppInboxStore) acknowledgeLocked(item AgentAppInboxItem) {
	identity := agentAppInboxIdentity(item.AppID, item.NotificationClass, item.SourceRef)
	s.acknowledgedSources[identity] = AgentAppInboxAcknowledgedSource{
		AppID: item.AppID, NotificationClass: item.NotificationClass, SourceRef: item.SourceRef,
		ItemID: item.ItemID, AcknowledgedAtMS: s.now().UnixMilli(), OwnerAgentID: s.ownerAgentID,
	}
	delete(s.items, item.ItemID)
	delete(s.identityIndex, identity)
}

func (s *AgentAppInboxStore) captureMemoryStateLocked() agentAppInboxMemoryState {
	state := agentAppInboxMemoryState{
		items:               make(map[string]AgentAppInboxItem, len(s.items)),
		identityIndex:       make(map[string]string, len(s.identityIndex)),
		acknowledgedSources: make(map[string]AgentAppInboxAcknowledgedSource, len(s.acknowledgedSources)),
		ackIntents:          make(map[string]AgentAppInboxAckIntent, len(s.ackIntents)),
	}
	for key, value := range s.items {
		state.items[key] = value
	}
	for key, value := range s.identityIndex {
		state.identityIndex[key] = value
	}
	for key, value := range s.acknowledgedSources {
		state.acknowledgedSources[key] = value
	}
	for key, value := range s.ackIntents {
		state.ackIntents[key] = value
	}
	return state
}

func (s *AgentAppInboxStore) restoreMemoryStateLocked(state agentAppInboxMemoryState) {
	s.items = state.items
	s.identityIndex = state.identityIndex
	s.acknowledgedSources = state.acknowledgedSources
	s.ackIntents = state.ackIntents
}

func (s *AgentAppInboxStore) persistLocked() error {
	state := agentAppInboxState{Version: agentAppInboxStateVersion, Items: make([]AgentAppInboxItem, 0, len(s.items)), AcknowledgedSources: make([]AgentAppInboxAcknowledgedSource, 0, len(s.acknowledgedSources)), AckIntents: make([]AgentAppInboxAckIntent, 0, len(s.ackIntents))}
	for _, item := range s.items {
		state.Items = append(state.Items, item)
	}
	for _, source := range s.acknowledgedSources {
		state.AcknowledgedSources = append(state.AcknowledgedSources, source)
	}
	for _, intent := range s.ackIntents {
		state.AckIntents = append(state.AckIntents, intent)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.writeState(s.path, append(raw, '\n'))
}

func normalizeReminderSourceRef(ref AgentAppInboxSourceRef) (AgentAppInboxSourceRef, error) {
	if ref.Kind != "reminder" {
		return AgentAppInboxSourceRef{}, errors.New("reminder sourceRef.kind must be reminder")
	}
	parsed, err := uuid.Parse(ref.ID)
	if err != nil || parsed.String() != strings.ToLower(ref.ID) {
		return AgentAppInboxSourceRef{}, errors.New("reminder sourceRef.id must be a canonical UUID")
	}
	if !positiveDecimalPattern.MatchString(ref.Revision) {
		return AgentAppInboxSourceRef{}, errors.New("reminder sourceRef.revision must be a positive integer")
	}
	return AgentAppInboxSourceRef{Kind: "reminder", ID: parsed.String(), Revision: ref.Revision}, nil
}

func validatePersistedReminderInboxItem(item AgentAppInboxItem) error {
	ref, err := normalizeReminderSourceRef(item.SourceRef)
	if err != nil || item.Source != "app" || item.AppID != reminderInboxAppID || item.NotificationClass != reminderDueClass || item.Retention != "until_explicit_ack" || item.ItemID != reminderInboxItemID(ref) || item.PrimaryAction != (AgentAppInboxPrimaryAction{Kind: "run_command", CommandID: "reminder.ack"}) || item.ActionCLI != "multica reminder ack --id "+shortReminderID(ref.ID)+" --revision "+ref.Revision || item.CreatedAtMS < 0 {
		return errors.New("agent app inbox persisted item invalid")
	}
	if _, err := sanitizeAgentInboxPreview(item.Title); err != nil {
		return errors.New("agent app inbox persisted item preview invalid")
	}
	if _, err := sanitizeAgentInboxPreview(item.Summary); err != nil {
		return errors.New("agent app inbox persisted item preview invalid")
	}
	return nil
}

func validatePersistedInboxItem(item AgentAppInboxItem) error {
	if item.AppID == agentInboxAppID {
		if item.Source != "app" || item.NotificationClass != "message" || item.Retention != "until_explicit_ack" || item.Message == nil || item.SourceRef.Kind != "message" || item.SourceRef.ID != item.Message.ID || item.SourceRef.Revision == "" || item.ItemID != "message:"+item.SourceRef.ID+":"+item.SourceRef.Revision || item.CreatedAtMS < 0 {
			return errors.New("agent app inbox persisted message item invalid")
		}
		return nil
	}
	return validatePersistedReminderInboxItem(item)
}

func validatePersistedAcknowledgedSource(source AgentAppInboxAcknowledgedSource) error {
	if source.AppID == agentInboxAppID {
		if source.NotificationClass != "message" || source.SourceRef.Kind != "message" || source.SourceRef.ID == "" || source.SourceRef.Revision == "" || source.ItemID != "message:"+source.SourceRef.ID+":"+source.SourceRef.Revision || source.AcknowledgedAtMS < 0 {
			return errors.New("agent app inbox acknowledged message source invalid")
		}
		return nil
	}
	ref, err := normalizeReminderSourceRef(source.SourceRef)
	if err != nil || source.AppID != reminderInboxAppID || source.NotificationClass != reminderDueClass || source.ItemID != reminderInboxItemID(ref) || source.AcknowledgedAtMS < 0 {
		return errors.New("agent app inbox acknowledged source invalid")
	}
	return nil
}

func validatePersistedAckIntent(intent AgentAppInboxAckIntent) error {
	if intent.AppID == agentInboxAppID {
		if intent.NotificationClass != "message" || intent.SourceRef.Kind != "message" || intent.SourceRef.ID == "" || intent.SourceRef.Revision == "" || intent.ItemID != "message:"+intent.SourceRef.ID+":"+intent.SourceRef.Revision || strings.TrimSpace(intent.AckAttemptID) == "" || intent.CreatedAtMS < 0 {
			return errors.New("agent app inbox message ACK intent invalid")
		}
		return nil
	}
	ref, err := normalizeReminderSourceRef(intent.SourceRef)
	if err != nil || intent.AppID != reminderInboxAppID || intent.NotificationClass != reminderDueClass || intent.ItemID != reminderInboxItemID(ref) || strings.TrimSpace(intent.AckAttemptID) == "" || intent.CreatedAtMS < 0 {
		return errors.New("agent app inbox ACK intent invalid")
	}
	return nil
}

func sanitizeAgentInboxPreview(value string) (string, error) {
	if len([]rune(value)) > agentAppInboxPreviewLimit {
		return "", fmt.Errorf("preview exceeds %d characters", agentAppInboxPreviewLimit)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("preview must be single-line without control characters")
		}
	}
	return value, nil
}

func agentAppInboxIdentity(appID, class string, ref AgentAppInboxSourceRef) string {
	return appID + "\x00" + class + "\x00" + ref.Kind + "\x00" + ref.ID + "\x00" + ref.Revision
}

func reminderInboxItemID(ref AgentAppInboxSourceRef) string {
	return "reminder:" + ref.ID + ":" + ref.Revision
}

func shortReminderID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

type AgentAppInboxRegistry struct {
	mu        sync.Mutex
	root      string
	stores    map[string]*AgentAppInboxStore
	beforeAck func(string, AgentAppInboxItem) bool
}

func newAgentAppInboxRegistry(root string, beforeAck func(string, AgentAppInboxItem) bool) *AgentAppInboxRegistry {
	return &AgentAppInboxRegistry{root: root, stores: make(map[string]*AgentAppInboxStore), beforeAck: beforeAck}
}

func (r *AgentAppInboxRegistry) Store(agentID string) (*AgentAppInboxStore, error) {
	if r == nil {
		return nil, errors.New("app inbox unavailable")
	}
	if r.root == "" {
		return nil, errors.New("app inbox storage identity is unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	if _, err := uuid.Parse(agentID); err != nil {
		return nil, errors.New("app inbox owner Agent ID must be a UUID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if store := r.stores[agentID]; store != nil {
		return store, nil
	}
	store := newAgentAppInboxStore(agentID, filepath.Join(r.root, agentID, "state.json"))
	store.beforeAck = func(item AgentAppInboxItem) bool {
		return r.beforeAck == nil || r.beforeAck(agentID, item)
	}
	store.beforeServerAuthorized = func(item AgentAppInboxItem, _ AgentAppInboxAckIntent) bool {
		return r.beforeAck == nil || r.beforeAck(agentID, item)
	}
	if err := store.Restore(); err != nil {
		return nil, err
	}
	r.stores[agentID] = store
	return store, nil
}

func (r *AgentAppInboxRegistry) OwnerIDs() ([]string, error) {
	if r == nil {
		return nil, errors.New("app inbox unavailable")
	}
	entries, err := os.ReadDir(r.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read app inbox registry: %w", err)
	}
	owners := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		owner := entry.Name()
		if _, err := uuid.Parse(owner); err != nil {
			return nil, fmt.Errorf("invalid app inbox owner directory %q", entry.Name())
		}
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	return owners, nil
}

func reminderRevision(ref AgentAppInboxSourceRef) (int64, bool) {
	version, err := strconv.ParseInt(ref.Revision, 10, 64)
	return version, err == nil && version > 0
}

func appInboxNoticeFingerprint(items []AgentAppInboxItem) string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ItemID)
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (d *Daemon) notifyAgentAppInbox(ctx context.Context, agentID, runtimeID string) error {
	if d == nil || d.agentAppInboxes == nil || d.canonicalRuntimes == nil {
		return errors.New("Agent App Inbox notice unavailable")
	}
	store, err := d.agentAppInboxes.Store(agentID)
	if err != nil {
		return err
	}
	items := store.List()
	if len(items) == 0 {
		d.canonicalRuntimes.clearAppInboxNoticeMemo(agentID, runtimeID)
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	noticeCtx, cancel := context.WithTimeout(ctx, pendingNoticeWriteTimeout)
	defer cancel()
	return d.canonicalRuntimes.deliverAppInboxNotice(noticeCtx, agentID, runtimeID, agent.ResidentPendingNotice{
		PendingAppItems: len(items),
	}, appInboxNoticeFingerprint(items))
}

func (d *Daemon) enqueueAgentAppInboxNotice(agentID, runtimeID string) bool {
	if d == nil || agentID == "" || runtimeID == "" {
		return false
	}
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	runner := d.currentWorkspaceDaemon(workspaceID)
	if runner == nil {
		return false
	}
	if _, managed := runner.managedLaunch(agentID, runtimeID); managed {
		return d.notifyAgentAppInbox(context.Background(), agentID, runtimeID) == nil
	}
	residency, ok := runner.residency.get(agentID)
	if !ok || !residency.idle || residency.runtimeID != runtimeID {
		return false
	}
	parent := runner.life
	if parent == nil {
		parent = context.Background()
	}
	restartCtx, started := runner.residency.beginRestart(parent, agentID)
	if !started {
		return true
	}
	if err := runner.restartFromIdleSnapshot(agentID, residency); err != nil {
		runner.residency.endRestart(agentID)
		return false
	}
	go func() {
		defer runner.residency.endRestart(agentID)
		runner.completeIdleSnapshotStart(restartCtx, agentID, residency)
	}()
	return true
}
