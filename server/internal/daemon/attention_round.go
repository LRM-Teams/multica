package daemon

// This file holds the pure, DB-agnostic Attention Round resolution engine. The
// orchestration layer (channel message flow, probe dispatch, persistence) feeds
// per-agent Attention decisions in and acts on the resolved AttentionResolution.
//
// Keeping the rules here as pure functions means the trickiest part of the
// feature — "who gets to speak when several agents raise hands" — is unit
// testable without a database, which is exactly where the earlier
// implementation's bugs lived.

// Convergence vote values an ANSWERing agent may return in the single
// convergence round.
const (
	ConvergenceVoteYield          = "YIELD"
	ConvergenceVoteKeep           = "KEEP"
	ConvergenceVoteMerge          = "MERGE"
	ConvergenceVoteRequestManager = "REQUEST_MANAGER"
)

// AttentionVoteValues lists valid convergence votes.
var AttentionVoteValues = []string{ConvergenceVoteYield, ConvergenceVoteKeep, ConvergenceVoteMerge, ConvergenceVoteRequestManager}

// ValidConvergenceVote reports whether v is a known vote value.
func ValidConvergenceVote(v string) bool {
	for _, known := range AttentionVoteValues {
		if v == known {
			return true
		}
	}
	return false
}

// AttentionRoundOutcome is the resolved disposition of one attention round.
type AttentionRoundOutcome string

const (
	// AttentionRoundOutcomeSilent means nobody claimed a public answer; stay quiet.
	AttentionRoundOutcomeSilent AttentionRoundOutcome = "silent"
	// AttentionRoundOutcomeGranted means one agent clearly won and may respond publicly.
	AttentionRoundOutcomeGranted AttentionRoundOutcome = "granted"
	// AttentionRoundOutcomeConverge means multiple agents claimed; one convergence
	// round is needed before anyone speaks.
	AttentionRoundOutcomeConverge AttentionRoundOutcome = "converge"
	// AttentionRoundOutcomeManager means coordination is needed (COORDINATE, or
	// convergence could not converge); route to the group manager.
	AttentionRoundOutcomeManager AttentionRoundOutcome = "manager"
)

// AttentionParticipant is one agent's contribution to a round. Failed/Unusable
// probes never block or auto-silence other participants.
type AttentionParticipant struct {
	AgentID  string
	Decision string // SILENT | ANSWER | CONTRIBUTE | COORDINATE (usable only)
	Summary  string // best-effort
	Usable   bool   // probe completed and parsed into a valid decision
	Failed   bool   // probe failed/unusable for this agent alone
}

// AttentionResolution is the outcome of resolving a round, plus the facts the
// orchestration layer needs to act on it.
type AttentionResolution struct {
	Outcome          AttentionRoundOutcome
	GrantAgentID     string   // set when Outcome == Granted
	ManagerReason    string   // set when Outcome == Manager
	ConvergeAgentIDs []string // set when Outcome == Converge (agents to vote)
	AnswerCount      int
	ContributeCount  int
}

// ResolveAttentionRound collapses a set of per-agent probe decisions into a
// single round disposition. Rules (from the collaboration spec + CodexLoom):
//   - A COORDINATE among usable agents always escalates to the manager.
//   - Exactly one usable ANSWER → grant that agent the public response.
//   - More than one usable ANSWER → one convergence round among them.
//   - No ANSWER (with or without CONTRIBUTE) → silent; contributions are kept as
//     internal offers, never a public response.
//   - Unusable/failed probes never decide the round (CodexLoom: one agent's
//     parse trouble must not auto-silence others).
func ResolveAttentionRound(participants []AttentionParticipant) AttentionResolution {
	var answerers []string
	managed := false
	managerReasons := []string{}
	contributes := 0

	for _, p := range participants {
		if p.Failed || !p.Usable {
			continue
		}
		switch p.Decision {
		case "ANSWER":
			answerers = append(answerers, p.AgentID)
		case "CONTRIBUTE":
			contributes++
		case "COORDINATE":
			managed = true
			managerReasons = append(managerReasons, "agent "+p.AgentID+" requested coordination")
		}
	}

	if managed {
		return AttentionResolution{
			Outcome:         AttentionRoundOutcomeManager,
			ManagerReason:   joinReasons(managerReasons),
			AnswerCount:     len(answerers),
			ContributeCount: contributes,
		}
	}

	switch len(answerers) {
	case 0:
		return AttentionResolution{
			Outcome:         AttentionRoundOutcomeSilent,
			AnswerCount:     0,
			ContributeCount: contributes,
		}
	case 1:
		return AttentionResolution{
			Outcome:         AttentionRoundOutcomeGranted,
			GrantAgentID:    answerers[0],
			AnswerCount:     1,
			ContributeCount: contributes,
		}
	default:
		return AttentionResolution{
			Outcome:          AttentionRoundOutcomeConverge,
			ConvergeAgentIDs: answerers,
			AnswerCount:      len(answerers),
			ContributeCount:  contributes,
		}
	}
}

// ConvergenceVote is one ANSWERing agent's vote in the single convergence round.
type ConvergenceVote struct {
	AgentID       string // voter
	Vote          string // YIELD | KEEP | MERGE | REQUEST_MANAGER
	TargetAgentID string // when Merge: the agent to hand the primary role to
}

// ResolveConvergence resolves one convergence round among the ANSWERing agents.
//   - Any REQUEST_MANAGER → manager (escalate).
//   - Exactly one KEEP → grant that (sole non-yielding) agent.
//   - MERGE with a single consistent target → grant that target.
//   - Multiple distinct KEEP → manager (unresolvable head-to-head).
//   - All YIELD / no clear primary → manager (something claimed but no owner).
func ResolveConvergence(votes []ConvergenceVote) AttentionResolution {
	out := AttentionResolution{AnswerCount: len(votes)}

	keepAgent := ""
	keepCount := 0
	mergeTarget := ""
	mergeCount := 0
	multiMerge := false
	wantManager := false

	for _, v := range votes {
		switch v.Vote {
		case ConvergenceVoteRequestManager:
			wantManager = true
		case ConvergenceVoteKeep:
			keepCount++
			keepAgent = v.AgentID
		case ConvergenceVoteMerge:
			if v.TargetAgentID == "" {
				continue
			}
			mergeCount++
			if mergeTarget == "" {
				mergeTarget = v.TargetAgentID
			} else if mergeTarget != v.TargetAgentID {
				multiMerge = true
			}
		}
	}

	if wantManager || keepCount > 1 || multiMerge {
		out.Outcome = AttentionRoundOutcomeManager
		out.ManagerReason = "convergence did not converge"
		if keepCount > 1 {
			out.ManagerReason = "multiple KEEP votes conflicted"
		}
		return out
	}
	if keepCount == 1 {
		out.Outcome = AttentionRoundOutcomeGranted
		out.GrantAgentID = keepAgent
		return out
	}
	if mergeCount >= 1 && mergeTarget != "" {
		out.Outcome = AttentionRoundOutcomeGranted
		out.GrantAgentID = mergeTarget
		return out
	}
	// All YIELD or empty: claimed but no owner → manager.
	out.Outcome = AttentionRoundOutcomeManager
	out.ManagerReason = "no agent kept the claim after convergence"
	return out
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "coordination requested"
	}
	out := reasons[0]
	for _, r := range reasons[1:] {
		out += "; " + r
	}
	return out
}
