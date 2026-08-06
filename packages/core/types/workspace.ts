import type { AgentMemoryGrowth } from "./agent";
import type { HonorSnapshot } from "./honor";

export type MemberRole = "owner" | "admin" | "member";

export interface Workspace {
  id: string;
  name: string;
  slug: string;
  description: string | null;
  context: string | null;
  settings: Record<string, unknown>;
  issue_prefix: string;
  avatar_url: string | null;
  /** Name-independent authority binding for the Workspace Onboarding Agent. */
  onboarding_agent_id?: string | null;
  created_at: string;
  updated_at: string;
  /**
   * When the current user was last active in this workspace (server-tracked,
   * per-member). Null until they've opened it since the feature shipped. Used
   * to route a returning user back to their last-active workspace when the
   * client `last_workspace_slug` cookie is missing (e.g. after logout) (#225).
   */
  last_active_at?: string | null;
}

export interface Member {
  id: string;
  workspace_id: string;
  user_id: string;
  role: MemberRole;
  created_at: string;
}

export interface User {
  id: string;
  /** Stable unique handle used for routing and bare @handle fallback. */
  name: string;
  /** Human-facing label. Falls back to `name` for older server payloads. */
  display_name: string;
  email: string;
  avatar_url: string | null;
  onboarded_at: string | null;
  /**
   * JSONB payload from the server. Typed as `unknown` here so this module
   * stays independent of the questionnaire shape — the onboarding views
   * cast into `Partial<QuestionnaireAnswers>` when reading. Server always
   * returns an object (defaults to `{}`), never null.
   */
  onboarding_questionnaire: Record<string, unknown>;
  /**
   * Legacy column from the removed starter-content dialog. The column is
   * still written to (always 'imported' for new accounts after the
   * mark-onboarded paths run) so older desktop builds — which still render
   * the dialog on NULL — don't show it to anyone created on a newer server.
   * Kept as `string | null` for forward compatibility.
   */
  starter_content_state: string | null;
  /** Preferred UI language. null means "follow client/system". */
  language: string | null;
  /**
   * Free-form self-description (role, stack, preferences). Injected into
   * the agent brief so coding agents have cheap, durable context about
   * who is requesting the work. Server always returns a string —
   * NOT NULL DEFAULT '' at the column level, empty when unset.
   */
  profile_description: string;
  /** Pinned IANA tz; null means "use browser-detected tz at render time". */
  timezone: string | null;
  created_at: string;
  updated_at: string;
}

export interface MemberWithUser {
  id: string;
  workspace_id: string;
  user_id: string;
  role: MemberRole;
  created_at: string;
  /** Stable user handle. */
  name: string;
  /** Human-facing member label. */
  display_name: string;
  email: string;
  avatar_url: string | null;
  profile_description: string;
  honor?: HonorSnapshot;
}

export interface MemberProfileActivityItem {
  id: string;
  kind: "command" | "tool_use" | "text" | "queued" | "working" | "failed" | "cancelled" | "task";
  label: string;
  /** Backend canonical EN label for compact/list surfaces; no raw command detail. */
  display_label: string;
  /** Stable key for display_label, matching the Activity EN label contract. */
  label_key: string;
  activity_kind: string;
  detail_kind: string;
  occurred_at: string;
  status: "queued" | "dispatched" | "running" | "completed" | "failed" | "cancelled";
}

export interface MemberProfile {
  member_type: "user" | "agent";
  member_id: string;
  /** Stable handle. */
  name: string;
  /** Human-facing label; server returns display_name || name. */
  display_name: string;
  avatar_url: string | null;
  /** User profile_description or agent description. Empty when unset. */
  description: string;
  /** Workspace role for users; "Agent" for agent profiles. */
  role: string;
  /** null for user profiles in v1. */
  status: string | null;
  /** Empty for user profiles in v1; max 5 safe items for agents. */
  recent_activity: MemberProfileActivityItem[];
  /** full when live panels are allowed; identity_only exposes only basic identity fields. */
  profile_access: "full" | "identity_only";
  /**
   * Agent Memory growth (LRM-303). Only on full-access agent profiles with
   * ≥1 valid write; omitted for users, identity_only, and zero writes.
   */
  memory_growth?: AgentMemoryGrowth | null;
  honor?: HonorSnapshot;
}

export interface Invitation {
  id: string;
  workspace_id: string;
  inviter_id: string;
  invitee_email: string;
  invitee_user_id: string | null;
  role: MemberRole;
  status: "pending" | "accepted" | "declined" | "expired";
  created_at: string;
  updated_at: string;
  expires_at: string;
  inviter_name?: string;
  inviter_email?: string;
  workspace_name?: string;
}

/** LRM-462: human member online snapshot / WS payload. */
export interface MemberPresenceEntry {
  user_id: string;
  status: "online" | "offline";
  observed_at?: string;
}

export interface MemberPresenceResponse {
  members: MemberPresenceEntry[];
}
