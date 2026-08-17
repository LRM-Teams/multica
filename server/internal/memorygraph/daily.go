package memorygraph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// dailyDateFormat is the date component of daily node ids and of
// Node.LateForDate.
const dailyDateFormat = "2006-01-02"

// DailyNodeID is the stable graph-local daily identity (spec §6):
//
//	project graph: daily:<agent>:<project>:<channel|none>:<YYYY-MM-DD>
//	channel graph: daily:<agent>:none:<channel>:<YYYY-MM-DD>
func DailyNodeID(agentID, projectID, channelID string, day time.Time) string {
	if projectID == "" {
		projectID = "none"
	}
	if channelID == "" {
		channelID = "none"
	}
	return fmt.Sprintf("daily:%s:%s:%s:%s", agentID, projectID, channelID, day.Format(dailyDateFormat))
}

// dailyNodeDate extracts the local date of a daily node id, or "" when id
// is not a daily node id.
func dailyNodeDate(id string) string {
	if !strings.HasPrefix(id, "daily:") {
		return ""
	}
	day := id[strings.LastIndex(id, ":")+1:]
	if _, err := time.Parse(dailyDateFormat, day); err != nil {
		return ""
	}
	return day
}

// DailyEvent is one source event merged into a daily node.
type DailyEvent struct {
	AgentID, ProjectID, ChannelID, TaskID, Text string
	OccurredAt                                  time.Time
}

// DailyUpdater owns daily-node update and seal. All mutations serialize on
// the injected locker (the server's GraphMutationCoordinator in production;
// a mutex in tests). Every mutation is a new lightweight graph version —
// readers never observe in-place edits (spec §6).
type DailyUpdater struct {
	store *Store
	loc   *time.Location
	now   func() time.Time
	lock  func(ctx context.Context, fn func() error) error
}

func NewDailyUpdater(store *Store, loc *time.Location) *DailyUpdater {
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600) // memorycuration.DefaultTimezone Asia/Shanghai
	}
	return &DailyUpdater{store: store, loc: loc, now: time.Now, lock: func(ctx context.Context, fn func() error) error { return fn() }}
}

// Location exposes the updater's timezone for tests.
func (u *DailyUpdater) Location() *time.Location { return u.loc }

// SetLocker injects the per-graph mutation serializer.
func (u *DailyUpdater) SetLocker(lock func(ctx context.Context, fn func() error) error) {
	if lock != nil {
		u.lock = lock
	}
}

// Record merges one event into its daily node. Same-day events land in
// today's open daily. An event for the prior day merges into that daily
// while it is still open; once its date is sealed the event lands in the
// current open daily with late_for_date provenance — sealed nodes are
// never mutated (spec §6). Events older than the prior day never resurrect
// a stale daily: they land in the open daily with late_for_date.
func (u *DailyUpdater) Record(ctx context.Context, ev DailyEvent) error {
	return u.lock(ctx, func() error {
		nowLocal := u.now().In(u.loc)
		today := nowLocal.Format(dailyDateFormat)
		eventLocal := ev.OccurredAt.In(u.loc)
		eventDay := eventLocal.Format(dailyDateFormat)

		id := DailyNodeID(ev.AgentID, ev.ProjectID, ev.ChannelID, eventLocal)
		lateFor := ""
		if eventDay != today {
			nodes, err := u.currentNodes()
			if err != nil {
				return err
			}
			yesterday := nowLocal.AddDate(0, 0, -1).Format(dailyDateFormat)
			existing := nodes[id]
			switch {
			case existing != nil && existing.SealedAt != nil:
				lateFor = eventDay // sealed: redirect to the open daily
			case eventDay != yesterday:
				lateFor = eventDay // stale date: never resurrect an old daily
			}
			// Otherwise the prior day's daily is absent or still open:
			// merge into it so the seal pass closes a complete day.
			if lateFor != "" {
				id = DailyNodeID(ev.AgentID, ev.ProjectID, ev.ChannelID, nowLocal)
			}
		}
		return u.mutateDaily(id, ev, lateFor, false)
	})
}

// SealPriorDay seals the open daily nodes of every local date before today
// with a compare-and-swap on sealed_at; already-sealed nodes are skipped
// (CAS lost). Sealing all past dates, not only yesterday's, also closes
// prior-day dailies created by events that raced the previous seal pass.
// The scheduler invokes it after local midnight plus the ten-minute grace
// period. It returns the first sealed node id, or "" when nothing needed
// sealing.
func (u *DailyUpdater) SealPriorDay(ctx context.Context) (string, error) {
	var sealedID string
	err := u.lock(ctx, func() error {
		today := u.now().In(u.loc).Format(dailyDateFormat)
		nodes, err := u.currentNodes()
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(nodes))
		for id := range nodes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			n := nodes[id]
			day := dailyNodeDate(id)
			if day == "" || day >= today || n.SealedAt != nil {
				continue
			}
			if err := u.mutateDaily(id, DailyEvent{}, "", true); err != nil {
				return err
			}
			if sealedID == "" {
				sealedID = id
			}
		}
		return nil
	})
	return sealedID, err
}

// currentNodes loads the nodes of the current version keyed by node id.
func (u *DailyUpdater) currentNodes() (map[string]*Node, error) {
	v, err := u.store.CurrentVersion()
	if err != nil {
		return nil, err
	}
	list, err := u.store.LoadNodes(v)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*Node, len(list))
	for _, n := range list {
		out[n.NodeID] = n
	}
	return out, nil
}

// mutateDaily applies one update or seal as a fresh graph version: the new
// version is fully written (a copy of current with the one changed node
// overwritten) before the current pointer switches.
func (u *DailyUpdater) mutateDaily(id string, ev DailyEvent, lateFor string, seal bool) error {
	nodes, err := u.currentNodes()
	if err != nil {
		return err
	}
	node := nodes[id]
	if node == nil {
		node = &Node{
			NodeID:         id,
			CreatedBy:      "daily-updater",
			Epistemic:      StatusProposed,
			TemporalStatus: TemporalCurrent,
		}
		if ev.ChannelID != "" {
			node.Visibility = "channel" // channel daily nodes are channel-visible (§6)
			node.ChannelID = ev.ChannelID
		} else {
			node.Visibility = "project"
		}
	}
	if node.SealedAt != nil {
		return fmt.Errorf("graph_mutation_busy: daily node %s already sealed", id)
	}
	if seal {
		now := u.now().UTC()
		node.SealedAt = &now
	} else {
		line := "- " + strings.TrimSpace(ev.Text)
		if ev.TaskID != "" {
			line += " (task " + ev.TaskID + ")"
		}
		node.Body = strings.TrimSpace(node.Body + "\n" + line)
		if lateFor != "" {
			node.LateForDate = lateFor
		}
		if ev.AgentID != "" {
			node.SourceAgentIDs = mergeStringSet(node.SourceAgentIDs, []string{ev.AgentID})
		}
		if ev.ChannelID != "" {
			node.SourceChannelIDs = mergeStringSet(node.SourceChannelIDs, []string{ev.ChannelID})
		}
		if ev.TaskID != "" {
			node.SourceTaskIDs = mergeStringSet(node.SourceTaskIDs, []string{ev.TaskID})
		}
	}
	current, err := u.store.CurrentVersion()
	if err != nil {
		return err
	}
	next, err := u.store.CreateVersionFrom(current, "daily-updater")
	if err != nil {
		return err
	}
	if node.CreatedVersion == 0 {
		node.CreatedVersion = next
	}
	node.UpdatedVersion = next
	if err := u.store.SaveNode(next, node); err != nil {
		return err
	}
	return u.store.SwitchCurrent(next)
}
