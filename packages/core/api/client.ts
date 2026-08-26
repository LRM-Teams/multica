import type { z } from "zod";
import type {
  ChannelMemberRole,
  Issue,
  CreateIssueRequest,
  UpdateIssueRequest,
  GroupedIssuesResponse,
  ProjectGroupedIssuesResponse,
  ListIssuesResponse,
  SearchIssuesResponse,
  SearchProjectsResponse,
  UpdateMeRequest,
  CreateMemberRequest,
  UpdateMemberRequest,
  ListIssuesParams,
  ListGroupedIssuesParams,
  Agent,
  AgentPresenceResponse,
  ComputerConnection,
  AgentFileContentResponse,
  AgentFilesResponse,
  ListAgentFilesParams,
  CreateAgentRequest,
  CreateAgentDraftRequest,
  AgentCreationDraft,
  EnsureWindyResponse,
  EnsurePeriodBriefAgentResponse,
  EnsureNotesAssistantAgentResponse,
  EnsurePeriodBriefCollectorsResponse,
  AgentTemplate,
  AgentTemplateSummary,
  CreateAgentFromTemplateRequest,
  CreateAgentFromTemplateResponse,
  EvolutionMetricsResponse,
  EvolutionTrainingExample,
  EvolutionTrainingExampleListResponse,
  EvolutionTrainingExampleCreateRequest,
  EvolutionTrainingExampleUpdateRequest,
  EvolutionModelRuntimeConfig,
  EvolutionModelRuntimeConfigListResponse,
  EvolutionModelRuntimeConfigUpdateRequest,
  EvolutionModelEvalRun,
  EvolutionModelEvalRunCreateRequest,
  EvolutionModelEvalRunListResponse,
  EvolutionReviewDecisionRequest,
  EvolutionReviewSubmission,
  EvolutionReviewSubmissionStatus,
  WorkspaceMemoryCurationStatus,
  MemoryCurationRunDetail,
  MemoryCuratorProfile,
  UpdateMemoryCuratorProfileRequest,
  GraphMemoryProfile,
  UpdateGraphMemoryProfileRequest,
  GraphMemoryChannelMode,
  GraphMemoryMessageCitations,
  GraphMemoryStatus,
  GraphMemoryAuditSummary,
  GraphMemoryChannelLineage,
  GraphMemoryConsolidationRun,
  StartMemoryCurationRunRequest,
  StartMemoryCurationRunResponse,
  MemoryCurationBackfillRequest,
  MemoryCurationBackfillResponse,
  MemoryCurationDailySummaryResponse,
  MemoryCurationCandidateItem,
  MemoryCurationCandidateListResponse,
  TeamKnowledgeListItem,
  TeamKnowledgeListResponse,
  KnowledgeNeighborsResponse,
  PromoteKnowledgeRequest,
  PromoteKnowledgeResponse,
  PromoteEvolutionReviewSubmissionResponse,
  UpdateAgentRequest,
  BulkUpdateAgentRuntimeConfigRequest,
  BulkUpdateAgentRuntimeConfigResponse,
  BulkAgentLifecycleRequest,
  BulkAgentLifecycleResponse,
  AgentEnvResponse,
  UpdateAgentEnvRequest,
  RuntimeEnvResponse,
  UpdateRuntimeEnvRequest,
  UpdateAgentFileContentRequest,
  UpdateAgentFileContentResponse,
  AgentTask,
  RunnerActivityResponse,
  RunnerActivitySummariesResponse,
  AgentHealthResponse,
  AgentActivityBucket,
  AgentRunCount,
  AgentFleetRank,
  AgentFleetRulesDocument,
  AgentHonorAdminAudit,
  AgentHonorDashboard,
  AgentHonorGrantRequest,
  AgentHonorRules,
  AgentHonorRulesView,
  UpdateAgentHonorShowcaseRequest,
  AgentRuntime,
  AgentRuntimeConfig,
  RuntimeAgentWorkspacesResponse,
  InboxItem,
  UserActivityListResponse,
  UserActivityTab,
  IssueSubscriber,
  Comment,
  CommentTriggerPreview,
  Reaction,
  IssueReaction,
  Workspace,
  MemberWithUser,
  MemberPresenceResponse,
  MemberProfile,
  User,
  Skill,
  SkillSummary,
  PromoteSkillRequest,
  SkillPromotionsResponse,
  PlatformSkillSummary,
  AgentMemory,
  ListAgentSkillSuggestionsResponse,
  DecideAgentSkillSuggestionRequest,
  CreateSkillRequest,
  UpdateSkillRequest,
  SetAgentSkillsRequest,
  PersonalAccessToken,
  CreatePersonalAccessTokenRequest,
  CreatePersonalAccessTokenResponse,
  RuntimeUsage,
  IssueUsageSummary,
  RuntimeHourlyActivity,
  RuntimeUsageByAgent,
  RuntimeUsageByHour,
  DashboardUsageDaily,
  DashboardUsageByAgent,
  DashboardAgentRunTime,
  DashboardRunTimeDaily,
  RuntimeRestart,
  AgentRestartMode,
  AgentRestartPreflight,
  AgentRestartOperation,
  RuntimeModelListRequest,
  RuntimeLocalSkillListRequest,
  CreateRuntimeLocalSkillImportRequest,
  RuntimeLocalSkillImportRequest,
  TimelineEntry,
  AssigneeFrequencyEntry,
  TaskMessagePayload,
  Attachment,
  ChatSession,
  ChatMessage,
  ChatMessagesPage,
  ChatPendingTask,
  MessagePart,
  PendingChatTasksResponse,
  SendChatMessageResponse,
  StickerCatalogResponse,
  Channel,
  ChannelNotifyLevel,
  ChannelMember,
  ChannelInviteCandidatesResponse,
  ChannelMentionCandidatesResponse,
  ChannelMemberManagementCapabilities,
  ChannelMessage,
  ChannelMessageQuoteInput,
  ChannelMessagesPage,
  MarkChannelReadResult,
  ChannelReaction,
  ChannelMessageSearchParams,
  ChannelMessageSearchResponse,
  ChannelThreadMessagesPage,
  ChannelStats,
  ChannelProjectFiles,
  ChannelProjectFileContent,
  ChannelGoalEnvelope,
  CreateChannelGoalRequest,
  BootstrapChannelGoalControlPlaneRequest,
  UpdateChannelGoalRequest,
  ChannelGoalProcessEnvelope,
  ChannelGoalProcessListEnvelope,
  ChannelGoalSubgoalListEnvelope,
  ChannelGoalSubgoalEnvelope,
  CreateChannelGoalSubgoalRequest,
  UpdateChannelGoalSubgoalRequest,
  ResolveChannelGoalSubgoalRequest,
  ClearChannelGoalSubgoalWaitingOnRequest,
  CancelTaskResponse,
  WorkspaceSearchResponse,
  WorkspaceSearchScope,
  Project,
  CreateProjectRequest,
  UpdateProjectRequest,
  ListProjectsResponse,
  ProjectResource,
  CreateProjectResourceRequest,
  UpdateProjectResourceRequest,
  ListProjectResourcesResponse,
  Label,
  CreateLabelRequest,
  UpdateLabelRequest,
  ListLabelsResponse,
  IssueLabelsResponse,
  PinnedItem,
  CreatePinRequest,
  PinnedItemType,
  ReorderPinsRequest,
  Invitation,
  NotificationPreferenceResponse,
  NotificationPreferences,
  WebPushPublicKeyResponse,
  WebPushSubscriptionPayload,
  WebPushSubscriptionResponse,
  WebPushTestResponse,
  GitHubPullRequest,
  ListGitHubInstallationsResponse,
  GitHubConnectResponse,
  ListLarkInstallationsResponse,
  BeginLarkInstallResponse,
  LarkInstallStatusResponse,
  RedeemLarkBindingTokenResponse,
  BillingBalance,
  BillingTransactionsPage,
  BillingBatchesPage,
  BillingTopupsPage,
  BillingPriceTier,
  CreateBillingCheckoutSessionRequest,
  CreateBillingCheckoutSessionResponse,
  BillingCheckoutSessionStatus,
  CreateBillingPortalSessionResponse,
  AgentTaskFeedPage,
  AgentTaskStats,
  IssueReviewStats,
  SandboxNode,
  SandboxBinding,
  SandboxInstance,
  SandboxJob,
  SandboxNodeDockerImagesResponse,
  SandboxNodeTemplatesResponse,
  SandboxSnapshot,
  CreateSandboxSnapshotRequest,
  CreateSandboxRequest,
  UpdateSandboxRequest,
  ProjectChannel,
  CreateVoiceCallRequest,
  CreateVoiceCallResponse,
  GetVoiceCallResponse,
  StartVoiceCallDuplexResponse,
  WorkGraphDetail,
  NotePage,
  NotePageListResponse,
  CreateNotePageRequest,
  DuplicateNotePageRequest,
  MoveNotePageRequest,
  UpdateNotePageRequest,
  UpdateNotePageSharesRequest,
  CreateNoteAIJobRequest,
  NoteAIJob,
  NotePageIssueRef,
  NotePageIssueRefListResponse,
  CreateNotePageIssueRefRequest,
  CreateNotePageAgentRefRequest,
  CreateNotePageRunRefRequest,
  CreateNotePageChannelRefRequest,
  NoteWriteback,
  NoteWritebackListResponse,
  CreateNoteWritebackRequest,
  CreateNoteRetrospectiveRequest,
  CreateNoteRetrospectiveResponse,
  CreateNotePeriodBriefRequest,
  CreateNotePeriodBriefResponse,
  NotePeriodBriefActiveResponse,
  InsertNotePeriodBriefRequest,
  InsertNotePeriodBriefResponse,
  IssueNoteRefListResponse,
} from "../types";
import type { OnboardingCompletionPath } from "../onboarding/types";
import type { DMItem, CreateOrFindDMBody } from "../dm/types";
import type { ConversationHandleLookup, ConversationListResponse } from "../conversations/types";
import type { AgentReminderListResponse } from "../agents/reminder-view-model";
import type {
  CloudRuntimeNode,
  CreateCloudRuntimeNodeRequest,
  ListCloudRuntimeNodesParams,
} from "../runtimes/cloud-runtime";
import { type Logger, noopLogger } from "../logger";
import { createRequestId } from "../utils";
import { getCurrentSlug } from "../platform/workspace-storage";
import { parseWithFallback } from "./schema";
import {
  EMPTY_AGENT_FLEET_RANK_LIST,
  agentFleetRankListSchema,
  agentFleetRankSchema,
  agentFleetRulesSchema,
} from "./agent-fleet-schemas";
import {
  agentHonorAdminAuditListSchema,
  agentHonorDashboardSchema,
  agentHonorRulesViewSchema,
  EMPTY_AGENT_HONOR_DASHBOARD,
  EMPTY_AGENT_HONOR_RULES_VIEW,
} from "./agent-honor-schemas";
import {
  honorCompareSchema,
  honorDashboardSchema,
  honorPublicWallSchema,
  honorRulesSchema,
} from "./honor-schemas";
import {
  AgentTemplateSchema,
  AgentTemplateSummaryListSchema,
  AttachmentResponseSchema,
  CancelTaskResponseSchema,
  ChildIssuesResponseSchema,
  IssueNoteRefListResponseSchema,
  EMPTY_ISSUE_NOTE_REF_LIST,
  CommentsListSchema,
  CommentTriggerPreviewSchema,
  CloudRuntimeNodeListSchema,
  CloudRuntimeNodeSchema,
  CreateAgentFromTemplateResponseSchema,
  EnsureWindyResponseSchema,
  EnsurePeriodBriefAgentResponseSchema,
  EnsureNotesAssistantAgentResponseSchema,
  EnsurePeriodBriefCollectorsResponseSchema,
  DashboardAgentRunTimeListSchema,
  DashboardRunTimeDailyListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  EMPTY_AGENT_TEMPLATE_DETAIL,
  EMPTY_AGENT_FILE_CONTENT_RESPONSE,
  EMPTY_AGENT_FILES_RESPONSE,
  EMPTY_AGENT_HEALTH_RESPONSE,
  EMPTY_AGENT_RESTART_OPERATION,
  EMPTY_AGENT_RESTART_PREFLIGHT,
  EMPTY_AGENT_RUNTIME_CONFIG,
  EMPTY_AGENT_RUNTIME_LIST,
  EMPTY_COMPUTER_CONNECTION_LIST,
  EMPTY_COMPUTER_WORK_JOURNAL_SETTING,
  EMPTY_AGENT_TEMPLATE_SUMMARY_LIST,
  EMPTY_APP_CONFIG,
  EMPTY_ATTACHMENT,
  EMPTY_CLOUD_RUNTIME_NODE,
  EMPTY_CLOUD_RUNTIME_NODE_LIST,
  EMPTY_CREATE_AGENT_FROM_TEMPLATE_RESPONSE,
  ConversationHandleLookupSchema,
  EMPTY_CHANNEL_MESSAGE_SEARCH_RESPONSE,
  EMPTY_CONVERSATION_HANDLE_LOOKUP,
  EMPTY_GROUPED_ISSUES_RESPONSE,
  EMPTY_LIST_ISSUES_RESPONSE,
  EMPTY_STICKER_CATALOG_RESPONSE,
  EMPTY_TIMELINE_ENTRIES,
  EMPTY_USER,
  EMPTY_WORKSPACE_SEARCH_RESPONSE,
  AppConfigSchema,
  AgentFileContentResponseSchema,
  AgentFilesResponseSchema,
  AgentHealthResponseSchema,
  AgentRestartOperationSchema,
  AgentRestartPreflightSchema,
  RunnerActivityResponseSchema,
  EMPTY_RUNNER_ACTIVITY_RESPONSE,
  RunnerActivitySummariesResponseSchema,
  EMPTY_RUNNER_ACTIVITY_SUMMARIES_RESPONSE,
  AgentPresenceResponseSchema,
  EMPTY_AGENT_PRESENCE_RESPONSE,
  AgentRuntimeConfigSchema,
  AgentRuntimeListSchema,
  ComputerConnectionListSchema,
  ComputerWorkJournalSettingSchema,
  ChannelMessagesPageSchema,
  ChannelThreadMessagesPageSchema,
  ChannelGoalEnvelopeSchema,
  WorkGraphDetailSchema,
  ChannelGoalProcessEnvelopeSchema,
  ChannelGoalProcessListEnvelopeSchema,
  ChannelGoalSubgoalListEnvelopeSchema,
  ChannelGoalSubgoalEnvelopeSchema,
  EMPTY_CHANNEL_GOAL_SUBGOAL_LIST,
  ChannelMessageSearchResponseSchema,
  ChannelMentionCandidatesResponseSchema,
  EMPTY_CHANNEL_MENTION_CANDIDATES,
  WorkspaceSearchResponseSchema,
  EMPTY_CHANNEL_MESSAGES_PAGE,
  EMPTY_CHANNEL_THREAD_MESSAGES_PAGE,
  StickerCatalogResponseSchema,
  type AppConfigResponse,
  GroupedIssuesResponseSchema,
  ProjectGroupedIssuesResponseSchema,
  EMPTY_PROJECT_GROUPED_ISSUES_RESPONSE,
  ListIssuesResponseSchema,
  RuntimeHourlyActivityListSchema,
  RuntimeUsageByAgentListSchema,
  RuntimeUsageByHourListSchema,
  RuntimeUsageListSchema,
  WorkspaceSchema,
  WorkspaceListSchema,
  EMPTY_WORKSPACE,
  RuntimeAgentWorkspacesResponseSchema,
  EMPTY_RUNTIME_AGENT_WORKSPACES_RESPONSE,
  SubscribersListSchema,
  TimelineEntriesSchema,
  UserSchema,
  BillingBalanceSchema,
  BillingTransactionsPageSchema,
  BillingBatchesPageSchema,
  BillingTopupsPageSchema,
  BillingPriceTierListSchema,
  CreateBillingCheckoutSessionResponseSchema,
  BillingCheckoutSessionStatusSchema,
  CreateBillingPortalSessionResponseSchema,
  EMPTY_BILLING_BALANCE,
  EMPTY_BILLING_TRANSACTIONS_PAGE,
  EMPTY_BILLING_BATCHES_PAGE,
  EMPTY_BILLING_TOPUPS_PAGE,
  EMPTY_BILLING_PRICE_TIER_LIST,
  EMPTY_CREATE_BILLING_CHECKOUT_SESSION_RESPONSE,
  EMPTY_BILLING_CHECKOUT_SESSION_STATUS,
  EMPTY_CREATE_BILLING_PORTAL_SESSION_RESPONSE,
  EMPTY_SANDBOX_NODE_TEMPLATES_RESPONSE,
  EMPTY_SANDBOX_SNAPSHOT,
  EMPTY_CANCEL_TASK_RESPONSE,
  EMPTY_SEND_CHAT_MESSAGE_RESPONSE,
  SendChatMessageResponseSchema,
  ChatPendingTaskSchema,
  EMPTY_CHAT_PENDING_TASK,
  PendingChatTasksResponseSchema,
  EMPTY_PENDING_CHAT_TASKS_RESPONSE,
  EMPTY_EVOLUTION_METRICS,
  EMPTY_EVOLUTION_TRAINING_EXAMPLE_LIST,
  EMPTY_EVOLUTION_MODEL_RUNTIME_CONFIG_LIST,
  EMPTY_EVOLUTION_MODEL_EVAL_RUN_LIST,
  EMPTY_EVOLUTION_REVIEW_SUBMISSION_LIST,
  EMPTY_UPDATE_AGENT_FILE_CONTENT_RESPONSE,
  EMPTY_WORKSPACE_MEMORY_CURATION_STATUS,
  EMPTY_MEMORY_CURATION_RUN_DETAIL,
  EMPTY_MEMORY_CURATOR_PROFILE,
  EMPTY_GRAPH_MEMORY_PROFILE,
  GraphMemoryStatusSchema,
  EMPTY_GRAPH_MEMORY_STATUS,
  GraphMemoryAuditSummarySchema,
  EMPTY_GRAPH_MEMORY_AUDIT,
  GraphMemoryChannelLineageSchema,
  GraphMemoryConsolidationRunSchema,
  GraphMemoryConsolidationListSchema,
  EMPTY_GRAPH_MEMORY_CONSOLIDATION_RUN,
  EvolutionMetricsSchema,
  EvolutionTrainingExampleListSchema,
  EvolutionTrainingExampleSchema,
  EvolutionModelRuntimeConfigListSchema,
  EvolutionModelRuntimeConfigSchema,
  EvolutionModelEvalRunListSchema,
  EvolutionModelEvalRunSchema,
  EvolutionReviewSubmissionListSchema,
  EvolutionReviewSubmissionSchema,
  UpdateAgentFileContentResponseSchema,
  WorkspaceMemoryCurationStatusSchema,
  MemoryCurationRunDetailSchema,
  MemoryCuratorProfileSchema,
  GraphMemoryProfileSchema,
  GraphMemoryChannelModeSchema,
  GraphMemoryMessageCitationsSchema,
  StartMemoryCurationRunResponseSchema,
  MemoryCurationBackfillResponseSchema,
  EMPTY_MEMORY_CURATION_BACKFILL_RESPONSE,
  MemoryCurationDailySummaryResponseSchema,
  EMPTY_MEMORY_CURATION_DAILY_SUMMARY,
  MemoryCurationCandidateItemSchema,
  MemoryCurationCandidateListResponseSchema,
  EMPTY_MEMORY_CURATION_CANDIDATE_LIST,
  TeamKnowledgeListItemSchema,
  TeamKnowledgeListResponseSchema,
  EMPTY_TEAM_KNOWLEDGE_LIST,
  KnowledgeNeighborsResponseSchema,
  EMPTY_KNOWLEDGE_NEIGHBORS,
  PromoteKnowledgeResponseSchema,
  EMPTY_SANDBOX_NODE_DOCKER_IMAGES_RESPONSE,
  SandboxNodeDockerImagesResponseSchema,
  SandboxNodeTemplatesResponseSchema,
  SandboxSnapshotSchema,
  SandboxSnapshotListSchema,
  VoiceTranscriptResponseSchema,
  EMPTY_VOICE_TRANSCRIPT_RESPONSE,
  CreateVoiceCallResponseSchema,
  GetVoiceCallResponseSchema,
  StartVoiceCallDuplexResponseSchema,
  EMPTY_CREATE_VOICE_CALL_RESPONSE,
  EMPTY_GET_VOICE_CALL_RESPONSE,
  EMPTY_START_VOICE_CALL_DUPLEX_RESPONSE,
  AgentReminderListResponseSchema,
  EMPTY_AGENT_REMINDER_LIST,
  EMPTY_WEB_PUSH_PUBLIC_KEY,
  EMPTY_WEB_PUSH_SUBSCRIPTION,
  EMPTY_WEB_PUSH_TEST,
  WebPushPublicKeySchema,
  WebPushSubscriptionSchema,
  WebPushTestSchema,
  DeleteComputerResponseSchema,
  EMPTY_DELETE_COMPUTER_RESPONSE,
  type DeleteComputerResponse,
  NotePageSchema,
  NotePageListResponseSchema,
  NoteShareUnreadCountSchema,
  EMPTY_NOTE_PAGE,
  EMPTY_NOTE_PAGE_LIST,
  EMPTY_NOTE_SHARE_UNREAD_COUNT,
  NoteAIJobSchema,
  EMPTY_NOTE_AI_JOB,
  NotePageIssueRefSchema,
  NotePageIssueRefListResponseSchema,
  EMPTY_NOTE_PAGE_ISSUE_REF,
  EMPTY_NOTE_PAGE_ISSUE_REF_LIST,
  NoteWritebackSchema,
  NoteWritebackListResponseSchema,
  EMPTY_NOTE_WRITEBACK,
  EMPTY_NOTE_WRITEBACK_LIST,
  CreateNoteRetrospectiveResponseSchema,
  EMPTY_CREATE_NOTE_RETROSPECTIVE_RESPONSE,
  CreateNotePeriodBriefResponseSchema,
  EMPTY_CREATE_NOTE_PERIOD_BRIEF_RESPONSE,
  NotePeriodBriefActiveResponseSchema,
  EMPTY_NOTE_PERIOD_BRIEF_ACTIVE,
  InsertNotePeriodBriefResponseSchema,
  EMPTY_INSERT_NOTE_PERIOD_BRIEF_RESPONSE,
} from "./schemas";

/** Identifies the calling client to the server.
 *  Sent on every HTTP request as X-Client-Platform / X-Client-Version /
 *  X-Client-OS so the backend can log, gate, or split metrics by client.
 *  See server/internal/middleware/client.go for the receiving end. */
export interface ApiClientIdentity {
  /** Logical client kind. Server expects: "web" | "desktop" | "cli" | "daemon". */
  platform?: string;
  /** Client/app version string (e.g. "0.1.0", git tag, commit). */
  version?: string;
  /** Operating system the client is running on: "macos" | "windows" | "linux". */
  os?: string;
}

export interface ApiClientOptions {
  logger?: Logger;
  onUnauthorized?: () => void;
  /** Identifies the client to the server. Sent as X-Client-* headers. */
  identity?: ApiClientIdentity;
}

export interface LoginResponse {
  token: string;
  user: User;
}

/** RFC 8628 device-code confirmation (task #36). */
export interface DevicePending {
  /** Human-readable hint the CLI sent when it created the code (e.g. hostname). May be empty. */
  client_hint: string;
  /** RFC3339 timestamp of when the CLI requested this code. */
  created_at: string;
}

export interface DeviceConfirmResponse {
  status: "approved" | "denied";
}

export class ApiError extends Error {
  readonly status: number;
  readonly statusText: string;
  // Raw decoded JSON body (when the server returned one). Carries structured
  // error fields like `code` so callers can branch on machine-readable
  // identifiers instead of pattern-matching the human-readable message.
  readonly body?: unknown;

  constructor(message: string, status: number, statusText: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
    this.body = body;
  }
}

// Thrown by getAttachmentTextContent when the server refuses to inline a
// file because it exceeds the 2 MB cap. UI maps to a "too large, please
// download" affordance with the Download CTA still available.
export class PreviewTooLargeError extends Error {
  constructor() {
    super("attachment too large for inline preview");
    this.name = "PreviewTooLargeError";
  }
}

// Thrown by getAttachmentTextContent when the server's text whitelist
// rejects the content type. Normally the client's isPreviewable() guard
// catches this earlier, but the two whitelists can drift — surfacing the
// 415 as a typed error makes the drift visible.
export class PreviewUnsupportedError extends Error {
  constructor() {
    super("attachment type not supported for inline preview");
    this.name = "PreviewUnsupportedError";
  }
}

// Composer sends must never hang forever: a stalled fetch (network stall,
// held-open connection, stuck backend) is aborted after this window so the
// composer lock releases and a visible retry error shows instead of an endless
// spinner. Aborting an already-landed send is harmless — retries dedupe via
// client_message_id. Per-request on purpose: uploads / long queries keep their
// own (or no) timeout (#294).
//
// #1276: 30s → 8s. This is the ONLY failure signal for the most common real
// case — online at click, network drops mid-flight (subway / elevator / WiFi
// switch) — where `navigator.onLine` is still true at dispatch, so nothing else
// surfaces the failure until this aborts. 8s gives fast, visible feedback (INV-2)
// and puts the terminal state inside a human/QA observation window. Safe to
// lower ONLY because #1276 INV-1 now clears the draft on commit (not dispatch):
// a mis-fired timeout costs one extra retry tap, no longer lost text (a message
// should send in ~1s anyway; 8s stays tolerant of slow networks).
const SEND_TIMEOUT_MS = 8_000;

/**
 * Forward a caller's `AbortSignal` (React Query hands one to every queryFn) into
 * a fetch init, or nothing at all when absent.
 *
 * LRM-1296: this is not cosmetic. React Query only aborts an in-flight fetch on
 * last-observer-unsubscribe when the queryFn *consumed* the signal it was given;
 * a read that ignores it keeps running after the user switched away, holding one
 * of the ~6 per-origin connection slots that the newly-opened conversation's
 * message page needs to paint.
 */
function abortInit(options?: { signal?: AbortSignal }): RequestInit | undefined {
  return options?.signal ? { signal: options.signal } : undefined;
}

/** Build a same-origin Duplex WebSocket URL from an HTTP API base URL. */
export function voiceCallDuplexWsUrl(
  baseUrl: string,
  workspaceId: string,
  callId: string,
): string {
  const wsBase = baseUrl.replace(/^http/i, "ws").replace(/\/$/, "");
  return `${wsBase}/api/workspaces/${encodeURIComponent(workspaceId)}/voice-calls/${encodeURIComponent(callId)}/duplex/ws`;
}

/** Resolve a server-provided ws_path against an HTTP API base URL. */
export function voiceCallDuplexWsUrlFromPath(
  baseUrl: string,
  wsPath: string,
): string {
  const wsBase = baseUrl.replace(/^http/i, "ws").replace(/\/$/, "");
  const path = wsPath.startsWith("/") ? wsPath : `/${wsPath}`;
  return `${wsBase}${path}`;
}

export class ApiClient {
  private baseUrl: string;
  private token: string | null = null;
  private logger: Logger;
  private options: ApiClientOptions;

  constructor(baseUrl: string, options?: ApiClientOptions) {
    this.baseUrl = baseUrl;
    this.options = options ?? {};
    this.logger = options?.logger ?? noopLogger;
  }

  getBaseUrl(): string {
    return this.baseUrl;
  }

  setToken(token: string | null) {
    this.token = token;
  }

  private readCsrfToken(): string | null {
    if (typeof document === "undefined") return null;
    const match = document.cookie
      .split("; ")
      .find((c) => c.startsWith("multica_csrf="));
    return match ? match.split("=")[1] ?? null : null;
  }

  private authHeaders(): Record<string, string> {
    const headers: Record<string, string> = {};
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    const slug = getCurrentSlug();
    if (slug) headers["X-Workspace-Slug"] = slug;
    const csrf = this.readCsrfToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;
    const id = this.options.identity;
    if (id?.platform) headers["X-Client-Platform"] = id.platform;
    if (id?.version) headers["X-Client-Version"] = id.version;
    if (id?.os) headers["X-Client-OS"] = id.os;
    return headers;
  }

  private handleUnauthorized() {
    this.token = null;
    // Workspace id is owned by the URL-driven workspace-storage singleton
    // (set by [workspaceSlug]/layout.tsx). On 401, the auth flow navigates
    // to /login which leaves the workspace route, and the next workspace
    // entry will overwrite the id. No clear needed here.
    this.options.onUnauthorized?.();
  }

  private async parseErrorMessage(res: Response, fallback: string): Promise<string> {
    try {
      const data = await res.json() as { error?: string };
      if (typeof data.error === "string" && data.error) return data.error;
    } catch {
      // Ignore non-JSON error bodies.
    }
    return fallback;
  }

  // Reads the response body once for both human-readable error message and
  // structured fields. The Response stream can only be consumed once, so
  // both pieces have to come from a single read.
  private async parseErrorBody(res: Response, fallback: string): Promise<{ message: string; body: unknown }> {
    try {
      const data = await res.json() as { error?: string };
      const message = typeof data.error === "string" && data.error ? data.error : fallback;
      return { message, body: data };
    } catch {
      return { message: fallback, body: undefined };
    }
  }

  // Sends the request with the standard headers (auth, CSRF, request id,
  // client identity) and runs the shared error path (401 → handleUnauthorized,
  // structured ApiError, status-aware log level). Returns the raw Response so
  // callers can decide how to decode the body — JSON for the typed `fetch<T>`
  // path, plain text for the attachment-preview proxy, etc.
  private async fetchRaw(
    path: string,
    init?: RequestInit & { extraHeaders?: Record<string, string> },
  ): Promise<Response> {
    const rid = createRequestId();
    const start = Date.now();
    const method = init?.method ?? "GET";

    const headers: Record<string, string> = {
      "X-Request-ID": rid,
      ...this.authHeaders(),
      ...(init?.extraHeaders ?? {}),
      ...((init?.headers as Record<string, string>) ?? {}),
    };

    this.logger.info(`→ ${method} ${path}`, { rid });

    const res = await fetch(`${this.baseUrl}${path}`, {
      ...init,
      headers,
      credentials: "include",
    });

    if (!res.ok) {
      if (res.status === 401) this.handleUnauthorized();
      const { message, body } = await this.parseErrorBody(res, `API error: ${res.status} ${res.statusText}`);
      const logLevel = res.status === 404 ? "warn" : "error";
      this.logger[logLevel](`← ${res.status} ${path}`, { rid, duration: `${Date.now() - start}ms`, error: message });
      throw new ApiError(message, res.status, res.statusText, body);
    }

    this.logger.info(`← ${res.status} ${path}`, { rid, duration: `${Date.now() - start}ms` });
    return res;
  }

  private async fetch<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await this.fetchRaw(path, {
      ...init,
      extraHeaders: { "Content-Type": "application/json" },
    });
    // Handle 204 No Content
    if (res.status === 204) {
      return undefined as T;
    }
    return res.json() as Promise<T>;
  }

  // Auth
  async sendCode(email: string): Promise<void> {
    await this.fetch("/auth/send-code", {
      method: "POST",
      body: JSON.stringify({ email }),
    });
  }

  async verifyCode(email: string, code: string): Promise<LoginResponse> {
    return this.fetch("/auth/verify-code", {
      method: "POST",
      body: JSON.stringify({ email, code }),
    });
  }

  async googleLogin(code: string, redirectUri: string): Promise<LoginResponse> {
    return this.fetch("/auth/google", {
      method: "POST",
      body: JSON.stringify({ code, redirect_uri: redirectUri }),
    });
  }

  async logout(): Promise<void> {
    await this.fetch("/auth/logout", { method: "POST" });
  }

  // Device-code login confirmation (task #36, RFC 8628 §3.3).
  async getDevicePending(userCode: string): Promise<DevicePending> {
    const search = new URLSearchParams({ user_code: userCode });
    return this.fetch(`/api/device/pending?${search}`);
  }

  async confirmDevice(userCode: string, approve: boolean): Promise<DeviceConfirmResponse> {
    return this.fetch("/api/device/confirm", {
      method: "POST",
      body: JSON.stringify({ user_code: userCode, approve }),
    });
  }

  async issueCliToken(): Promise<{ token: string }> {
    return this.fetch("/api/cli-token", { method: "POST" });
  }

  async getMe(): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me");
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "GET /api/me",
    });
  }

  async getHonorRules(): Promise<import("../types/honor").HonorRulesDocument> {
    const raw = await this.fetch<unknown>("/api/honor/rules");
    return parseWithFallback(
      raw,
      honorRulesSchema,
      { version: "", founding_cutoff: "", level_thresholds: [], pillar_tier_tables: {}, action_rules: {}, name_style_unlocks: [], badge_catalog: [], changelog: [] },
      { endpoint: "GET /api/honor/rules" },
    );
  }

  async getMyHonor(): Promise<import("../types/honor").HonorDashboard> {
    const raw = await this.fetch<unknown>("/api/me/honor");
    return parseWithFallback(
      raw,
      honorDashboardSchema,
      {
        level: 1,
        total_xp: 0,
        xp_to_next_level: 0,
        name_style: "default",
        equipped_badge_id: null,
        equipped_badge_manual: false,
        pillars: [],
        unlocked_badges: [],
        unlocked_styles: [],
        recent_xp: [],
      },
      { endpoint: "GET /api/me/honor" },
    );
  }

  async updateMyHonor(data: {
    equipped_badge_id?: string;
    showcase_badge_ids?: string[];
  }): Promise<import("../types/honor").HonorDashboard> {
    const raw = await this.fetch<unknown>("/api/me/honor", {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(
      raw,
      honorDashboardSchema,
      {
        level: 1,
        total_xp: 0,
        xp_to_next_level: 0,
        name_style: "default",
        equipped_badge_id: data.equipped_badge_id ?? null,
        equipped_badge_manual: data.equipped_badge_id != null && data.equipped_badge_id !== "",
        pillars: [],
        unlocked_badges: [],
        unlocked_styles: [],
        recent_xp: [],
      },
      { endpoint: "PATCH /api/me/honor" },
    );
  }

  async compareHonor(withUserId: string): Promise<import("../types/honor").HonorCompareResult> {
    const raw = await this.fetch<unknown>(
      `/api/me/honor/compare?with=${encodeURIComponent(withUserId)}`,
    );
    return parseWithFallback(
      raw,
      honorCompareSchema,
      {
        self: { user_id: "", level: 1, unlocked_count: 0, total_badges: 0 },
        other: { user_id: withUserId, level: 1, unlocked_count: 0, total_badges: 0 },
        shared_badges: [],
        self_only_badges: [],
        other_only_badges: [],
      },
      { endpoint: "GET /api/me/honor/compare" },
    );
  }

  async postHonorPresence(): Promise<void> {
    await this.fetch("/api/me/honor/presence", { method: "POST" });
  }

  async getUserHonor(userId: string): Promise<import("../types/honor").HonorPublicWall> {
    const raw = await this.fetch<unknown>(`/api/users/${userId}/honor`);
    return parseWithFallback(
      raw,
      honorPublicWallSchema,
      { level: 1, name_style: "default", unlocked_badges: [] },
      { endpoint: "GET /api/users/:id/honor" },
    );
  }

  async markOnboardingComplete(payload?: {
    completion_path?: OnboardingCompletionPath;
    workspace_id?: string;
  }): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me/onboarding/complete", {
      method: "POST",
      body: payload ? JSON.stringify(payload) : undefined,
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "POST /api/me/onboarding/complete",
    });
  }

  async joinCloudWaitlist(payload: {
    email: string;
    reason?: string;
  }): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me/onboarding/cloud-waitlist", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "POST /api/me/onboarding/cloud-waitlist",
    });
  }

  async patchOnboarding(payload: {
    questionnaire?: Record<string, unknown>;
  }): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me/onboarding", {
      method: "PATCH",
      body: JSON.stringify(payload),
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "PATCH /api/me/onboarding",
    });
  }

  async updateMe(data: UpdateMeRequest): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me", {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "PATCH /api/me",
    });
  }

  // Issues
  async listIssues(params?: ListIssuesParams): Promise<ListIssuesResponse> {
    const search = new URLSearchParams();
    if (params?.limit) search.set("limit", String(params.limit));
    if (params?.offset) search.set("offset", String(params.offset));
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.status) search.set("status", params.status);
    if (params?.priority) search.set("priority", params.priority);
    if (params?.assignee_id) search.set("assignee_id", params.assignee_id);
    if (params?.assignee_ids?.length) search.set("assignee_ids", params.assignee_ids.join(","));
    if (params?.creator_id) search.set("creator_id", params.creator_id);
    if (params?.project_id) search.set("project_id", params.project_id);
    if (params?.source_channel_id) search.set("source_channel_id", params.source_channel_id);
    if (params?.involves_user_id) search.set("involves_user_id", params.involves_user_id);
    if (params?.metadata && Object.keys(params.metadata).length > 0) {
      search.set("metadata", JSON.stringify(params.metadata));
    }
    if (params?.open_only) search.set("open_only", "true");
    if (params?.scheduled) search.set("scheduled", "true");
    if (params?.sort_by) search.set("sort", params.sort_by);
    if (params?.sort_direction) search.set("direction", params.sort_direction);
    const path = `/api/issues?${search}`;
    const raw = await this.fetch<unknown>(path);
    return parseWithFallback(raw, ListIssuesResponseSchema, EMPTY_LIST_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues",
    });
  }

  async listGroupedIssues(params: ListGroupedIssuesParams & { group_by: "assignee" }): Promise<GroupedIssuesResponse>;
  async listGroupedIssues(params: ListGroupedIssuesParams & { group_by: "project" }): Promise<ProjectGroupedIssuesResponse>;
  async listGroupedIssues(params: ListGroupedIssuesParams): Promise<GroupedIssuesResponse | ProjectGroupedIssuesResponse>;
  async listGroupedIssues(params: ListGroupedIssuesParams): Promise<GroupedIssuesResponse | ProjectGroupedIssuesResponse> {
    const search = new URLSearchParams({ group_by: params.group_by });
    if (params.limit) search.set("limit", String(params.limit));
    if (params.offset) search.set("offset", String(params.offset));
    if (params.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params.statuses?.length) search.set("statuses", params.statuses.join(","));
    if (params.priorities?.length) search.set("priorities", params.priorities.join(","));
    if (params.assignee_types?.length) search.set("assignee_types", params.assignee_types.join(","));
    if (params.assignee_id) search.set("assignee_id", params.assignee_id);
    if (params.assignee_ids?.length) search.set("assignee_ids", params.assignee_ids.join(","));
    if (params.creator_id) search.set("creator_id", params.creator_id);
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.involves_user_id) search.set("involves_user_id", params.involves_user_id);
    if (params.metadata && Object.keys(params.metadata).length > 0) {
      search.set("metadata", JSON.stringify(params.metadata));
    }
    if (params.assignee_filters?.length) {
      search.set("assignee_filters", params.assignee_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params.include_no_assignee) search.set("include_no_assignee", "true");
    if (params.creator_filters?.length) {
      search.set("creator_filters", params.creator_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params.project_ids?.length) search.set("project_ids", params.project_ids.join(","));
    if (params.include_no_project) search.set("include_no_project", "true");
    if (params.label_ids?.length) search.set("label_ids", params.label_ids.join(","));
    if (params.group_assignee_type) search.set("group_assignee_type", params.group_assignee_type);
    if (params.group_assignee_id) search.set("group_assignee_id", params.group_assignee_id);
    if (params.group_project_id) search.set("group_project_id", params.group_project_id);
    if (params.sort_by) search.set("sort", params.sort_by);
    if (params.sort_direction) search.set("direction", params.sort_direction);
    const raw = await this.fetch<unknown>(`/api/issues/grouped?${search}`);
    if (params.group_by === "project") {
      return parseWithFallback(raw, ProjectGroupedIssuesResponseSchema, EMPTY_PROJECT_GROUPED_ISSUES_RESPONSE, {
        endpoint: "GET /api/issues/grouped?group_by=project",
      });
    }
    return parseWithFallback(raw, GroupedIssuesResponseSchema, EMPTY_GROUPED_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues/grouped",
    });
  }

  async searchIssues(params: { q: string; limit?: number; offset?: number; include_closed?: boolean; signal?: AbortSignal }): Promise<SearchIssuesResponse> {
    const search = new URLSearchParams({ q: params.q });
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.offset !== undefined) search.set("offset", String(params.offset));
    if (params.include_closed) search.set("include_closed", "true");
    return this.fetch(`/api/issues/search?${search}`, params.signal ? { signal: params.signal } : undefined);
  }

  async searchProjects(params: { q: string; limit?: number; offset?: number; include_closed?: boolean; signal?: AbortSignal }): Promise<SearchProjectsResponse> {
    const search = new URLSearchParams({ q: params.q });
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.offset !== undefined) search.set("offset", String(params.offset));
    if (params.include_closed) search.set("include_closed", "true");
    return this.fetch(`/api/projects/search?${search}`, params.signal ? { signal: params.signal } : undefined);
  }

  async listNotePages(): Promise<NotePageListResponse> {
    const raw = await this.fetch<unknown>("/api/notes/pages");
    return parseWithFallback(raw, NotePageListResponseSchema, EMPTY_NOTE_PAGE_LIST, {
      endpoint: "GET /api/notes/pages",
    });
  }

  async countNoteShareUnread(): Promise<{ count: number }> {
    const raw = await this.fetch<unknown>("/api/notes/share-unread-count");
    return parseWithFallback(raw, NoteShareUnreadCountSchema, EMPTY_NOTE_SHARE_UNREAD_COUNT, {
      endpoint: "GET /api/notes/share-unread-count",
    });
  }

  async listDeletedNotePages(): Promise<NotePageListResponse> {
    const raw = await this.fetch<unknown>("/api/notes/pages/trash");
    return parseWithFallback(raw, NotePageListResponseSchema, EMPTY_NOTE_PAGE_LIST, {
      endpoint: "GET /api/notes/pages/trash",
    });
  }

  async createNotePage(data: CreateNotePageRequest): Promise<NotePage> {
    const raw = await this.fetch<unknown>("/api/notes/pages", {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageSchema, EMPTY_NOTE_PAGE, {
      endpoint: "POST /api/notes/pages",
    });
  }

  async getNotePage(id: string): Promise<NotePage> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(id)}`);
    return parseWithFallback(raw, NotePageSchema, EMPTY_NOTE_PAGE, {
      endpoint: "GET /api/notes/pages/{id}",
    });
  }

  async updateNotePage(id: string, data: UpdateNotePageRequest): Promise<NotePage> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageSchema, EMPTY_NOTE_PAGE, {
      endpoint: "PATCH /api/notes/pages/{id}",
    });
  }

  async moveNotePage(id: string, data: MoveNotePageRequest): Promise<NotePage> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(id)}/move`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageSchema, EMPTY_NOTE_PAGE, {
      endpoint: "PATCH /api/notes/pages/{id}/move",
    });
  }

  async duplicateNotePage(id: string, data: DuplicateNotePageRequest = {}): Promise<NotePageListResponse> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(id)}/duplicate`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageListResponseSchema, EMPTY_NOTE_PAGE_LIST, {
      endpoint: "POST /api/notes/pages/{id}/duplicate",
    });
  }

  async updateNotePageShares(id: string, data: UpdateNotePageSharesRequest): Promise<NotePage> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(id)}/shares`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageSchema, EMPTY_NOTE_PAGE, {
      endpoint: "PUT /api/notes/pages/{id}/shares",
    });
  }

  async deleteNotePage(id: string): Promise<void> {
    await this.fetch(`/api/notes/pages/${encodeURIComponent(id)}`, { method: "DELETE" });
  }

  async permanentlyDeleteNotePage(id: string): Promise<void> {
    await this.fetch(`/api/notes/pages/${encodeURIComponent(id)}/permanent`, { method: "DELETE" });
  }

  async emptyNoteTrash(): Promise<void> {
    await this.fetch("/api/notes/pages/trash", { method: "DELETE" });
  }

  async restoreNotePage(id: string): Promise<NotePage> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(id)}/restore`, { method: "POST" });
    return parseWithFallback(raw, NotePageSchema, EMPTY_NOTE_PAGE, {
      endpoint: "POST /api/notes/pages/{id}/restore",
    });
  }

  async createNoteAIJob(pageId: string, data: CreateNoteAIJobRequest, init?: { signal?: AbortSignal }): Promise<NoteAIJob> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/ai-jobs`, {
      method: "POST",
      body: JSON.stringify(data),
      signal: init?.signal,
    });
    return parseWithFallback(raw, NoteAIJobSchema, EMPTY_NOTE_AI_JOB, {
      endpoint: "POST /api/notes/pages/{id}/ai-jobs",
    });
  }

  async getNoteAIJob(jobId: string, init?: { signal?: AbortSignal }): Promise<NoteAIJob> {
    const raw = await this.fetch<unknown>(`/api/notes/ai-jobs/${encodeURIComponent(jobId)}`, init?.signal ? { signal: init.signal } : undefined);
    return parseWithFallback(raw, NoteAIJobSchema, EMPTY_NOTE_AI_JOB, {
      endpoint: "GET /api/notes/ai-jobs/{id}",
    });
  }

  async cancelNoteAIJob(jobId: string): Promise<NoteAIJob> {
    const raw = await this.fetch<unknown>(`/api/notes/ai-jobs/${encodeURIComponent(jobId)}/cancel`, { method: "POST" });
    return parseWithFallback(raw, NoteAIJobSchema, EMPTY_NOTE_AI_JOB, {
      endpoint: "POST /api/notes/ai-jobs/{id}/cancel",
    });
  }

  async listNotePageIssueRefs(pageId: string): Promise<NotePageIssueRefListResponse> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/issue-refs`);
    return parseWithFallback(raw, NotePageIssueRefListResponseSchema, EMPTY_NOTE_PAGE_ISSUE_REF_LIST, {
      endpoint: "GET /api/notes/pages/{id}/issue-refs",
    });
  }

  async createNotePageIssueRef(pageId: string, data: CreateNotePageIssueRefRequest): Promise<NotePageIssueRef> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/issue-refs`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageIssueRefSchema, EMPTY_NOTE_PAGE_ISSUE_REF, {
      endpoint: "POST /api/notes/pages/{id}/issue-refs",
    });
  }

  async deleteNotePageIssueRef(pageId: string, issueId: string): Promise<void> {
    await this.fetch(`/api/notes/pages/${encodeURIComponent(pageId)}/issue-refs/${encodeURIComponent(issueId)}`, {
      method: "DELETE",
    });
  }

  async listNotePageAgentRefs(pageId: string): Promise<NotePageIssueRefListResponse> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/agent-refs`);
    return parseWithFallback(raw, NotePageIssueRefListResponseSchema, EMPTY_NOTE_PAGE_ISSUE_REF_LIST, {
      endpoint: "GET /api/notes/pages/{id}/agent-refs",
    });
  }

  async createNotePageAgentRef(pageId: string, data: CreateNotePageAgentRefRequest): Promise<NotePageIssueRef> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/agent-refs`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageIssueRefSchema, EMPTY_NOTE_PAGE_ISSUE_REF, {
      endpoint: "POST /api/notes/pages/{id}/agent-refs",
    });
  }

  async deleteNotePageAgentRef(pageId: string, agentId: string): Promise<void> {
    await this.fetch(`/api/notes/pages/${encodeURIComponent(pageId)}/agent-refs/${encodeURIComponent(agentId)}`, {
      method: "DELETE",
    });
  }

  async listNotePageRunRefs(pageId: string): Promise<NotePageIssueRefListResponse> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/run-refs`);
    return parseWithFallback(raw, NotePageIssueRefListResponseSchema, EMPTY_NOTE_PAGE_ISSUE_REF_LIST, {
      endpoint: "GET /api/notes/pages/{id}/run-refs",
    });
  }

  async createNotePageRunRef(pageId: string, data: CreateNotePageRunRefRequest): Promise<NotePageIssueRef> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/run-refs`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageIssueRefSchema, EMPTY_NOTE_PAGE_ISSUE_REF, {
      endpoint: "POST /api/notes/pages/{id}/run-refs",
    });
  }

  async deleteNotePageRunRef(pageId: string, runId: string): Promise<void> {
    await this.fetch(`/api/notes/pages/${encodeURIComponent(pageId)}/run-refs/${encodeURIComponent(runId)}`, {
      method: "DELETE",
    });
  }

  async listNotePageChannelRefs(pageId: string): Promise<NotePageIssueRefListResponse> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/channel-refs`);
    return parseWithFallback(raw, NotePageIssueRefListResponseSchema, EMPTY_NOTE_PAGE_ISSUE_REF_LIST, {
      endpoint: "GET /api/notes/pages/{id}/channel-refs",
    });
  }

  async createNotePageChannelRef(pageId: string, data: CreateNotePageChannelRefRequest): Promise<NotePageIssueRef> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/channel-refs`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NotePageIssueRefSchema, EMPTY_NOTE_PAGE_ISSUE_REF, {
      endpoint: "POST /api/notes/pages/{id}/channel-refs",
    });
  }

  async deleteNotePageChannelRef(pageId: string, channelId: string): Promise<void> {
    await this.fetch(`/api/notes/pages/${encodeURIComponent(pageId)}/channel-refs/${encodeURIComponent(channelId)}`, {
      method: "DELETE",
    });
  }

  async createNoteRetrospective(data: CreateNoteRetrospectiveRequest): Promise<CreateNoteRetrospectiveResponse> {
    const raw = await this.fetch<unknown>("/api/notes/retrospectives", {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, CreateNoteRetrospectiveResponseSchema, EMPTY_CREATE_NOTE_RETROSPECTIVE_RESPONSE, {
      endpoint: "POST /api/notes/retrospectives",
    });
  }

  async getActiveNotePeriodBrief(pageId: string): Promise<NotePeriodBriefActiveResponse> {
    const raw = await this.fetch<unknown>(
      `/api/notes/period-briefs/active?page_id=${encodeURIComponent(pageId)}`,
    );
    return parseWithFallback(raw, NotePeriodBriefActiveResponseSchema, EMPTY_NOTE_PERIOD_BRIEF_ACTIVE, {
      endpoint: "GET /api/notes/period-briefs/active",
    });
  }

  async insertNotePeriodBrief(
    runId: string,
    data: InsertNotePeriodBriefRequest,
  ): Promise<InsertNotePeriodBriefResponse> {
    const raw = await this.fetch<unknown>(`/api/notes/period-briefs/${runId}/insert`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, InsertNotePeriodBriefResponseSchema, EMPTY_INSERT_NOTE_PERIOD_BRIEF_RESPONSE, {
      endpoint: "POST /api/notes/period-briefs/{runId}/insert",
    });
  }

  async createNotePeriodBrief(data: CreateNotePeriodBriefRequest): Promise<CreateNotePeriodBriefResponse> {
    // Collectors finish in the background; this call only dispatches and
    // returns quickly. Keep a modest timeout for proxy/network stalls.
    const raw = await this.fetch<unknown>("/api/notes/period-briefs", {
      method: "POST",
      body: JSON.stringify(data),
      signal: AbortSignal.timeout(60_000),
    });
    return parseWithFallback(raw, CreateNotePeriodBriefResponseSchema, EMPTY_CREATE_NOTE_PERIOD_BRIEF_RESPONSE, {
      endpoint: "POST /api/notes/period-briefs",
    });
  }

  async listNotePageWritebacks(pageId: string, status?: string): Promise<NoteWritebackListResponse> {
    const query = status ? `?status=${encodeURIComponent(status)}` : "";
    const raw = await this.fetch<unknown>(
      `/api/notes/pages/${encodeURIComponent(pageId)}/writebacks${query}`,
    );
    return parseWithFallback(raw, NoteWritebackListResponseSchema, EMPTY_NOTE_WRITEBACK_LIST, {
      endpoint: "GET /api/notes/pages/{id}/writebacks",
    });
  }

  async createNotePageWriteback(pageId: string, data: CreateNoteWritebackRequest): Promise<NoteWriteback> {
    const raw = await this.fetch<unknown>(`/api/notes/pages/${encodeURIComponent(pageId)}/writebacks`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, NoteWritebackSchema, EMPTY_NOTE_WRITEBACK, {
      endpoint: "POST /api/notes/pages/{id}/writebacks",
    });
  }

  async acceptNotePageWriteback(writebackId: string): Promise<NoteWriteback> {
    const raw = await this.fetch<unknown>(
      `/api/notes/writebacks/${encodeURIComponent(writebackId)}/accept`,
      { method: "POST" },
    );
    return parseWithFallback(raw, NoteWritebackSchema, EMPTY_NOTE_WRITEBACK, {
      endpoint: "POST /api/notes/writebacks/{id}/accept",
    });
  }

  async rejectNotePageWriteback(writebackId: string): Promise<NoteWriteback> {
    const raw = await this.fetch<unknown>(
      `/api/notes/writebacks/${encodeURIComponent(writebackId)}/reject`,
      { method: "POST" },
    );
    return parseWithFallback(raw, NoteWritebackSchema, EMPTY_NOTE_WRITEBACK, {
      endpoint: "POST /api/notes/writebacks/{id}/reject",
    });
  }

  async getIssue(id: string): Promise<Issue> {
    return this.fetch(`/api/issues/${id}`);
  }

  async createIssue(data: CreateIssueRequest): Promise<Issue> {
    return this.fetch("/api/issues", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async quickCreateIssue(data: {
    agent_id?: string;
    prompt: string;
    project_id?: string | null;
    parent_issue_id?: string | null;
    attachment_ids?: string[];
    source?: {
      channel_id: string;
      message_id?: string | null;
      thread_root_message_id?: string | null;
    } | null;
  }): Promise<{ task_id: string }> {
    return this.fetch("/api/issues/quick-create", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async createFeedback(data: {
    message: string;
    url?: string;
    workspace_id?: string;
  }): Promise<{ id: string; created_at: string }> {
    return this.fetch("/api/feedback", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateIssue(id: string, data: UpdateIssueRequest): Promise<Issue> {
    return this.fetch(`/api/issues/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async listChildIssues(id: string): Promise<{ issues: Issue[] }> {
    const raw = await this.fetch<unknown>(`/api/issues/${id}/children`);
    return parseWithFallback(raw, ChildIssuesResponseSchema, { issues: [] }, {
      endpoint: "GET /api/issues/:id/children",
    });
  }

  /** Notes linked to this issue (S3-R5b). ACL-filtered — inaccessible notes omitted. */
  async listIssueNoteRefs(id: string): Promise<IssueNoteRefListResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${encodeURIComponent(id)}/note-refs`);
    return parseWithFallback(raw, IssueNoteRefListResponseSchema, EMPTY_ISSUE_NOTE_REF_LIST, {
      endpoint: "GET /api/issues/:id/note-refs",
    });
  }

  /** Batched variant — returns children for multiple parents in one request.
   *  Avoids an N-request fan-out in Swimlane (one per visible parent lane).
   *  parentIds must be non-empty; pass a sorted, deduplicated list so the
   *  React Query cache key is stable across renders. */
  async listChildrenByParents(parentIds: string[]): Promise<{ issues: Issue[] }> {
    const raw = await this.fetch<unknown>(
      `/api/issues/children?parent_ids=${parentIds.join(",")}`,
    );
    return parseWithFallback(raw, ChildIssuesResponseSchema, { issues: [] }, {
      endpoint: "GET /api/issues/children",
    });
  }

  async getChildIssueProgress(): Promise<{ progress: { parent_issue_id: string; total: number; done: number }[] }> {
    return this.fetch("/api/issues/child-progress");
  }

  async deleteIssue(id: string): Promise<void> {
    await this.fetch(`/api/issues/${id}`, { method: "DELETE" });
  }

  async batchUpdateIssues(issueIds: string[], updates: UpdateIssueRequest): Promise<{ updated: number }> {
    return this.fetch("/api/issues/batch-update", {
      method: "POST",
      body: JSON.stringify({ issue_ids: issueIds, updates }),
    });
  }

  async batchDeleteIssues(issueIds: string[]): Promise<{ deleted: number }> {
    return this.fetch("/api/issues/batch-delete", {
      method: "POST",
      body: JSON.stringify({ issue_ids: issueIds }),
    });
  }

  // Comments
  async listComments(issueId: string): Promise<Comment[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/comments`);
    return parseWithFallback(raw, CommentsListSchema, [], {
      endpoint: "GET /api/issues/:id/comments",
    });
  }

  async createComment(
    issueId: string,
    content: string,
    type?: string,
    parentId?: string,
    attachmentIds?: string[],
    suppressAgentIds?: string[],
  ): Promise<Comment> {
    return this.fetch(`/api/issues/${issueId}/comments`, {
      method: "POST",
      body: JSON.stringify({
        content,
        type: type ?? "comment",
        ...(parentId ? { parent_id: parentId } : {}),
        ...(attachmentIds?.length ? { attachment_ids: attachmentIds } : {}),
        ...(suppressAgentIds?.length ? { suppress_agent_ids: suppressAgentIds } : {}),
      }),
    });
  }

  async previewCommentTriggers(issueId: string, content: string, parentId?: string): Promise<CommentTriggerPreview> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/comments/trigger-preview`, {
      method: "POST",
      body: JSON.stringify({
        content,
        ...(parentId ? { parent_id: parentId } : {}),
      }),
    });
    return parseWithFallback(raw, CommentTriggerPreviewSchema, { agents: [] }, {
      endpoint: "POST /api/issues/:id/comments/trigger-preview",
    });
  }

  async listTimeline(issueId: string): Promise<TimelineEntry[]> {
    const raw = await this.fetch<unknown>(
      `/api/issues/${issueId}/timeline`,
    );
    return parseWithFallback(raw, TimelineEntriesSchema, EMPTY_TIMELINE_ENTRIES, {
      endpoint: "GET /api/issues/:id/timeline",
    });
  }

  async getAssigneeFrequency(): Promise<AssigneeFrequencyEntry[]> {
    return this.fetch("/api/assignee-frequency");
  }

  async updateComment(commentId: string, content: string, attachmentIds?: string[]): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}`, {
      method: "PUT",
      body: JSON.stringify({ content, attachment_ids: attachmentIds }),
    });
  }

  async deleteComment(commentId: string): Promise<void> {
    await this.fetch(`/api/comments/${commentId}`, { method: "DELETE" });
  }

  async resolveComment(commentId: string): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}/resolve`, { method: "POST" });
  }

  async unresolveComment(commentId: string): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}/resolve`, { method: "DELETE" });
  }

  async addReaction(commentId: string, emoji: string): Promise<Reaction> {
    return this.fetch(`/api/comments/${commentId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    });
  }

  async removeReaction(commentId: string, emoji: string): Promise<void> {
    await this.fetch(`/api/comments/${commentId}/reactions`, {
      method: "DELETE",
      body: JSON.stringify({ emoji }),
    });
  }

  async addIssueReaction(issueId: string, emoji: string): Promise<IssueReaction> {
    return this.fetch(`/api/issues/${issueId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    });
  }

  async removeIssueReaction(issueId: string, emoji: string): Promise<void> {
    await this.fetch(`/api/issues/${issueId}/reactions`, {
      method: "DELETE",
      body: JSON.stringify({ emoji }),
    });
  }

  // Subscribers
  async listIssueSubscribers(issueId: string): Promise<IssueSubscriber[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/subscribers`);
    return parseWithFallback(raw, SubscribersListSchema, [], {
      endpoint: "GET /api/issues/:id/subscribers",
    });
  }

  async subscribeToIssue(issueId: string, userId?: string, userType?: string): Promise<void> {
    const body: Record<string, string> = {};
    if (userId) body.user_id = userId;
    if (userType) body.user_type = userType;
    await this.fetch(`/api/issues/${issueId}/subscribe`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async unsubscribeFromIssue(issueId: string, userId?: string, userType?: string): Promise<void> {
    const body: Record<string, string> = {};
    if (userId) body.user_id = userId;
    if (userType) body.user_type = userType;
    await this.fetch(`/api/issues/${issueId}/unsubscribe`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  // Agents
  async listAgents(params?: {
    workspace_id?: string;
    include_archived?: boolean;
  }): Promise<Agent[]> {
    const search = new URLSearchParams();
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.include_archived) search.set("include_archived", "true");
    return this.fetch(`/api/members/agents?${search}`);
  }

  async getAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/members/agents/${id}`);
  }

  async getAgentReminders(agentId: string): Promise<AgentReminderListResponse> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${agentId}/reminders`);
    return parseWithFallback(raw, AgentReminderListResponseSchema, EMPTY_AGENT_REMINDER_LIST, {
      endpoint: "GET /api/members/agents/{agentId}/reminders",
    });
  }

  async createAgent(data: CreateAgentRequest): Promise<Agent> {
    return this.fetch("/api/members/agents", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async ensureWindy(
    runtimeId: string,
    model: string,
    thinkingLevel?: string,
  ): Promise<EnsureWindyResponse> {
    const raw = await this.fetch<unknown>("/api/members/agents/windy", {
      method: "POST",
      body: JSON.stringify({
        runtime_id: runtimeId,
        model,
        ...(thinkingLevel ? { thinking_level: thinkingLevel } : {}),
      }),
    });
    return EnsureWindyResponseSchema.parse(raw);
  }

  async ensurePeriodBriefAgent(): Promise<EnsurePeriodBriefAgentResponse> {
    const raw = await this.fetch<unknown>("/api/members/agents/period-brief", {
      method: "POST",
      body: JSON.stringify({}),
    });
    return EnsurePeriodBriefAgentResponseSchema.parse(raw);
  }

  async ensureNotesAssistantAgent(input?: {
    clone_onboarding?: boolean;
    runtime_id?: string;
    model?: string;
  }): Promise<EnsureNotesAssistantAgentResponse> {
    const raw = await this.fetch<unknown>("/api/members/agents/notes-assistant", {
      method: "POST",
      body: JSON.stringify(input ?? {}),
    });
    return EnsureNotesAssistantAgentResponseSchema.parse(raw);
  }

  async ensurePeriodBriefCollectors(input: {
    model: string;
    runtime_id?: string;
  }): Promise<EnsurePeriodBriefCollectorsResponse> {
    const raw = await this.fetch<unknown>("/api/members/agents/period-brief-collectors", {
      method: "POST",
      body: JSON.stringify(input),
    });
    return parseWithFallback(
      raw,
      EnsurePeriodBriefCollectorsResponseSchema,
      { agents: [], created: [] },
      { endpoint: "POST /api/members/agents/period-brief-collectors" },
    );
  }

  async createAgentDraft(data: CreateAgentDraftRequest): Promise<AgentCreationDraft> {
    return this.fetch("/api/members/agents/drafts", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async getAgentDraft(id: string): Promise<AgentCreationDraft> {
    return this.fetch(`/api/members/agents/drafts/${encodeURIComponent(id)}`);
  }

  async listAgentTemplates(): Promise<AgentTemplateSummary[]> {
    const raw = await this.fetch<unknown>("/api/agent-templates");
    return parseWithFallback(
      raw,
      AgentTemplateSummaryListSchema,
      EMPTY_AGENT_TEMPLATE_SUMMARY_LIST,
      { endpoint: "GET /api/agent-templates" },
    );
  }

  async getAgentTemplate(slug: string): Promise<AgentTemplate> {
    const raw = await this.fetch<unknown>(
      `/api/agent-templates/${encodeURIComponent(slug)}`,
    );
    // Round-trip the requested slug into the fallback so a malformed
    // detail response still produces a navigable record matching the URL
    // the user clicked.
    return parseWithFallback(
      raw,
      AgentTemplateSchema,
      { ...EMPTY_AGENT_TEMPLATE_DETAIL, slug },
      { endpoint: "GET /api/agent-templates/:slug" },
    );
  }

  /** Creates an agent from a curated template. The server fetches every
   *  referenced skill URL in parallel, materializes them into the workspace
   *  (find-or-create by name), and writes the agent + skill bindings in a
   *  single transaction. On any upstream fetch failure, the entire write is
   *  rolled back and the API returns 422 with `failed_urls`. */
  async createAgentFromTemplate(
    data: CreateAgentFromTemplateRequest,
  ): Promise<CreateAgentFromTemplateResponse> {
    const raw = await this.fetch<unknown>("/api/members/agents/from-template", {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(
      raw,
      CreateAgentFromTemplateResponseSchema,
      EMPTY_CREATE_AGENT_FROM_TEMPLATE_RESPONSE,
      { endpoint: "POST /api/members/agents/from-template" },
    );
  }

  async updateAgent(id: string, data: UpdateAgentRequest): Promise<Agent> {
    return this.fetch(`/api/members/agents/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async bulkUpdateAgentRuntimeConfig(
    data: BulkUpdateAgentRuntimeConfigRequest,
  ): Promise<BulkUpdateAgentRuntimeConfigResponse> {
    return this.fetch("/api/members/agents/runtime-config", {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async bulkAgentLifecycle(
    data: BulkAgentLifecycleRequest,
  ): Promise<BulkAgentLifecycleResponse> {
    return this.fetch("/api/members/agents/lifecycle", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  /**
   * Workspace-level agent authority (`member` | `admin`). Separate from
   * PUT /api/members/agents/:id — that endpoint rejects `workspace_role`.
   * Human route only; workspace owners and admins are authorized server-side.
   */
  async updateAgentWorkspaceRole(
    workspaceId: string,
    agentId: string,
    role: "member" | "admin",
  ): Promise<{ status: string; agent_id: string; workspace_role: "member" | "admin" }> {
    return this.fetch(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/agents/${encodeURIComponent(agentId)}/role`,
      {
        method: "PATCH",
        body: JSON.stringify({ role }),
      },
    );
  }

  async archiveAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/members/agents/${id}/archive`, { method: "POST" });
  }

  async getAgentHealth(id: string): Promise<AgentHealthResponse> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${id}/health`);
    return parseWithFallback(raw, AgentHealthResponseSchema, EMPTY_AGENT_HEALTH_RESPONSE, {
      endpoint: "GET /api/members/agents/:id/health",
    });
  }

  // Active presentation-only Runner Activity boundary. Clients receive no
  // provider/runtime facts and must not apply a semantic fallback of their own.
  async getRunnerActivity(id: string): Promise<RunnerActivityResponse> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${id}/runner-activity`);
    return parseWithFallback(raw, RunnerActivityResponseSchema, EMPTY_RUNNER_ACTIVITY_RESPONSE, {
      endpoint: "GET /api/members/agents/:id/runner-activity",
    });
  }

  async getRunnerActivitySummaries(): Promise<RunnerActivitySummariesResponse> {
    const raw = await this.fetch<unknown>("/api/members/agents/runner-activity-summaries");
    return parseWithFallback(
      raw,
      RunnerActivitySummariesResponseSchema,
      EMPTY_RUNNER_ACTIVITY_SUMMARIES_RESPONSE,
      { endpoint: "GET /api/members/agents/runner-activity-summaries" },
    );
  }

  async getAgentPresence(): Promise<AgentPresenceResponse> {
    const raw = await this.fetch<unknown>(`/api/members/agents/presence`);
    return parseWithFallback(
      raw,
      AgentPresenceResponseSchema,
      EMPTY_AGENT_PRESENCE_RESPONSE,
      { endpoint: "GET /api/members/agents/presence" },
    );
  }

  /**
   * Returns the plaintext `custom_env` map for an agent. Owner/admin
   * only; calls from agent-actor sessions get a 403. Every successful
   * call writes an `agent_env_revealed` activity_log row server-side.
   * MUL-2600.
   */
  async getAgentEnv(id: string): Promise<AgentEnvResponse> {
    return this.fetch(`/api/members/agents/${id}/env`);
  }

  /**
   * Replaces an agent's `custom_env` wholesale. Values equal to
   * `"****"` are preserved server-side (the **** guard) so a partial
   * UI edit doesn't overwrite real secrets with the masked
   * placeholder. Owner/admin only; agent actors get a 403. Every
   * successful call writes an `agent_env_updated` activity_log row.
   * MUL-2600.
   */
  async updateAgentEnv(id: string, data: UpdateAgentEnvRequest): Promise<AgentEnvResponse> {
    return this.fetch(`/api/members/agents/${id}/env`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  /**
   * Returns the plaintext `custom_env` map for a runtime. Owner/admin /
   * runtime owner only; every successful call writes a
   * `runtime_env_revealed` activity_log row server-side.
   */
  async getRuntimeEnv(id: string): Promise<RuntimeEnvResponse> {
    return this.fetch(`/api/runtimes/${id}/env`);
  }

  /**
   * Replaces a runtime's `custom_env` wholesale. Values equal to `"****"`
   * preserve the existing value for that key (same **** sentinel as agent
   * env). Owner/admin / runtime owner only; every write is audited.
   */
  async updateRuntimeEnv(id: string, data: UpdateRuntimeEnvRequest): Promise<RuntimeEnvResponse> {
    return this.fetch(`/api/runtimes/${id}/env`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async listAgentFiles(id: string, params?: ListAgentFilesParams): Promise<AgentFilesResponse> {
    const search = new URLSearchParams();
    if (params?.include_hidden) search.set("include_hidden", "true");
    if (params?.path) search.set("path", params.path);
    const suffix = search.toString() ? `?${search}` : "";
    const raw = await this.fetch<unknown>(`/api/members/agents/${id}/files${suffix}`);
    return parseWithFallback(raw, AgentFilesResponseSchema, EMPTY_AGENT_FILES_RESPONSE, {
      endpoint: "GET /api/members/agents/:id/files",
    });
  }

  async getAgentFileContent(id: string, path: string): Promise<AgentFileContentResponse> {
    const search = new URLSearchParams({ path });
    const raw = await this.fetch<unknown>(`/api/members/agents/${id}/files/content?${search}`);
    return parseWithFallback(raw, AgentFileContentResponseSchema, EMPTY_AGENT_FILE_CONTENT_RESPONSE, {
      endpoint: "GET /api/members/agents/:id/files/content",
    });
  }

  async updateAgentFileContent(
    id: string,
    data: UpdateAgentFileContentRequest,
  ): Promise<UpdateAgentFileContentResponse> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${id}/files/content`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
    return parseWithFallback(
      raw,
      UpdateAgentFileContentResponseSchema,
      EMPTY_UPDATE_AGENT_FILE_CONTENT_RESPONSE,
      { endpoint: "PUT /api/members/agents/:id/files/content" },
    );
  }

  async restoreAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/members/agents/${id}/restore`, { method: "POST" });
  }

  // Raft-aligned Agent reset modes. Preflight is the server-authoritative
  // source for per-mode enable/disable — the FE never
  // derives active/idle from `agent.status`.
  async getAgentRestartPreflight(id: string): Promise<AgentRestartPreflight> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${id}/reset`);
    return parseWithFallback(
      raw,
      AgentRestartPreflightSchema,
      EMPTY_AGENT_RESTART_PREFLIGHT,
      { endpoint: "GET /api/members/agents/{id}/reset" },
    );
  }

  // Client sends only Raft's `mode` (never a path/force/runtime_id).
  async resetAgent(
    id: string,
    mode: AgentRestartMode,
  ): Promise<AgentRestartOperation> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${id}/reset`, {
      method: "POST",
      body: JSON.stringify({ mode }),
    });
    return parseWithFallback(
      raw,
      AgentRestartOperationSchema,
      EMPTY_AGENT_RESTART_OPERATION,
      { endpoint: "POST /api/members/agents/{id}/reset" },
    );
  }

  async startAgent(id: string): Promise<{ status: string }> {
    return this.fetch(`/api/members/agents/${id}/start`, { method: "POST" });
  }

  async stopAgent(id: string): Promise<{ status: string }> {
    return this.fetch(`/api/members/agents/${id}/stop`, { method: "POST" });
  }

  // Bulk-cancel every active task (queued/dispatched/running) for the agent.
  // Permission: agent owner or workspace admin/owner. Server returns the
  // count of cancelled rows; broadcasts task:cancelled for each so other
  // surfaces can clear their live cards.
  async cancelAgentTasks(id: string): Promise<{ cancelled: number }> {
    return this.fetch(`/api/members/agents/${id}/cancel-tasks`, { method: "POST" });
  }

  async listRuntimes(params?: { workspace_id?: string; owner?: "me" }): Promise<AgentRuntime[]> {
    const search = new URLSearchParams();
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.owner) search.set("owner", params.owner);
    const raw = await this.fetch<unknown>(`/api/runtimes?${search}`);
    return parseWithFallback(
      raw,
      AgentRuntimeListSchema,
      EMPTY_AGENT_RUNTIME_LIST,
      { endpoint: "GET /api/runtimes" },
    );
  }

  /**
   * The agent's assembled runtime config. Separate from getAgent because it
   * joins Computer-level facts (name, liveness) that no agent row carries,
   * and because it must stay readable for an agent bound to a runtime the
   * caller cannot manage.
   */
  async getAgentRuntimeConfig(agentId: string): Promise<AgentRuntimeConfig> {
    const raw = await this.fetch<unknown>(
      `/api/agents/${encodeURIComponent(agentId)}/runtime-config`,
    );
    return parseWithFallback(
      raw,
      AgentRuntimeConfigSchema,
      EMPTY_AGENT_RUNTIME_CONFIG,
      { endpoint: "GET /api/agents/:id/runtime-config" },
    ) as AgentRuntimeConfig;
  }

  async listComputers(workspaceId: string): Promise<ComputerConnection[]> {
    const search = new URLSearchParams({ workspace_id: workspaceId });
    const raw = await this.fetch<unknown>(`/api/computers?${search}`);
    return parseWithFallback(
      raw,
      ComputerConnectionListSchema,
      EMPTY_COMPUTER_CONNECTION_LIST,
      { endpoint: "GET /api/computers" },
    );
  }

  async patchComputerWorkJournal(
    daemonId: string,
    enabled: boolean,
  ): Promise<{ enabled: boolean }> {
    const raw = await this.fetch<unknown>(
      `/api/computers/${encodeURIComponent(daemonId)}/work-journal`,
      { method: "PATCH", body: JSON.stringify({ enabled }) },
    );
    return parseWithFallback(
      raw,
      ComputerWorkJournalSettingSchema,
      EMPTY_COMPUTER_WORK_JOURNAL_SETTING,
      { endpoint: "PATCH /api/computers/:daemonId/work-journal" },
    );
  }

  async listCloudRuntimeNodes(
    params?: ListCloudRuntimeNodesParams,
  ): Promise<CloudRuntimeNode[]> {
    const search = new URLSearchParams();
    if (params?.limit !== undefined) search.set("limit", String(params.limit));
    if (params?.offset !== undefined) search.set("offset", String(params.offset));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-runtime/nodes${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      CloudRuntimeNodeListSchema,
      EMPTY_CLOUD_RUNTIME_NODE_LIST,
      { endpoint: "GET /api/cloud-runtime/nodes" },
    );
  }

  async createCloudRuntimeNode(
    data: CreateCloudRuntimeNodeRequest,
  ): Promise<CloudRuntimeNode> {
    const res = await this.fetchRaw("/api/cloud-runtime/nodes", {
      method: "POST",
      body: JSON.stringify(data),
      extraHeaders: { "Content-Type": "application/json" },
    });
    const raw = await res.json() as unknown;
    return parseWithFallback(
      raw,
      CloudRuntimeNodeSchema,
      EMPTY_CLOUD_RUNTIME_NODE,
      { endpoint: "POST /api/cloud-runtime/nodes" },
    );
  }

  async deleteCloudRuntimeNode(instanceId: string): Promise<void> {
    await this.fetchRaw("/api/cloud-runtime/nodes", {
      method: "DELETE",
      body: JSON.stringify({ instance_id: instanceId }),
      extraHeaders: { "Content-Type": "application/json" },
    });
  }

  // ---------------------------------------------------------------------
  // Cloud Billing — proxies to multica-cloud /api/v1/billing/*. The
  // multica-api server stamps X-User-ID and forwards bytes; everything
  // here is upstream-shaped. See packages/core/types/billing.ts for the
  // response field documentation.
  // ---------------------------------------------------------------------

  async getCloudBillingBalance(): Promise<BillingBalance> {
    const raw = await this.fetch<unknown>("/api/cloud-billing/balance");
    return parseWithFallback(raw, BillingBalanceSchema, EMPTY_BILLING_BALANCE, {
      endpoint: "GET /api/cloud-billing/balance",
    });
  }

  async listCloudBillingTransactions(
    params?: { page?: number; page_size?: number },
  ): Promise<BillingTransactionsPage> {
    const search = new URLSearchParams();
    if (params?.page !== undefined) search.set("page", String(params.page));
    if (params?.page_size !== undefined) search.set("page_size", String(params.page_size));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/transactions${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      BillingTransactionsPageSchema,
      EMPTY_BILLING_TRANSACTIONS_PAGE,
      { endpoint: "GET /api/cloud-billing/transactions" },
    );
  }

  async listCloudBillingBatches(
    params?: { page?: number; page_size?: number },
  ): Promise<BillingBatchesPage> {
    const search = new URLSearchParams();
    if (params?.page !== undefined) search.set("page", String(params.page));
    if (params?.page_size !== undefined) search.set("page_size", String(params.page_size));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/batches${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      BillingBatchesPageSchema,
      EMPTY_BILLING_BATCHES_PAGE,
      { endpoint: "GET /api/cloud-billing/batches" },
    );
  }

  async listCloudBillingTopups(
    params?: { page?: number; page_size?: number },
  ): Promise<BillingTopupsPage> {
    const search = new URLSearchParams();
    if (params?.page !== undefined) search.set("page", String(params.page));
    if (params?.page_size !== undefined) search.set("page_size", String(params.page_size));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/topups${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      BillingTopupsPageSchema,
      EMPTY_BILLING_TOPUPS_PAGE,
      { endpoint: "GET /api/cloud-billing/topups" },
    );
  }

  async listCloudBillingPriceTiers(): Promise<BillingPriceTier[]> {
    const raw = await this.fetch<unknown>("/api/cloud-billing/price-tiers");
    return parseWithFallback(
      raw,
      BillingPriceTierListSchema,
      EMPTY_BILLING_PRICE_TIER_LIST,
      { endpoint: "GET /api/cloud-billing/price-tiers" },
    );
  }

  async createCloudBillingCheckoutSession(
    data: CreateBillingCheckoutSessionRequest,
  ): Promise<CreateBillingCheckoutSessionResponse> {
    const res = await this.fetchRaw("/api/cloud-billing/checkout-sessions", {
      method: "POST",
      body: JSON.stringify(data),
      extraHeaders: { "Content-Type": "application/json" },
    });
    const raw = (await res.json()) as unknown;
    return parseWithFallback(
      raw,
      CreateBillingCheckoutSessionResponseSchema,
      EMPTY_CREATE_BILLING_CHECKOUT_SESSION_RESPONSE,
      { endpoint: "POST /api/cloud-billing/checkout-sessions" },
    );
  }

  async getCloudBillingCheckoutSession(
    sessionId: string,
  ): Promise<BillingCheckoutSessionStatus> {
    // Stripe session ids are `cs_<base62>` so they're URL-safe by
    // construction; encodeURIComponent is paranoia for the case where a
    // future Stripe format change adds a non-alphanumeric character. The
    // server has its own allow-list rejection for unsafe ids.
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/checkout-sessions/${encodeURIComponent(sessionId)}`,
    );
    return parseWithFallback(
      raw,
      BillingCheckoutSessionStatusSchema,
      EMPTY_BILLING_CHECKOUT_SESSION_STATUS,
      { endpoint: "GET /api/cloud-billing/checkout-sessions/{sessionId}" },
    );
  }

  async createCloudBillingPortalSession(): Promise<CreateBillingPortalSessionResponse> {
    const res = await this.fetchRaw("/api/cloud-billing/portal-sessions", {
      method: "POST",
      // Body is intentionally absent — the upstream endpoint requires no
      // payload today. fetchRaw with no body skips the Content-Type
      // default; that's fine because there's nothing to declare.
    });
    const raw = (await res.json()) as unknown;
    return parseWithFallback(
      raw,
      CreateBillingPortalSessionResponseSchema,
      EMPTY_CREATE_BILLING_PORTAL_SESSION_RESPONSE,
      { endpoint: "POST /api/cloud-billing/portal-sessions" },
    );
  }

  async deleteRuntime(runtimeId: string): Promise<void> {
    await this.fetch(`/api/runtimes/${runtimeId}`, { method: "DELETE" });
  }

  // Permanently removes the current Workspace's server-side Computer
  // projection. Active agents return a structured 409 and must be deleted
  // through the normal Agent flow first.
  async deleteComputer(daemonId: string): Promise<DeleteComputerResponse> {
    const raw = await this.fetch<unknown>(
      `/api/computers/${encodeURIComponent(daemonId)}`,
      { method: "DELETE" },
    );
    return parseWithFallback(
      raw,
      DeleteComputerResponseSchema,
      EMPTY_DELETE_COMPUTER_RESPONSE,
      { endpoint: "DELETE /api/computers/{daemonId}" },
    );
  }

  // Cascade variant of deleteRuntime. The strict DELETE refuses with
  // structured 409 (`code: "runtime_has_active_agents"`, body carries the
  // blocking agents) when active agents are bound; the front-end then opens
  // the cascade-mode confirmation dialog and submits the user-confirmed
  // active agent set here. Server compares the snapshot to the live set
  // inside the transaction and refuses with `code: "runtime_delete_plan_changed"`
  // (same shape, fresh `active_agents`) if they don't match — caller should
  // re-render the agent list and force the user to re-confirm.
  async archiveAgentsAndDeleteRuntime(
    runtimeId: string,
    expectedActiveAgentIds: string[],
  ): Promise<{ status: string; agents_archived: number; tasks_cancelled: number }> {
    return this.fetch(`/api/runtimes/${runtimeId}/archive-agents-and-delete`, {
      method: "POST",
      body: JSON.stringify({ expected_active_agent_ids: expectedActiveAgentIds }),
    });
  }

  async updateRuntime(
    runtimeId: string,
    patch: {
      visibility?: "private" | "public";
      display_name?: string | null;
    },
  ): Promise<AgentRuntime> {
    return this.fetch(`/api/runtimes/${runtimeId}`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    });
  }

  /** On-demand scan of `~/.multica/workspaces/<workspace_id>/agents/<agent_id>` directories. */
  async listRuntimeAgentWorkspaces(
    runtimeId: string,
  ): Promise<RuntimeAgentWorkspacesResponse> {
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/agent-workspaces`,
    );
    return parseWithFallback(
      raw,
      RuntimeAgentWorkspacesResponseSchema,
      EMPTY_RUNTIME_AGENT_WORKSPACES_RESPONSE,
      { endpoint: "GET /api/runtimes/:id/agent-workspaces" },
    );
  }

  async deleteRuntimeAgentWorkspace(
    runtimeId: string,
    dirName: string,
  ): Promise<{ ok: boolean }> {
    return this.fetch(
      `/api/runtimes/${runtimeId}/agent-workspaces/${encodeURIComponent(dirName)}`,
      { method: "DELETE" },
    );
  }

  async getRuntimeUsage(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsage[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    // `tz` drives the calendar-day boundary for the trend chart (Viewing
    // layer). Caller-supplied; the backend falls back to user.timezone /
    // UTC if omitted.
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage?${search}`,
    );
    return parseWithFallback<RuntimeUsage[]>(raw, RuntimeUsageListSchema, [], {
      endpoint: "GET /api/runtimes/:id/usage",
    });
  }

  async getRuntimeTaskActivity(
    runtimeId: string,
    params?: { tz?: string },
  ): Promise<RuntimeHourlyActivity[]> {
    // Hour-of-day heatmap follows the viewer's tz, like the other reports on
    // this page. Pass the viewer's IANA zone so the server buckets correctly.
    const search = new URLSearchParams();
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/activity?${search}`,
    );
    return parseWithFallback<RuntimeHourlyActivity[]>(
      raw,
      RuntimeHourlyActivityListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/activity" },
    );
  }

  async getRuntimeUsageByAgent(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsageByAgent[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage/by-agent?${search}`,
    );
    return parseWithFallback<RuntimeUsageByAgent[]>(
      raw,
      RuntimeUsageByAgentListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/usage/by-agent" },
    );
  }

  async getRuntimeUsageByHour(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsageByHour[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage/by-hour?${search}`,
    );
    return parseWithFallback<RuntimeUsageByHour[]>(
      raw,
      RuntimeUsageByHourListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/usage/by-hour" },
    );
  }

  // ---------------------------------------------------------------------------
  // Workspace dashboard — three independent rollups for `/{slug}/dashboard`.
  // Each accepts an optional `project_id` to narrow the scope to one project.
  // Cost is computed client-side from the model pricing table (same contract
  // as the per-runtime endpoints above).
  // ---------------------------------------------------------------------------

  async getDashboardUsageDaily(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardUsageDaily[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/usage/daily?${search}`);
    return parseWithFallback<DashboardUsageDaily[]>(
      raw,
      DashboardUsageDailyListSchema,
      [],
      { endpoint: "GET /api/dashboard/usage/daily" },
    );
  }

  async getDashboardUsageByAgent(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardUsageByAgent[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/usage/by-agent?${search}`);
    return parseWithFallback<DashboardUsageByAgent[]>(
      raw,
      DashboardUsageByAgentListSchema,
      [],
      { endpoint: "GET /api/dashboard/usage/by-agent" },
    );
  }

  async getDashboardAgentRunTime(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardAgentRunTime[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    // `tz` aligns the "last N days" cutoff with the viewer's calendar,
    // matching the per-agent token card.
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/agent-runtime?${search}`);
    return parseWithFallback<DashboardAgentRunTime[]>(
      raw,
      DashboardAgentRunTimeListSchema,
      [],
      { endpoint: "GET /api/dashboard/agent-runtime" },
    );
  }

  async getDashboardRunTimeDaily(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardRunTimeDaily[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    // `tz` cuts the day buckets in the viewer's calendar so Time / Tasks
    // align with the Cost / Tokens charts.
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/runtime/daily?${search}`);
    return parseWithFallback<DashboardRunTimeDaily[]>(
      raw,
      DashboardRunTimeDailyListSchema,
      [],
      { endpoint: "GET /api/dashboard/runtime/daily" },
    );
  }

  async initiateMachineUpgrade(
    daemonId: string,
    targetVersion: string,
    requestId: string,
  ): Promise<{ request_id: string }> {
    return this.fetch<{ request_id: string }>(`/api/daemons/${daemonId}/upgrades`, {
      method: "POST",
      body: JSON.stringify({ target_version: targetVersion, request_id: requestId }),
    });
  }

  async initiateRestart(runtimeId: string): Promise<RuntimeRestart> {
    return this.fetch(`/api/runtimes/${runtimeId}/restart`, {
      method: "POST",
    });
  }

  async getRestart(
    runtimeId: string,
    restartId: string,
  ): Promise<RuntimeRestart> {
    return this.fetch(`/api/runtimes/${runtimeId}/restart/${restartId}`);
  }

  async initiateListModels(runtimeId: string): Promise<RuntimeModelListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/models`, { method: "POST" });
  }

  async getListModelsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeModelListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/models/${requestId}`);
  }

  async initiateListLocalSkills(
    runtimeId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills`, {
      method: "POST",
    });
  }

  async getListLocalSkillsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/${requestId}`);
  }

  async initiateImportLocalSkill(
    runtimeId: string,
    data: CreateRuntimeLocalSkillImportRequest,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/import`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async getImportLocalSkillResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/import/${requestId}`);
  }

  async listAgentTasks(agentId: string): Promise<AgentTask[]> {
    return this.fetch(`/api/members/agents/${agentId}/tasks`);
  }

  // Workspace-scoped agent task snapshot: every active task
  // (queued/dispatched/running) plus each agent's most recent terminal task.
  // Powers the front-end's "active wins, else latest terminal" presence
  // derivation; one fetch backs every per-agent presence read in the app.
  // Workspace is resolved server-side from the X-Workspace-Slug header.
  async getAgentTaskSnapshot(): Promise<AgentTask[]> {
    return this.fetch(`/api/agent-task-snapshot`);
  }

  // Overview "pending human approval" KPI: in_review issue count + longest wait.
  async getIssueReviewStats(): Promise<IssueReviewStats> {
    return this.fetch(`/api/issues/review-stats`);
  }

  // Overview "tasks done" KPI: completed/failed/total agent-task counts.
  async getAgentTaskStats(): Promise<AgentTaskStats> {
    return this.fetch(`/api/agent-task-stats`);
  }

  // Workspace-wide, cursor-paginated feed of terminal agent tasks (one row per
  // completed/failed/cancelled task), newest first. Powers the overview agent
  // activity timeline. Workspace is resolved server-side from the header.
  async listAgentTaskFeed(params: {
    before?: { completed_at: string; id: string } | null;
    limit?: number;
  }): Promise<AgentTaskFeedPage> {
    const qs = new URLSearchParams();
    if (params.limit != null) qs.set("limit", String(params.limit));
    if (params.before) {
      qs.set("before_completed_at", params.before.completed_at);
      qs.set("before_id", params.before.id);
    }
    const q = qs.toString();
    return this.fetch(`/api/agent-tasks${q ? `?${q}` : ""}`);
  }

  // Per-agent daily activity for the last 30 days, anchored on
  // completed_at. One workspace-wide fetch backs both the Agents-list
  // sparkline (uses trailing 7 buckets) and the agent detail "Last 30
  // days" panel (uses all 30).
  async getWorkspaceAgentActivity30d(): Promise<AgentActivityBucket[]> {
    return this.fetch(`/api/agent-activity-30d`);
  }

  // Per-agent 30-day total run count for the Agents-list RUNS column.
  async getWorkspaceAgentRunCounts(): Promise<AgentRunCount[]> {
    return this.fetch(`/api/agent-run-counts`);
  }

  async getAgentFleetRankings(): Promise<AgentFleetRank[]> {
    const raw = await this.fetch<unknown>(`/api/members/agents/fleet-rankings`);
    return parseWithFallback(raw, agentFleetRankListSchema, EMPTY_AGENT_FLEET_RANK_LIST, {
      endpoint: "GET /api/members/agents/fleet-rankings",
    });
  }

  async getAgentFleetRank(agentId: string): Promise<AgentFleetRank> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${agentId}/fleet-rank`);
    return parseWithFallback(
      raw,
      agentFleetRankSchema,
      {
        agent_id: agentId,
        fleet_score: 0,
        class_id: "reserve",
        class_label: "Reserve",
        fleet_rank: 0,
        fleet_size: 0,
        sample_tasks: 0,
        min_sample_tasks: 5,
        sample_sufficient: false,
        frozen: false,
        pillars: { delivery: 0, evolution: 0, growth: 0, efficiency: 0 },
      },
      { endpoint: `GET /api/members/agents/${agentId}/fleet-rank` },
    );
  }

  async getAgentFleetRankRules(): Promise<AgentFleetRulesDocument> {
    const raw = await this.fetch<unknown>(`/api/members/agents/fleet-rank/rules`);
    return parseWithFallback(raw, agentFleetRulesSchema, {
      version: "",
      window_days: 30,
      min_sample_tasks: 5,
      pillar_weights: {},
      class_thresholds: [],
      changelog: [],
    }, { endpoint: "GET /api/members/agents/fleet-rank/rules" });
  }

  async getAgentHonorRules(): Promise<AgentHonorRulesView> {
    const raw = await this.fetch<unknown>("/api/members/agents/honor/rules");
    return parseWithFallback(
      raw,
      agentHonorRulesViewSchema,
      EMPTY_AGENT_HONOR_RULES_VIEW,
      { endpoint: "GET /api/members/agents/honor/rules" },
    );
  }

  async updateAgentHonorRules(rules: AgentHonorRules): Promise<AgentHonorRulesView> {
    const raw = await this.fetch<unknown>("/api/members/agents/honor/rules", {
      method: "PUT",
      body: JSON.stringify(rules),
    });
    return parseWithFallback(
      raw,
      agentHonorRulesViewSchema,
      EMPTY_AGENT_HONOR_RULES_VIEW,
      { endpoint: "PUT /api/members/agents/honor/rules" },
    );
  }

  async getAgentHonorAdminAudit(agentId?: string): Promise<AgentHonorAdminAudit[]> {
    const query = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : "";
    const raw = await this.fetch<unknown>(`/api/members/agents/honor/audit${query}`);
    return parseWithFallback(raw, agentHonorAdminAuditListSchema, [], {
      endpoint: "GET /api/members/agents/honor/audit",
    });
  }

  async getAgentHonor(agentId: string): Promise<AgentHonorDashboard> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${agentId}/honor`);
    return parseWithFallback(
      raw,
      agentHonorDashboardSchema,
      { ...EMPTY_AGENT_HONOR_DASHBOARD, agent_id: agentId },
      { endpoint: "GET /api/members/agents/:id/honor" },
    );
  }

  async updateAgentHonorShowcase(
    agentId: string,
    input: UpdateAgentHonorShowcaseRequest,
  ): Promise<AgentHonorDashboard> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${agentId}/honor`, {
      method: "PATCH",
      body: JSON.stringify(input),
    });
    return parseWithFallback(
      raw,
      agentHonorDashboardSchema,
      { ...EMPTY_AGENT_HONOR_DASHBOARD, agent_id: agentId },
      { endpoint: "PATCH /api/members/agents/:id/honor" },
    );
  }

  async grantAgentHonor(
    agentId: string,
    input: AgentHonorGrantRequest,
  ): Promise<AgentHonorDashboard> {
    const raw = await this.fetch<unknown>(`/api/members/agents/${agentId}/honor/grants`, {
      method: "POST",
      body: JSON.stringify(input),
    });
    return parseWithFallback(
      raw,
      agentHonorDashboardSchema,
      { ...EMPTY_AGENT_HONOR_DASHBOARD, agent_id: agentId },
      { endpoint: "POST /api/members/agents/:id/honor/grants" },
    );
  }

  async revokeAgentAchievement(
    agentId: string,
    achievementId: string,
    reason: string,
  ): Promise<void> {
    await this.fetch(
      `/api/members/agents/${agentId}/honor/achievements/${encodeURIComponent(achievementId)}`,
      { method: "DELETE", body: JSON.stringify({ reason }) },
    );
  }

  async getActiveTasksForIssue(issueId: string): Promise<{ tasks: AgentTask[] }> {
    return this.fetch(`/api/issues/${issueId}/active-task`);
  }

  async listTaskMessages(taskId: string): Promise<TaskMessagePayload[]> {
    return this.fetch(`/api/tasks/${taskId}/messages`);
  }

  /**
   * Chat execution transcript for a chat session's inbox-event round (#414).
   * Replaces the `listTaskMessages` inbox-event-id compat: the session-scoped
   * endpoint validates the event belongs to this session server-side (permission
   * + bounded history), returning the same `TaskMessagePayload[]` shape so the
   * timeline builder is unchanged. `eventId` is the pending inbox-event id (live)
   * or the persisted assistant `message.task_id` (completed).
   */
  async listChatAgentInboxEventTimeline(
    sessionId: string,
    eventId: string,
  ): Promise<TaskMessagePayload[]> {
    return this.fetch(
      `/api/chat/sessions/${sessionId}/agent-inbox-events/${eventId}/timeline`,
    );
  }

  async listTasksByIssue(issueId: string): Promise<AgentTask[]> {
    return this.fetch(`/api/issues/${issueId}/task-runs`);
  }

  async getIssueUsage(issueId: string): Promise<IssueUsageSummary> {
    return this.fetch(`/api/issues/${issueId}/usage`);
  }

  async cancelTask(issueId: string, taskId: string): Promise<AgentTask> {
    return this.fetch(`/api/issues/${issueId}/tasks/${taskId}/cancel`, {
      method: "POST",
    });
  }

  async rerunIssue(issueId: string, taskId?: string): Promise<AgentTask> {
    return this.fetch(`/api/issues/${issueId}/rerun`, {
      method: "POST",
      body: JSON.stringify(taskId ? { task_id: taskId } : {}),
    });
  }

  // Inbox
  async listInbox(): Promise<InboxItem[]> {
    return this.fetch("/api/inbox");
  }

  async markInboxRead(id: string): Promise<InboxItem> {
    return this.fetch(`/api/inbox/${id}/read`, { method: "POST" });
  }

  async archiveInbox(id: string): Promise<InboxItem> {
    return this.fetch(`/api/inbox/${id}/archive`, { method: "POST" });
  }

  async getUnreadInboxCount(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/unread-count");
  }

  async markAllInboxRead(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/mark-all-read", { method: "POST" });
  }

  async archiveAllInbox(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/archive-all", { method: "POST" });
  }

  async archiveAllReadInbox(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/archive-all-read", { method: "POST" });
  }

  async archiveCompletedInbox(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/archive-completed", { method: "POST" });
  }

  // Member Activity feed (threads + inbox)
  async listUserActivity(params: {
    tab: UserActivityTab;
    cursor?: string;
    limit?: number;
  }): Promise<UserActivityListResponse> {
    const search = new URLSearchParams({ tab: params.tab });
    if (params.cursor) search.set("cursor", params.cursor);
    if (params.limit != null) search.set("limit", String(params.limit));
    const suffix = search.toString();
    return this.fetch(`/api/activity${suffix ? `?${suffix}` : ""}`);
  }

  async markAllUserActivityRead(): Promise<{ thread_count: number; inbox_count: number }> {
    return this.fetch("/api/activity/mark-all-read", { method: "POST" });
  }

  // Notification preferences
  //
  // `workspaceSlug` overrides the default `X-Workspace-Slug` header (which
  // follows the active workspace) so a caller can read a SPECIFIC workspace's
  // preferences — e.g. honoring the mute setting of the workspace an inbox
  // notification came from while the user is viewing a different one (#3766).
  async getNotificationPreferences(workspaceSlug?: string): Promise<NotificationPreferenceResponse> {
    return this.fetch(
      "/api/notification-preferences",
      workspaceSlug ? { headers: { "X-Workspace-Slug": workspaceSlug } } : undefined,
    );
  }

  async updateNotificationPreferences(preferences: NotificationPreferences): Promise<NotificationPreferenceResponse> {
    return this.fetch("/api/notification-preferences", {
      method: "PUT",
      body: JSON.stringify({ preferences }),
    });
  }

  async getWebPushPublicKey(): Promise<WebPushPublicKeyResponse> {
    const raw = await this.fetch<unknown>("/api/web-push/public-key");
    return parseWithFallback<WebPushPublicKeyResponse>(raw, WebPushPublicKeySchema, EMPTY_WEB_PUSH_PUBLIC_KEY, {
      endpoint: "GET /api/web-push/public-key",
    });
  }

  async bindWebPushSubscription(subscription: WebPushSubscriptionPayload): Promise<WebPushSubscriptionResponse> {
    const raw = await this.fetch<unknown>("/api/web-push/subscriptions", {
      method: "POST",
      body: JSON.stringify({ subscription }),
    });
    return parseWithFallback<WebPushSubscriptionResponse>(raw, WebPushSubscriptionSchema, EMPTY_WEB_PUSH_SUBSCRIPTION, {
      endpoint: "POST /api/web-push/subscriptions",
    });
  }

  async unbindWebPushSubscription(endpoint: string): Promise<{ ok: boolean }> {
    return this.fetch("/api/web-push/subscriptions", {
      method: "DELETE",
      body: JSON.stringify({ endpoint }),
    });
  }

  /** LRM-755: real VAPID push to the caller's bound devices (settings self-test). */
  async sendTestWebPush(): Promise<WebPushTestResponse> {
    const raw = await this.fetch<unknown>("/api/web-push/test", { method: "POST" });
    return parseWithFallback<WebPushTestResponse>(raw, WebPushTestSchema, EMPTY_WEB_PUSH_TEST, {
      endpoint: "POST /api/web-push/test",
    });
  }

  // App Config
  async getConfig(): Promise<AppConfigResponse> {
    const raw = await this.fetch<unknown>("/api/config");
    return parseWithFallback<AppConfigResponse>(raw, AppConfigSchema, EMPTY_APP_CONFIG, {
      endpoint: "GET /api/config",
    });
  }

  async listStickers(): Promise<StickerCatalogResponse> {
    const raw = await this.fetch<unknown>("/api/stickers");
    return parseWithFallback(raw, StickerCatalogResponseSchema, EMPTY_STICKER_CATALOG_RESPONSE, {
      endpoint: "GET /api/stickers",
    });
  }

  // Workspaces
  async listWorkspaces(): Promise<Workspace[]> {
    const raw = await this.fetch<unknown>("/api/workspaces");
    return parseWithFallback(raw, WorkspaceListSchema, [], {
      endpoint: "GET /api/workspaces",
    });
  }

  async getWorkspace(id: string): Promise<Workspace> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${id}`);
    return parseWithFallback(raw, WorkspaceSchema, EMPTY_WORKSPACE, {
      endpoint: "GET /api/workspaces/:id",
    });
  }

  async createWorkspace(data: { name: string; slug: string; description?: string; context?: string }): Promise<Workspace> {
    const raw = await this.fetch<unknown>("/api/workspaces", {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, WorkspaceSchema, EMPTY_WORKSPACE, {
      endpoint: "POST /api/workspaces",
    });
  }

  async updateWorkspace(id: string, data: { name?: string; description?: string; context?: string; settings?: Record<string, unknown>; issue_prefix?: string; avatar_url?: string }): Promise<Workspace> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, WorkspaceSchema, EMPTY_WORKSPACE, {
      endpoint: "PATCH /api/workspaces/:id",
    });
  }

  // Sandboxes
  async listSandboxNodes(): Promise<SandboxNode[]> {
    return this.fetch("/api/sandbox/nodes");
  }

  async createSandboxNode(data: {
    node_key?: string;
    name: string;
    capabilities?: unknown[];
    max_concurrency?: number;
    metadata?: Record<string, unknown>;
  }): Promise<SandboxNode> {
    return this.fetch("/api/sandbox/nodes", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateSandboxNode(
    nodeId: string,
    data: { name?: string; default_template_id?: string },
  ): Promise<SandboxNode> {
    return this.fetch(`/api/sandbox/nodes/${nodeId}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async deleteSandboxNode(nodeId: string): Promise<void> {
    await this.fetch(`/api/sandbox/nodes/${nodeId}`, { method: "DELETE" });
  }

  async createSandboxNodeToken(nodeId: string, data: { name?: string } = {}): Promise<{ token: string; token_prefix: string; expires_at: string }> {
    return this.fetch(`/api/sandbox/nodes/${nodeId}/tokens`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async listSandboxNodeTemplates(nodeId: string): Promise<SandboxNodeTemplatesResponse> {
    const raw = await this.fetch(`/api/sandbox/nodes/${nodeId}/templates`);
    return parseWithFallback(
      raw,
      SandboxNodeTemplatesResponseSchema,
      EMPTY_SANDBOX_NODE_TEMPLATES_RESPONSE,
      { endpoint: `GET /api/sandbox/nodes/${nodeId}/templates` },
    );
  }

  async listSandboxNodeDockerImages(nodeId: string): Promise<SandboxNodeDockerImagesResponse> {
    const raw = await this.fetch(`/api/sandbox/nodes/${nodeId}/docker-images`);
    return parseWithFallback(
      raw,
      SandboxNodeDockerImagesResponseSchema,
      EMPTY_SANDBOX_NODE_DOCKER_IMAGES_RESPONSE,
      { endpoint: `GET /api/sandbox/nodes/${nodeId}/docker-images` },
    );
  }

  async listSandboxBindings(workspaceId: string): Promise<SandboxBinding[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/sandbox/bindings`);
  }

  async bindSandboxNode(workspaceId: string, data: { node_id: string; policy?: Record<string, unknown> }): Promise<SandboxBinding> {
    return this.fetch(`/api/workspaces/${workspaceId}/sandbox/bindings`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async listSandboxes(): Promise<SandboxInstance[]> {
    return this.fetch("/api/sandboxes");
  }

  async getSandbox(instanceId: string): Promise<SandboxInstance> {
    return this.fetch(`/api/sandboxes/${instanceId}`);
  }

  async createSandbox(data: CreateSandboxRequest): Promise<SandboxInstance> {
    return this.fetch("/api/sandboxes", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateSandbox(instanceId: string, data: UpdateSandboxRequest): Promise<SandboxInstance> {
    return this.fetch(`/api/sandboxes/${instanceId}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async stopSandbox(instanceId: string): Promise<SandboxJob> {
    return this.fetch(`/api/sandboxes/${instanceId}/stop`, { method: "POST" });
  }

  async resumeSandbox(instanceId: string): Promise<SandboxJob> {
    return this.fetch(`/api/sandboxes/${instanceId}/resume`, { method: "POST" });
  }

  async deleteSandbox(instanceId: string): Promise<void> {
    await this.fetch(`/api/sandboxes/${instanceId}`, { method: "DELETE" });
  }

  async createSandboxTemplate(
    instanceId: string,
    data: CreateSandboxSnapshotRequest,
  ): Promise<SandboxSnapshot> {
    const raw = await this.fetch(`/api/sandboxes/${instanceId}/create-template`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, SandboxSnapshotSchema, EMPTY_SANDBOX_SNAPSHOT, {
      endpoint: `POST /api/sandboxes/${instanceId}/create-template`,
    });
  }

  async listSandboxNodeSnapshots(nodeId: string): Promise<SandboxSnapshot[]> {
    const raw = await this.fetch(`/api/sandbox/nodes/${nodeId}/snapshots`);
    return parseWithFallback(raw, SandboxSnapshotListSchema, [], {
      endpoint: `GET /api/sandbox/nodes/${nodeId}/snapshots`,
    });
  }

  async deleteSandboxSnapshot(snapshotId: string): Promise<void> {
    await this.fetch(`/api/sandbox/snapshots/${snapshotId}`, { method: "DELETE" });
  }

  // Members
  async listMembers(workspaceId: string): Promise<MemberWithUser[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/members`);
  }

  /** LRM-462: currently-online human members (offline omitted). */
  async listMemberPresence(workspaceId: string): Promise<MemberPresenceResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/member-presence`);
  }

  async getMemberProfile(memberType: "user" | "agent", memberId: string): Promise<MemberProfile> {
    return this.fetch(`/api/member-profiles/${memberType}/${memberId}`);
  }

  async createMember(workspaceId: string, data: CreateMemberRequest): Promise<Invitation> {
    return this.fetch(`/api/workspaces/${workspaceId}/members`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateMember(workspaceId: string, memberId: string, data: UpdateMemberRequest): Promise<MemberWithUser> {
    return this.fetch(`/api/workspaces/${workspaceId}/members/${memberId}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async deleteMember(workspaceId: string, memberId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/members/${memberId}`, {
      method: "DELETE",
    });
  }

  async leaveWorkspace(workspaceId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/leave`, {
      method: "POST",
    });
  }

  // Invitations
  async listWorkspaceInvitations(workspaceId: string): Promise<Invitation[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/invitations`);
  }

  async revokeInvitation(workspaceId: string, invitationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/invitations/${invitationId}`, {
      method: "DELETE",
    });
  }

  async listMyInvitations(): Promise<Invitation[]> {
    return this.fetch("/api/invitations");
  }

  async getInvitation(invitationId: string): Promise<Invitation> {
    return this.fetch(`/api/invitations/${invitationId}`);
  }

  async acceptInvitation(invitationId: string): Promise<MemberWithUser> {
    return this.fetch(`/api/invitations/${invitationId}/accept`, {
      method: "POST",
    });
  }

  async declineInvitation(invitationId: string): Promise<void> {
    await this.fetch(`/api/invitations/${invitationId}/decline`, {
      method: "POST",
    });
  }

  async deleteWorkspace(workspaceId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}`, {
      method: "DELETE",
    });
  }

  // Skills
  async listSkills(): Promise<SkillSummary[]> {
    return this.fetch("/api/skills");
  }

  async getSkill(id: string): Promise<Skill> {
    return this.fetch(`/api/skills/${id}`);
  }

  async createSkill(data: CreateSkillRequest): Promise<Skill> {
    return this.fetch("/api/skills", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async listPlatformSkills(): Promise<PlatformSkillSummary[]> {
    return this.fetch("/api/skills/platform");
  }

  async installPlatformSkill(name: string): Promise<Skill> {
    return this.fetch(`/api/skills/platform/${encodeURIComponent(name)}/install`, {
      method: "POST",
    });
  }

  async updateSkill(id: string, data: UpdateSkillRequest): Promise<Skill> {
    return this.fetch(`/api/skills/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteSkill(id: string): Promise<void> {
    await this.fetch(`/api/skills/${id}`, { method: "DELETE" });
  }

  async importSkill(data: { url: string }): Promise<Skill> {
    return this.fetch("/api/skills/import", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  /** LRM-954 — promote skill grant level (agent → channel → workspace). */
  async promoteSkill(id: string, data: PromoteSkillRequest): Promise<Skill> {
    return this.fetch(`/api/skills/${id}/promote`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  /** LRM-954 — promotion audit trail for a skill. */
  async listSkillPromotions(id: string): Promise<SkillPromotionsResponse> {
    return this.fetch(`/api/skills/${id}/promotions`);
  }

  async listAgentSkills(agentId: string): Promise<SkillSummary[]> {
    return this.fetch(`/api/members/agents/${agentId}/skills`);
  }

  async listAgentProfileSkills(agentId: string): Promise<{ agentId: string; requestId?: string; global: SkillSummary[]; workspace: SkillSummary[] }> {
    return this.fetch(`/api/agents/${agentId}/skills/profile`);
  }

  async listAgentMemories(agentId: string): Promise<AgentMemory[]> {
    return this.fetch(`/api/members/agents/${agentId}/memories`);
  }

  async listAgentSkillSuggestions(agentId: string): Promise<ListAgentSkillSuggestionsResponse> {
    return this.fetch(`/api/members/agents/${agentId}/skill-suggestions`);
  }

  async decideAgentSkillSuggestion(
    agentId: string,
    suggestionId: string,
    data: DecideAgentSkillSuggestionRequest,
  ): Promise<void> {
    await this.fetch(`/api/members/agents/${agentId}/skill-suggestions/${suggestionId}/decision`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async listEvolutionReviewSubmissions(params?: {
    status?: EvolutionReviewSubmissionStatus;
    limit?: number;
  }): Promise<EvolutionReviewSubmission[]> {
    const search = new URLSearchParams();
    if (params?.status) search.set("status", params.status);
    if (params?.limit) search.set("limit", String(params.limit));
    const suffix = search.toString();
    const raw = await this.fetch<unknown>(`/api/evolution/submissions${suffix ? `?${suffix}` : ""}`);
    return parseWithFallback(
      raw,
      EvolutionReviewSubmissionListSchema,
      EMPTY_EVOLUTION_REVIEW_SUBMISSION_LIST,
      { endpoint: "GET /api/evolution/submissions" },
    );
  }

  async getEvolutionMetrics(params?: { unit_type?: string; days?: number }): Promise<EvolutionMetricsResponse> {
    const search = new URLSearchParams();
    if (params?.unit_type) search.set("unit_type", params.unit_type);
    if (params?.days) search.set("days", String(params.days));
    const suffix = search.toString();
    const raw = await this.fetch<unknown>(`/api/evolution/metrics${suffix ? `?${suffix}` : ""}`);
    return parseWithFallback(raw, EvolutionMetricsSchema, EMPTY_EVOLUTION_METRICS, {
      endpoint: "GET /api/evolution/metrics",
    });
  }

  async listEvolutionTrainingExamples(params?: { model_kind?: string; status?: string; split?: string; limit?: number }): Promise<EvolutionTrainingExampleListResponse> {
    const search = new URLSearchParams();
    if (params?.model_kind) search.set("model_kind", params.model_kind);
    if (params?.status) search.set("status", params.status);
    if (params?.split) search.set("split", params.split);
    if (params?.limit) search.set("limit", String(params.limit));
    const suffix = search.toString();
    const raw = await this.fetch<unknown>(`/api/evolution/training/examples${suffix ? `?${suffix}` : ""}`);
    return parseWithFallback(raw, EvolutionTrainingExampleListSchema, EMPTY_EVOLUTION_TRAINING_EXAMPLE_LIST, {
      endpoint: "GET /api/evolution/training/examples",
    });
  }

  async createEvolutionTrainingExample(body: EvolutionTrainingExampleCreateRequest): Promise<EvolutionTrainingExample> {
    const raw = await this.fetch<unknown>("/api/evolution/training/examples", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return parseWithFallback(raw, EvolutionTrainingExampleSchema, {} as EvolutionTrainingExample, {
      endpoint: "POST /api/evolution/training/examples",
    });
  }

  async updateEvolutionTrainingExample(exampleId: string, body: EvolutionTrainingExampleUpdateRequest): Promise<EvolutionTrainingExample> {
    const raw = await this.fetch<unknown>(`/api/evolution/training/examples/${encodeURIComponent(exampleId)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return parseWithFallback(raw, EvolutionTrainingExampleSchema, {} as EvolutionTrainingExample, {
      endpoint: "PATCH /api/evolution/training/examples/:id",
    });
  }

  async listEvolutionModelConfigs(): Promise<EvolutionModelRuntimeConfigListResponse> {
    const raw = await this.fetch<unknown>("/api/evolution/model-configs");
    return parseWithFallback(raw, EvolutionModelRuntimeConfigListSchema, EMPTY_EVOLUTION_MODEL_RUNTIME_CONFIG_LIST, {
      endpoint: "GET /api/evolution/model-configs",
    });
  }

  async updateEvolutionModelConfig(modelKind: string, body: EvolutionModelRuntimeConfigUpdateRequest): Promise<EvolutionModelRuntimeConfig> {
    const raw = await this.fetch<unknown>(`/api/evolution/model-configs/${encodeURIComponent(modelKind)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return parseWithFallback(raw, EvolutionModelRuntimeConfigSchema, {} as EvolutionModelRuntimeConfig, {
      endpoint: "PUT /api/evolution/model-configs/:modelKind",
    });
  }

  async listEvolutionModelEvalRuns(params?: { model_kind?: string; limit?: number }): Promise<EvolutionModelEvalRunListResponse> {
    const search = new URLSearchParams();
    if (params?.model_kind) search.set("model_kind", params.model_kind);
    if (params?.limit) search.set("limit", String(params.limit));
    const suffix = search.toString();
    const raw = await this.fetch<unknown>(`/api/evolution/model-evals${suffix ? `?${suffix}` : ""}`);
    return parseWithFallback(raw, EvolutionModelEvalRunListSchema, EMPTY_EVOLUTION_MODEL_EVAL_RUN_LIST, {
      endpoint: "GET /api/evolution/model-evals",
    });
  }

  async createEvolutionModelEvalRun(body: EvolutionModelEvalRunCreateRequest): Promise<EvolutionModelEvalRun> {
    const raw = await this.fetch<unknown>("/api/evolution/model-evals", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return parseWithFallback(raw, EvolutionModelEvalRunSchema, {} as EvolutionModelEvalRun, {
      endpoint: "POST /api/evolution/model-evals",
    });
  }

  async getWorkspaceMemoryCurationStatus(workspaceId: string): Promise<WorkspaceMemoryCurationStatus> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/status`,
    );
    return parseWithFallback(
      raw,
      WorkspaceMemoryCurationStatusSchema,
      EMPTY_WORKSPACE_MEMORY_CURATION_STATUS,
      { endpoint: "GET /api/workspaces/{id}/memory-curation/status" },
    );
  }

  async getMemoryCuratorProfile(workspaceId: string): Promise<MemoryCuratorProfile> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/profile`,
    );
    return parseWithFallback(raw, MemoryCuratorProfileSchema, EMPTY_MEMORY_CURATOR_PROFILE, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/profile",
    });
  }

  async getMemoryCurationRun(workspaceId: string, runId: string): Promise<MemoryCurationRunDetail> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/runs/${encodeURIComponent(runId)}`,
    );
    return parseWithFallback(raw, MemoryCurationRunDetailSchema, EMPTY_MEMORY_CURATION_RUN_DETAIL, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/runs/{runId}",
    });
  }

  async updateMemoryCuratorProfile(
    workspaceId: string,
    data: UpdateMemoryCuratorProfileRequest,
  ): Promise<MemoryCuratorProfile> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/profile`,
      { method: "PUT", body: JSON.stringify(data) },
    );
    return parseWithFallback(raw, MemoryCuratorProfileSchema, EMPTY_MEMORY_CURATOR_PROFILE, {
      endpoint: "PUT /api/workspaces/{id}/memory-curation/profile",
    });
  }

  async getGraphMemoryProfile(workspaceId: string): Promise<GraphMemoryProfile> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/profile`,
    );
    return parseWithFallback(raw, GraphMemoryProfileSchema, EMPTY_GRAPH_MEMORY_PROFILE, {
      endpoint: "GET /api/workspaces/{id}/graph-memory/profile",
    });
  }

  async updateGraphMemoryProfile(
    workspaceId: string,
    data: UpdateGraphMemoryProfileRequest,
  ): Promise<GraphMemoryProfile> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/profile`,
      { method: "PUT", body: JSON.stringify(data) },
    );
    return parseWithFallback(raw, GraphMemoryProfileSchema, EMPTY_GRAPH_MEMORY_PROFILE, {
      endpoint: "PUT /api/workspaces/{id}/graph-memory/profile",
    });
  }

  async getGraphMemoryChannelMode(workspaceId: string, channelId: string): Promise<GraphMemoryChannelMode> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/channels/${encodeURIComponent(channelId)}/mode`,
    );
    return parseWithFallback(raw, GraphMemoryChannelModeSchema, {
      workspace_id: workspaceId, channel_id: channelId, override: "inherit", effective_mode: "agent",
      status: "inactive", blocked_reason: "", agent_id: "", runtime_id: "",
    }, { endpoint: "GET /api/workspaces/{id}/graph-memory/channels/{channelId}/mode" });
  }

  async updateGraphMemoryChannelMode(workspaceId: string, channelId: string, override: "inherit" | "inject" | "agent"): Promise<GraphMemoryChannelMode> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/channels/${encodeURIComponent(channelId)}/mode`,
      { method: "PUT", body: JSON.stringify({ override }) },
    );
    return parseWithFallback(raw, GraphMemoryChannelModeSchema, {
      workspace_id: workspaceId, channel_id: channelId, override, effective_mode: override === "inherit" ? "agent" : override,
      status: "inactive", blocked_reason: "", agent_id: "", runtime_id: "",
    }, { endpoint: "PUT /api/workspaces/{id}/graph-memory/channels/{channelId}/mode" });
  }

  async resetGraphMemoryChannelAgent(workspaceId: string, channelId: string): Promise<void> {
    await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/channels/${encodeURIComponent(channelId)}/reset`,
      { method: "POST", body: JSON.stringify({}) },
    );
  }

  async getGraphMemoryMessageCitations(workspaceId: string, messageId: string): Promise<GraphMemoryMessageCitations> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/messages/${encodeURIComponent(messageId)}/citations`,
    );
    return parseWithFallback(raw, GraphMemoryMessageCitationsSchema, {
      message_id: messageId, items: [],
    }, { endpoint: "GET /api/workspaces/{id}/graph-memory/messages/{messageId}/citations" });
  }

  async getGraphMemoryStatus(workspaceId: string): Promise<GraphMemoryStatus> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/status`,
    );
    return parseWithFallback(raw, GraphMemoryStatusSchema, EMPTY_GRAPH_MEMORY_STATUS, {
      endpoint: "GET /api/workspaces/{id}/graph-memory/status",
    });
  }

  async getGraphMemoryAudit(workspaceId: string): Promise<GraphMemoryAuditSummary> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/audit`,
    );
    return parseWithFallback(raw, GraphMemoryAuditSummarySchema, EMPTY_GRAPH_MEMORY_AUDIT, {
      endpoint: "GET /api/workspaces/{id}/graph-memory/audit",
    });
  }

  async getGraphMemoryChannelLineage(workspaceId: string, channelId: string): Promise<GraphMemoryChannelLineage> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/channels/${encodeURIComponent(channelId)}/lineage`,
    );
    return parseWithFallback(raw, GraphMemoryChannelLineageSchema, {
      workspace_id: workspaceId, channel_id: channelId, routing_mode: "", current: null, lineage: [],
    }, { endpoint: "GET /api/workspaces/{id}/graph-memory/channels/{channelId}/lineage" });
  }

  async startGraphMemoryConsolidation(workspaceId: string): Promise<{ id: string; status: string }> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/consolidations`,
      { method: "POST", body: JSON.stringify({}) },
    );
    return parseWithFallback(raw, GraphMemoryConsolidationRunSchema, EMPTY_GRAPH_MEMORY_CONSOLIDATION_RUN, {
      endpoint: "POST /api/workspaces/{id}/graph-memory/consolidations",
    });
  }

  async listGraphMemoryConsolidations(workspaceId: string): Promise<GraphMemoryConsolidationRun[]> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/graph-memory/consolidations`,
    );
    const parsed = parseWithFallback(raw, GraphMemoryConsolidationListSchema, { runs: [] }, {
      endpoint: "GET /api/workspaces/{id}/graph-memory/consolidations",
    });
    return parsed.runs;
  }

  async startMemoryCurationRun(
    workspaceId: string,
    data: StartMemoryCurationRunRequest,
  ): Promise<StartMemoryCurationRunResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/runs`,
      { method: "POST", body: JSON.stringify(data) },
    );
    return parseWithFallback(raw, StartMemoryCurationRunResponseSchema, { id: "", status: "failed" }, {
      endpoint: "POST /api/workspaces/{id}/memory-curation/runs",
    });
  }

  async previewMemoryCurationBackfill(
    workspaceId: string,
    params: { since?: string; until?: string } = {},
  ): Promise<MemoryCurationBackfillResponse> {
    const query = new URLSearchParams();
    if (params.since) query.set("since", params.since);
    if (params.until) query.set("until", params.until);
    const suffix = query.toString() ? `?${query.toString()}` : "";
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/backfill-preview${suffix}`,
    );
    return parseWithFallback(raw, MemoryCurationBackfillResponseSchema, EMPTY_MEMORY_CURATION_BACKFILL_RESPONSE, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/backfill-preview",
    });
  }

  async startMemoryCurationBackfill(
    workspaceId: string,
    data: MemoryCurationBackfillRequest,
  ): Promise<MemoryCurationBackfillResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/backfill`,
      { method: "POST", body: JSON.stringify(data) },
    );
    return parseWithFallback(raw, MemoryCurationBackfillResponseSchema, EMPTY_MEMORY_CURATION_BACKFILL_RESPONSE, {
      endpoint: "POST /api/workspaces/{id}/memory-curation/backfill",
    });
  }

  async getMemoryCurationDailySummary(
    workspaceId: string,
    params: { since?: string; until?: string; timezone?: string } = {},
  ): Promise<MemoryCurationDailySummaryResponse> {
    const query = new URLSearchParams();
    if (params.since) query.set("since", params.since);
    if (params.until) query.set("until", params.until);
    if (params.timezone) query.set("timezone", params.timezone);
    const suffix = query.toString() ? `?${query.toString()}` : "";
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/daily-summary${suffix}`,
    );
    return parseWithFallback(raw, MemoryCurationDailySummaryResponseSchema, EMPTY_MEMORY_CURATION_DAILY_SUMMARY, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/daily-summary",
    });
  }

  async listMemoryCurationCandidates(
    workspaceId: string,
    params: { date: string; kind?: "memory" | "skill" | "all"; status?: string; limit?: number; timezone?: string },
  ): Promise<MemoryCurationCandidateListResponse> {
    const query = new URLSearchParams();
    query.set("date", params.date);
    if (params.kind) query.set("kind", params.kind);
    if (params.status) query.set("status", params.status);
    if (params.limit) query.set("limit", String(params.limit));
    if (params.timezone) query.set("timezone", params.timezone);
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/candidates?${query.toString()}`,
    );
    return parseWithFallback(raw, MemoryCurationCandidateListResponseSchema, EMPTY_MEMORY_CURATION_CANDIDATE_LIST, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/candidates",
    });
  }

  async getMemoryCurationCandidate(
    workspaceId: string,
    candidateId: string,
  ): Promise<MemoryCurationCandidateItem> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/candidates/${encodeURIComponent(candidateId)}`,
    );
    return parseWithFallback(raw, MemoryCurationCandidateItemSchema, {
      id: "",
      candidate_type: "",
      scope: "",
      title: "",
      snippet: "",
      confidence: 0,
      status: "",
      created_at: "",
    }, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/candidates/{candidateId}",
    });
  }

  async listTeamKnowledgeItems(
    workspaceId: string,
    params: { date?: string; kind?: string; limit?: number; timezone?: string; include_content?: boolean } = {},
  ): Promise<TeamKnowledgeListResponse> {
    const query = new URLSearchParams();
    if (params.date) query.set("date", params.date);
    if (params.kind) query.set("kind", params.kind);
    if (params.limit) query.set("limit", String(params.limit));
    if (params.timezone) query.set("timezone", params.timezone);
    if (params.include_content) query.set("include_content", "true");
    const suffix = query.toString() ? `?${query.toString()}` : "";
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/team-knowledge${suffix}`,
    );
    return parseWithFallback(raw, TeamKnowledgeListResponseSchema, EMPTY_TEAM_KNOWLEDGE_LIST, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/team-knowledge",
    });
  }

  async getTeamKnowledgeItem(
    workspaceId: string,
    itemId: string,
  ): Promise<TeamKnowledgeListItem> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/team-knowledge/${encodeURIComponent(itemId)}`,
    );
    return parseWithFallback(raw, TeamKnowledgeListItemSchema, {
      id: "",
      kind: "",
      title: "",
      snippet: "",
      status: "",
      created_at: "",
    }, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/team-knowledge/{itemId}",
    });
  }

  async listKnowledgeNeighbors(
    workspaceId: string,
    itemId: string,
    hops: 1 | 2 = 1,
  ): Promise<KnowledgeNeighborsResponse> {
    const query = hops === 2 ? "?hops=2" : "?hops=1";
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/memory-curation/team-knowledge/${encodeURIComponent(itemId)}/neighbors${query}`,
    );
    return parseWithFallback(raw, KnowledgeNeighborsResponseSchema, EMPTY_KNOWLEDGE_NEIGHBORS, {
      endpoint: "GET /api/workspaces/{id}/memory-curation/team-knowledge/{itemId}/neighbors",
    });
  }

  async promoteKnowledgePage(
    workspaceId: string,
    data: PromoteKnowledgeRequest,
  ): Promise<PromoteKnowledgeResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/knowledge/promote`,
      {
        method: "POST",
        body: JSON.stringify(data),
      },
    );
    return parseWithFallback(raw, PromoteKnowledgeResponseSchema, {
      id: "",
      kind: "",
      title: "",
      content: "",
      status: "",
      edges: [],
      created_at: "",
    }, {
      endpoint: "POST /api/workspaces/{id}/knowledge/promote",
    });
  }

  async getEvolutionReviewSubmission(id: string): Promise<EvolutionReviewSubmission | null> {
    const raw = await this.fetch<unknown>(`/api/evolution/submissions/${id}`);
    return parseWithFallback(raw, EvolutionReviewSubmissionSchema, null, {
      endpoint: "GET /api/evolution/submissions/{id}",
    });
  }

  async promoteEvolutionReviewSubmission(
    id: string,
    data: EvolutionReviewDecisionRequest = {},
  ): Promise<PromoteEvolutionReviewSubmissionResponse> {
    return this.fetch(`/api/evolution/submissions/${id}/promote`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async setEvolutionSourceSkillAssignment(id: string, enabled: boolean): Promise<void> {
    await this.fetch(`/api/evolution/submissions/${id}/source-skill`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    });
  }

  async rejectEvolutionReviewSubmission(
    id: string,
    data: EvolutionReviewDecisionRequest = {},
  ): Promise<EvolutionReviewSubmission | null> {
    const raw = await this.fetch<unknown>(`/api/evolution/submissions/${id}/reject`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, EvolutionReviewSubmissionSchema, null, {
      endpoint: "POST /api/evolution/submissions/{id}/reject",
    });
  }

  async setAgentSkills(agentId: string, data: SetAgentSkillsRequest): Promise<void> {
    await this.fetch(`/api/members/agents/${agentId}/skills`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // Personal Access Tokens
  async listPersonalAccessTokens(): Promise<PersonalAccessToken[]> {
    return this.fetch("/api/tokens");
  }

  async createPersonalAccessToken(data: CreatePersonalAccessTokenRequest): Promise<CreatePersonalAccessTokenResponse> {
    return this.fetch("/api/tokens", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async revokePersonalAccessToken(id: string): Promise<void> {
    await this.fetch(`/api/tokens/${id}`, { method: "DELETE" });
  }

  // File Upload & Attachments
  async uploadFile(
    file: File,
    opts?: { issueId?: string; commentId?: string; chatSessionId?: string; channelId?: string },
  ): Promise<Attachment> {
    const formData = new FormData();
    formData.append("file", file);
    if (opts?.issueId) formData.append("issue_id", opts.issueId);
    if (opts?.commentId) formData.append("comment_id", opts.commentId);
    if (opts?.chatSessionId) formData.append("chat_session_id", opts.chatSessionId);
    if (opts?.channelId) formData.append("channel_id", opts.channelId);

    const rid = createRequestId();
    const start = Date.now();
    this.logger.info("→ POST /api/upload-file", { rid });

    const res = await fetch(`${this.baseUrl}/api/upload-file`, {
      method: "POST",
      headers: this.authHeaders(),
      body: formData,
      credentials: "include",
    });

    if (!res.ok) {
      if (res.status === 401) this.handleUnauthorized();
      const message = await this.parseErrorMessage(res, `Upload failed: ${res.status}`);
      this.logger.error(`← ${res.status} /api/upload-file`, { rid, duration: `${Date.now() - start}ms`, error: message });
      throw new Error(message);
    }

    this.logger.info(`← ${res.status} /api/upload-file`, { rid, duration: `${Date.now() - start}ms` });
    // Strict validation (LRM-238 / LRM-426): do not parseWithFallback to
    // EMPTY_ATTACHMENT — an empty id looks like success to callers that only
    // check res.ok, then surfaces as a silent "Upload failed" chip. Mobile
    // already throws on shape mismatch for the same reason.
    const raw = (await res.json()) as unknown;
    const parsed = AttachmentResponseSchema.safeParse(raw);
    if (!parsed.success) {
      this.logger.error(`← shape mismatch /api/upload-file`, {
        rid,
        duration: `${Date.now() - start}ms`,
        issues: parsed.error.issues,
      });
      throw new Error("Upload response invalid");
    }
    if (!parsed.data.id) {
      throw new Error("Upload response missing attachment id");
    }
    // Response schema is a subset of Attachment; fill remaining fields from
    // EMPTY_ATTACHMENT so callers get a typed Attachment without a unsafe cast.
    return {
      ...EMPTY_ATTACHMENT,
      ...parsed.data,
      chat_session_id: parsed.data.chat_session_id ?? null,
      chat_message_id: parsed.data.chat_message_id ?? null,
    };
  }

  // Chat Sessions
  async listChatSessions(params?: { status?: string }): Promise<ChatSession[]> {
    const query = params?.status ? `?status=${params.status}` : "";
    return this.fetch(`/api/chat/sessions${query}`);
  }

  async getChatSession(id: string): Promise<ChatSession> {
    return this.fetch(`/api/chat/sessions/${id}`);
  }

  async createChatSession(data: {
    agent_id: string;
    title?: string;
    context_note_page_id?: string;
  }): Promise<ChatSession> {
    return this.fetch("/api/chat/sessions", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async deleteChatSession(id: string): Promise<void> {
    await this.fetch(`/api/chat/sessions/${id}`, { method: "DELETE" });
  }

  async updateChatSession(
    id: string,
    data: { title?: string; project_id?: string | null; status?: "active" | "archived" },
  ): Promise<ChatSession> {
    return this.fetch(`/api/chat/sessions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async listChatMessages(sessionId: string): Promise<ChatMessage[]> {
    return this.fetch(`/api/chat/sessions/${sessionId}/messages`);
  }

  async listChatMessagesPage(
    sessionId: string,
    params: { before?: { created_at: string; id: string } | null; limit?: number } = {},
  ): Promise<ChatMessagesPage> {
    const limit = params.limit ?? 50;
    const query = new URLSearchParams({ limit: String(limit) });
    if (params.before) {
      query.set("before_created_at", params.before.created_at);
      query.set("before_id", params.before.id);
    }
    try {
      return await this.fetch(
        `/api/chat/sessions/${sessionId}/messages/page?${query.toString()}`,
      );
    } catch (err) {
      // Deployment-order compatibility: a backend deployed before this endpoint
      // existed returns 404 for the unknown route. Fall back to the legacy
      // full-list endpoint so chat never white-screens regardless of whether
      // the server or the client deploys first. Only the initial (cursorless)
      // page falls back — the legacy endpoint returns every message at once, so
      // the fallback page reports has_more: false and there is no follow-up
      // request to translate. A 404 on a cursor request is an unexpected state
      // and propagates instead of duplicating the whole list.
      if (err instanceof ApiError && err.status === 404 && !params.before) {
        const messages = await this.listChatMessages(sessionId);
        return { messages, limit, has_more: false, next_cursor: null };
      }
      throw err;
    }
  }

  async sendChatMessage(
    sessionId: string,
    content: string,
    attachmentIds?: string[],
    parts?: MessagePart[],
  ): Promise<SendChatMessageResponse> {
    const body: { content: string; attachment_ids?: string[]; parts?: MessagePart[] } = { content };
    if (attachmentIds && attachmentIds.length > 0) {
      body.attachment_ids = attachmentIds;
    }
    if (parts && parts.length > 0) {
      body.parts = parts;
    }
    const raw = await this.fetch(`/api/chat/sessions/${sessionId}/messages`, {
      method: "POST",
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(SEND_TIMEOUT_MS),
    });
    return parseWithFallback(raw, SendChatMessageResponseSchema, EMPTY_SEND_CHAT_MESSAGE_RESPONSE, {
      endpoint: "POST /api/chat/sessions/:id/messages",
    });
  }

  async getPendingChatTask(sessionId: string): Promise<ChatPendingTask> {
    const raw = await this.fetch(`/api/chat/sessions/${sessionId}/pending-task`);
    return parseWithFallback(raw, ChatPendingTaskSchema, EMPTY_CHAT_PENDING_TASK, {
      endpoint: "GET /api/chat/sessions/:id/pending-task",
    });
  }

  async cancelStandaloneChat(sessionId: string): Promise<{ ok: boolean; pending: boolean }> {
    return this.fetch(`/api/chat/sessions/${sessionId}/cancel`, { method: "POST" });
  }

  async listPendingChatTasks(): Promise<PendingChatTasksResponse> {
    const raw = await this.fetch(`/api/chat/pending-tasks`);
    return parseWithFallback(raw, PendingChatTasksResponseSchema, EMPTY_PENDING_CHAT_TASKS_RESPONSE, {
      endpoint: "GET /api/chat/pending-tasks",
    });
  }

  async markChatSessionRead(sessionId: string): Promise<void> {
    await this.fetch(`/api/chat/sessions/${sessionId}/read`, { method: "POST" });
  }

  // ─── Direct messages (1-on-1) ─────────────────────────────────────────────
  // Visible DMs are kind='dm' channels. Legacy chat_sessions may still exist for
  // history migration, but `/api/dm` does not expose them as a Messages source.

  async listDMs(): Promise<DMItem[]> {
    return this.fetch("/api/dm");
  }

  async createOrFindDM(body: CreateOrFindDMBody): Promise<DMItem> {
    return this.fetch("/api/dm", {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  // DM conversation operations (pin / mark-unread / close). All return
  // `{ ok: true }`; callers refetch `/api/dm`.
  private dmOpsPath(source: DMItem["source"], id: string): string {
    void source;
    return `/api/dm/channels/${id}`;
  }

  async pinDM(source: DMItem["source"], id: string): Promise<{ ok: boolean }> {
    return this.fetch(`${this.dmOpsPath(source, id)}/pin`, { method: "PUT" });
  }

  async unpinDM(source: DMItem["source"], id: string): Promise<{ ok: boolean }> {
    return this.fetch(`${this.dmOpsPath(source, id)}/pin`, { method: "DELETE" });
  }

  async muteDM(source: DMItem["source"], id: string): Promise<{ ok: boolean }> {
    return this.fetch(`${this.dmOpsPath(source, id)}/mute`, { method: "PUT" });
  }

  async unmuteDM(source: DMItem["source"], id: string): Promise<{ ok: boolean }> {
    return this.fetch(`${this.dmOpsPath(source, id)}/mute`, { method: "DELETE" });
  }

  async markDMUnread(source: DMItem["source"], id: string): Promise<{ ok: boolean }> {
    return this.fetch(`${this.dmOpsPath(source, id)}/unread`, { method: "POST" });
  }

  /** Close Chat — soft-hides the conversation from the user's list (recoverable). */
  async closeDM(source: DMItem["source"], id: string): Promise<{ ok: boolean }> {
    return this.fetch(this.dmOpsPath(source, id), { method: "DELETE" });
  }

  async listChannels(options?: { archived?: boolean }): Promise<Channel[]> {
    return this.fetch(options?.archived ? "/api/channels?archived=true" : "/api/channels");
  }

  /**
   * LRM-1399 — unified active Conversations list (CHANNELS + DIRECT MESSAGES in
   * one globally-ordered read). Read-only: create/send, detail, membership,
   * preference, and permission routes remain under /api/channels and /api/dm.
   */
  async listConversations(options?: {
    limit?: number;
    cursor?: string;
  }): Promise<ConversationListResponse> {
    const params = new URLSearchParams();
    if (options?.limit) params.set("limit", String(options.limit));
    if (options?.cursor) params.set("cursor", options.cursor);
    const suffix = params.size > 0 ? `?${params.toString()}` : "";
    return this.fetch(`/api/conversations${suffix}`);
  }

  async lookupConversationHandle(handle: string): Promise<ConversationHandleLookup> {
    const params = new URLSearchParams({ handle });
    const raw = await this.fetch<unknown>(`/api/conversations/lookup?${params.toString()}`);
    return parseWithFallback(
      raw,
      ConversationHandleLookupSchema,
      EMPTY_CONVERSATION_HANDLE_LOOKUP,
      { endpoint: "GET /api/conversations/lookup" },
    );
  }

  async createChannel(data: { name: string; description?: string; lark_chat_id?: string; project_id?: string | null }): Promise<Channel> {
    return this.fetch("/api/channels", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateChannel(
    channelId: string,
    data: { name?: string; description?: string | null; lark_chat_id?: string | null; avatar_url?: string | null },
  ): Promise<Channel> {
    return this.fetch(`/api/channels/${channelId}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async deleteChannel(channelId: string): Promise<void> {
    await this.fetch(`/api/channels/${channelId}`, { method: "DELETE" });
  }

  async archiveChannel(channelId: string): Promise<Channel> {
    return this.fetch(`/api/channels/${channelId}/archive`, { method: "POST" });
  }

  async restoreChannel(channelId: string): Promise<Channel> {
    return this.fetch(`/api/channels/${channelId}/restore`, { method: "POST" });
  }

  async pinChannel(channelId: string): Promise<{ ok: boolean }> {
    return this.fetch(`/api/channels/${channelId}/pin`, { method: "PUT" });
  }

  async unpinChannel(channelId: string): Promise<{ ok: boolean }> {
    return this.fetch(`/api/channels/${channelId}/pin`, { method: "DELETE" });
  }

  async muteChannel(channelId: string): Promise<{ ok: boolean }> {
    return this.fetch(`/api/channels/${channelId}/mute`, { method: "PUT" });
  }

  async unmuteChannel(channelId: string): Promise<{ ok: boolean }> {
    return this.fetch(`/api/channels/${channelId}/mute`, { method: "DELETE" });
  }

  /** LRM-748 / LRM-769 — set the viewer's per-channel notify level (four
   *  levels; BE dual-writes `muted_at` for legacy clients). */
  async setChannelNotifyPreference(
    channelId: string,
    level: ChannelNotifyLevel,
  ): Promise<{ ok: boolean; notify_level: ChannelNotifyLevel }> {
    return this.fetch(`/api/channels/${channelId}/notify-preference`, {
      method: "PUT",
      body: JSON.stringify({ level }),
    });
  }

  async markChannelUnread(channelId: string): Promise<{ ok: boolean }> {
    return this.fetch(`/api/channels/${channelId}/unread`, { method: "POST" });
  }

  async listChannelMembers(
    channelId: string,
    options?: { signal?: AbortSignal },
  ): Promise<ChannelMember[]> {
    return this.fetch(`/api/channels/${channelId}/members`, abortInit(options));
  }

  /**
   * LRM-872 / LRM-879 — per-row member-management gates (can_remove includes
   * inviter-of-self-added-agent and workspace admin paths).
   */
  async getChannelMemberManagementCapabilities(
    channelId: string,
    options?: { signal?: AbortSignal },
  ): Promise<ChannelMemberManagementCapabilities> {
    return this.fetch(
      `/api/channels/${channelId}/member-management-capabilities`,
      abortInit(options),
    );
  }

  /** LRM-622 — single invite-picker pool (users + agents), server-filtered. */
  async listChannelInviteCandidates(
    channelId: string,
    params?: { q?: string; limit?: number },
  ): Promise<ChannelInviteCandidatesResponse> {
    const search = new URLSearchParams();
    const q = params?.q?.trim();
    if (q) search.set("q", q);
    if (params?.limit !== undefined) search.set("limit", String(params.limit));
    const suffix = search.size > 0 ? `?${search.toString()}` : "";
    return this.fetch(`/api/channels/${channelId}/invite-candidates${suffix}`);
  }

  async listChannelMentionCandidates(
    channelId: string,
    params?: { q?: string; limit?: number; offset?: number; signal?: AbortSignal },
  ): Promise<ChannelMentionCandidatesResponse> {
    const search = new URLSearchParams();
    const q = params?.q?.trim();
    if (q) search.set("q", q);
    if (params?.limit !== undefined) search.set("limit", String(params.limit));
    if (params?.offset !== undefined) search.set("offset", String(params.offset));
    const suffix = search.size > 0 ? `?${search.toString()}` : "";
    const raw = await this.fetch<unknown>(
      `/api/channels/${channelId}/mention-candidates${suffix}`,
      abortInit({ signal: params?.signal }),
    );
    return parseWithFallback(raw, ChannelMentionCandidatesResponseSchema, EMPTY_CHANNEL_MENTION_CANDIDATES, {
      endpoint: "GET /api/channels/{id}/mention-candidates",
    });
  }

  // Group-local Tasks projection. The channel is a discussion context, not an
  // Issue owner: this only returns issues anchored to a source message there.
  async listChannelSourceIssues(
    channelId: string,
    params: Pick<ListIssuesParams, "status" | "assignee_id" | "limit" | "offset"> = {},
  ): Promise<ListIssuesResponse> {
    const search = new URLSearchParams();
    if (params.status) search.set("status", params.status);
    if (params.assignee_id) search.set("assignee_id", params.assignee_id);
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.offset !== undefined) search.set("offset", String(params.offset));
    const suffix = search.size > 0 ? `?${search.toString()}` : "";
    return this.fetch(`/api/channels/${channelId}/issues${suffix}`);
  }

  async addChannelMembers(
    channelId: string,
    members: { member_type: "user" | "agent"; member_id: string }[],
  ): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/members/batch`, {
      method: "POST",
      body: JSON.stringify({ members }),
    });
  }

  async addChannelMember(channelId: string, data: { member_type: "user" | "agent"; member_id: string }): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/members`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async removeChannelMember(channelId: string, memberType: "user" | "agent", memberId: string): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/members/${memberType}/${memberId}`, { method: "DELETE" });
  }

  /**
   * Owner-only member role change (#832 / #814). Handles promote, demote, and
   * ownership transfer — the server distinguishes them by the target `role`.
   *
   * Failure codes the caller must keep apart (server contract, #1321/#1326):
   *   403 + `code: "owner_changed"` — someone else took ownership mid-flight.
   *     A PLAIN 403 deliberately carries no code, and the two must not be
   *     collapsed: one means "refresh, the world moved", the other means
   *     "you may not do this".
   *   409 — currently only "sole owner must transfer first". Keyed on status
   *     alone because that message has no stable code yet; see
   *     `updateChannelMemberRole` in channels/mutations.ts for why the UI does
   *     NOT name that reason.
   */
  /**
   * Ownership transfer has its OWN route (#814). The member-role PATCH above
   * explicitly rejects `role: "owner"` with 400 "use POST
   * .../transfer-ownership" (channel.go:1761) — so transfer is a different
   * request, not a different value.
   */
  async transferChannelOwnership(
    channelId: string,
    memberType: "user" | "agent",
    memberId: string,
  ): Promise<void> {
    await this.fetch(
      `/api/channels/${channelId}/members/${memberType}/${memberId}/transfer-ownership`,
      { method: "POST" },
    );
  }

  async updateChannelMemberRole(
    channelId: string,
    memberType: "user" | "agent",
    memberId: string,
    // NOT ChannelMemberRole: the server rejects "owner" here and routes it to
    // transferChannelOwnership above.
    role: "manager" | "member",
  ): Promise<{ role: ChannelMemberRole }> {
    return this.fetch(`/api/channels/${channelId}/members/${memberType}/${memberId}`, {
      method: "PATCH",
      body: JSON.stringify({ role }),
    });
  }

  async listChannelMessages(
    channelId: string,
    options?: { signal?: AbortSignal },
  ): Promise<ChannelMessage[]> {
    const page = await this.listChannelMessagesPage(channelId, options);
    return page.messages;
  }

  async listChannelMessagesPage(
    channelId: string,
    options: {
      limit?: number;
      before?: { seq?: number; created_at: string; id: string } | null;
      /**
       * Anchor sequence for a centered ("around") page (task #340). Loads a
       * window straddling this seq — the unread cursor on cold-open, or a
       * deep-link/search target. Mutually exclusive with `before` (the server
       * rejects both); when set, `before` is ignored.
       */
      around?: number | null;
      /** React Query's cancellation signal — see {@link abortInit} (LRM-1296). */
      signal?: AbortSignal;
    } = {},
  ): Promise<ChannelMessagesPage> {
    const limit = options.limit ?? 50;
    const params = new URLSearchParams({ limit: String(limit) });
    if (options.around != null) {
      params.set("around_seq", String(options.around));
    } else if (options.before) {
      if (typeof options.before.seq === "number") {
        params.set("before_seq", String(options.before.seq));
      } else {
        params.set("before_created_at", options.before.created_at);
        params.set("before_id", options.before.id);
      }
    }
    const raw = await this.fetch<unknown>(
      `/api/channels/${channelId}/messages?${params.toString()}`,
      abortInit(options),
    );
    return parseWithFallback(
      raw,
      ChannelMessagesPageSchema,
      { ...EMPTY_CHANNEL_MESSAGES_PAGE, limit },
      { endpoint: "GET /api/channels/{channelId}/messages" },
    );
  }

  async listChannelMessageThread(
    channelId: string,
    messageId: string,
    options?: { limit?: number; beforeSeq?: number; before?: string; beforeId?: string; signal?: AbortSignal },
  ): Promise<ChannelThreadMessagesPage> {
    const params = new URLSearchParams();
    if (options?.limit) {
      params.set("limit", String(options.limit));
    }
    if (typeof options?.beforeSeq === "number") {
      params.set("before_seq", String(options.beforeSeq));
    } else if (options?.before && options?.beforeId) {
      params.set("before", options.before);
      params.set("before_id", options.beforeId);
    }
    const suffix = params.toString();
    const raw = await this.fetch<unknown>(
      `/api/channels/${channelId}/messages/${messageId}/thread${suffix ? `?${suffix}` : ""}`,
      abortInit(options),
    );
    return parseWithFallback(
      raw,
      ChannelThreadMessagesPageSchema,
      EMPTY_CHANNEL_THREAD_MESSAGES_PAGE,
      { endpoint: "GET /api/channels/{channelId}/messages/{messageId}/thread" },
    );
  }

  async searchChannelMessages(
    channelId: string,
    queryOrParams: string | ChannelMessageSearchParams,
    limit?: number,
  ): Promise<ChannelMessageSearchResponse> {
    const params = new URLSearchParams();
    if (typeof queryOrParams === "string") {
      params.set("q", queryOrParams);
      if (limit) params.set("limit", String(limit));
    } else {
      if (queryOrParams.q) params.set("q", queryOrParams.q);
      if (queryOrParams.author_id) params.set("author_id", queryOrParams.author_id);
      if (queryOrParams.author_type) params.set("author_type", queryOrParams.author_type);
      if (queryOrParams.include_thread === false) params.set("include_thread", "false");
      if (queryOrParams.limit) params.set("limit", String(queryOrParams.limit));
      if (queryOrParams.offset != null) params.set("offset", String(queryOrParams.offset));
    }
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/messages/search?${params.toString()}`);
    return parseWithFallback(raw, ChannelMessageSearchResponseSchema, EMPTY_CHANNEL_MESSAGE_SEARCH_RESPONSE, {
      endpoint: "GET /api/channels/{channelId}/messages/search",
    });
  }

  /**
   * Workspace-level global search (LRM-605 / `GET /api/search`). Returns
   * collaboration content (Messages/Channels/DMs/People) within the viewer's
   * permission boundary.
   *
   * Authorization (LRM-606 AC#3 option b, aligned to Slack): the server filters
   * to only content the viewer can see (SQL `JOIN channel_member viewer`).
   * There is no `denied` flag — a query matching only hidden content returns
   * an empty result (not faked as anything else); whole-request auth failure
   * surfaces as a 401/403 error (retryable, no silent fallback, LRM-238).
   */
  async searchWorkspace(
    // workspaceId is retained for caller-side query-key isolation but is NOT
    // part of the request URL: the server resolves the workspace from the
    // auth context (ctxWorkspaceID). See LRM-605 / channel.go SearchGlobal.
    _workspaceId: string,
    params: {
      q?: string;
      scope?: WorkspaceSearchScope;
      limit?: number;
      /** LRM-874 from:@ — with scope=messages, q may be omitted. */
      author_type?: "user" | "agent";
      author_id?: string;
      include_thread?: boolean;
      signal?: AbortSignal;
    },
  ): Promise<WorkspaceSearchResponse> {
    const search = new URLSearchParams();
    if (params.q) search.set("q", params.q);
    if (params.scope) search.set("scope", params.scope);
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.author_type) search.set("author_type", params.author_type);
    if (params.author_id) search.set("author_id", params.author_id);
    if (params.include_thread === false) search.set("include_thread", "false");
    const raw = await this.fetch<unknown>(
      `/api/search?${search.toString()}`,
      params.signal ? { signal: params.signal } : undefined,
    );
    return parseWithFallback(raw, WorkspaceSearchResponseSchema, EMPTY_WORKSPACE_SEARCH_RESPONSE, {
      endpoint: "GET /api/search",
    });
  }

  async transcribeVoice(pcm: ArrayBuffer): Promise<string> {
    const res = await this.fetchRaw("/api/voice/asr", {
      method: "POST",
      body: pcm,
      extraHeaders: { "Content-Type": "audio/pcm; rate=16000" },
    });
    let raw: unknown;
    try {
      raw = await res.json() as unknown;
    } catch {
      raw = undefined;
    }
    const parsed = parseWithFallback<{ text: string }>(
      raw,
      VoiceTranscriptResponseSchema,
      EMPTY_VOICE_TRANSCRIPT_RESPONSE,
      { endpoint: "POST /api/voice/asr" },
    );
    if (parsed === EMPTY_VOICE_TRANSCRIPT_RESPONSE) {
      throw new ApiError("voice service returned an invalid transcript response", 502, "Bad Gateway");
    }
    return parsed.text.trim();
  }

  async synthesizeVoice(text: string): Promise<{
    audio: ArrayBuffer;
    durationMs: number | null;
  }> {
    const res = await this.fetchRaw("/api/voice/tts", {
      method: "POST",
      body: JSON.stringify({ text }),
      extraHeaders: { "Content-Type": "application/json" },
    });
    const contentType = res.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
    if (contentType !== "audio/wav") {
      throw new ApiError("voice service returned an invalid audio response", 502, "Bad Gateway");
    }
    const rawDuration = res.headers.get("X-Voice-Duration-Ms")?.trim() ?? "";
    const parsedDuration = Number.parseInt(rawDuration, 10);
    return {
      audio: await res.arrayBuffer(),
      durationMs: Number.isFinite(parsedDuration) && parsedDuration > 0
        ? parsedDuration
        : null,
    };
  }

  async createVoiceCall(
    workspaceId: string,
    input: CreateVoiceCallRequest,
  ): Promise<CreateVoiceCallResponse> {
    const path = `/api/workspaces/${encodeURIComponent(workspaceId)}/voice-calls`;
    const raw = await this.fetch<unknown>(path, {
      method: "POST",
      body: JSON.stringify(input),
    });
    const parsed = parseWithFallback<CreateVoiceCallResponse>(
      raw,
      CreateVoiceCallResponseSchema,
      EMPTY_CREATE_VOICE_CALL_RESPONSE,
      {
        endpoint: "POST /api/workspaces/{workspaceId}/voice-calls",
        receivedForLog: "[redacted voice call response]",
      },
    );
    if (parsed === EMPTY_CREATE_VOICE_CALL_RESPONSE) {
      throw new ApiError(
        "voice call service returned invalid media credentials",
        502,
        "Bad Gateway",
      );
    }
    return parsed;
  }

  async getVoiceCall(
    workspaceId: string,
    callId: string,
  ): Promise<GetVoiceCallResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/voice-calls/${encodeURIComponent(callId)}`,
    );
    return parseWithFallback(
      raw,
      GetVoiceCallResponseSchema,
      EMPTY_GET_VOICE_CALL_RESPONSE,
      { endpoint: "GET /api/workspaces/{workspaceId}/voice-calls/{callId}" },
    );
  }

  async connectVoiceCall(
    workspaceId: string,
    callId: string,
  ): Promise<GetVoiceCallResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/voice-calls/${encodeURIComponent(callId)}/connect`,
      { method: "POST" },
    );
    return parseWithFallback(
      raw,
      GetVoiceCallResponseSchema,
      EMPTY_GET_VOICE_CALL_RESPONSE,
      { endpoint: "POST /api/workspaces/{workspaceId}/voice-calls/{callId}/connect" },
    );
  }

  async answerVoiceCall(
    workspaceId: string,
    callId: string,
  ): Promise<GetVoiceCallResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/voice-calls/${encodeURIComponent(callId)}/answer`,
      { method: "POST" },
    );
    return parseWithFallback(
      raw,
      GetVoiceCallResponseSchema,
      EMPTY_GET_VOICE_CALL_RESPONSE,
      { endpoint: "POST /api/workspaces/{workspaceId}/voice-calls/{callId}/answer" },
    );
  }

  async stopVoiceCall(
    workspaceId: string,
    callId: string,
  ): Promise<GetVoiceCallResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/voice-calls/${encodeURIComponent(callId)}/stop`,
      { method: "POST" },
    );
    return parseWithFallback(
      raw,
      GetVoiceCallResponseSchema,
      EMPTY_GET_VOICE_CALL_RESPONSE,
      { endpoint: "POST /api/workspaces/{workspaceId}/voice-calls/{callId}/stop" },
    );
  }

  async startVoiceCallDuplex(
    workspaceId: string,
    callId: string,
  ): Promise<StartVoiceCallDuplexResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/voice-calls/${encodeURIComponent(callId)}/duplex`,
      { method: "POST" },
    );
    const parsed = parseWithFallback(
      raw,
      StartVoiceCallDuplexResponseSchema,
      EMPTY_START_VOICE_CALL_DUPLEX_RESPONSE,
      {
        endpoint: "POST /api/workspaces/{workspaceId}/voice-calls/{callId}/duplex",
        receivedForLog: "[redacted duplex voice call response]",
      },
    );
    if (parsed === EMPTY_START_VOICE_CALL_DUPLEX_RESPONSE) {
      throw new ApiError(
        "voice call duplex service returned an invalid response",
        502,
        "Bad Gateway",
      );
    }
    return parsed;
  }

  voiceCallDuplexWsUrl(workspaceId: string, callId: string): string {
    return voiceCallDuplexWsUrl(this.baseUrl, workspaceId, callId);
  }

  async sendChannelMessage(
    channelId: string,
    input: {
      content: string;
      parts?: MessagePart[];
      replyToMessageId?: string | null;
      clientMessageId?: string | null;
      quote?: ChannelMessageQuoteInput | null;
    },
  ): Promise<ChannelMessage> {
    // Channel attachments bind from structured `parts` (type: "attachment").
    // Do not send `attachment_ids` on this path — issue/comment keep that field.
    const body: {
      content: string;
      reply_to_message_id?: string;
      quote?: { message_id: string; selected_text?: string };
      parts?: MessagePart[];
      client_message_id?: string;
    } = { content: input.content };
    if (input.replyToMessageId) {
      body.reply_to_message_id = input.replyToMessageId;
    }
    if (input.quote) {
      body.quote = { message_id: input.quote.messageId };
      if (input.quote.selectedText) {
        body.quote.selected_text = input.quote.selectedText;
      }
    }
    if (input.parts && input.parts.length > 0) {
      body.parts = input.parts;
    }
    if (input.clientMessageId) {
      body.client_message_id = input.clientMessageId;
    }
    return this.fetch(`/api/channels/${channelId}/messages`, {
      method: "POST",
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(SEND_TIMEOUT_MS),
    });
  }

  async editChannelMessage(
    channelId: string,
    messageId: string,
    content: string,
    parts?: MessagePart[],
  ): Promise<ChannelMessage> {
    const body: { content: string; parts?: MessagePart[] } = { content };
    if (parts && parts.length > 0) {
      body.parts = parts;
    }
    return this.fetch(`/api/channels/${channelId}/messages/${messageId}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    });
  }

  async addChannelReaction(channelId: string, messageId: string, emoji: string): Promise<ChannelReaction> {
    return this.fetch(`/api/channels/${channelId}/messages/${messageId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    });
  }

  async removeChannelReaction(channelId: string, messageId: string, emoji: string): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/messages/${messageId}/reactions`, {
      method: "DELETE",
      body: JSON.stringify({ emoji }),
    });
  }

  async chooseChannelMessageOption(
    channelId: string,
    messageId: string,
    choiceId: string,
    optionId: string,
  ): Promise<{ message: ChannelMessage; reply?: ChannelMessage }> {
    return this.fetch(`/api/channels/${channelId}/messages/${messageId}/choice`, {
      method: "POST",
      body: JSON.stringify({ choice_id: choiceId, option_id: optionId }),
      signal: AbortSignal.timeout(SEND_TIMEOUT_MS),
    });
  }

  async sendChannelThreadMessage(
    channelId: string,
    messageId: string,
    input: {
      content: string;
      parts?: MessagePart[];
      replyToMessageId?: string | null;
      clientMessageId?: string | null;
      quote?: ChannelMessageQuoteInput | null;
    },
  ): Promise<ChannelMessage> {
    // Same as sendChannelMessage: attachment truth is `parts`, not attachment_ids.
    const body: {
      content: string;
      reply_to_message_id?: string;
      quote?: { message_id: string; selected_text?: string };
      parts?: MessagePart[];
      client_message_id?: string;
    } = { content: input.content };
    if (input.replyToMessageId) {
      body.reply_to_message_id = input.replyToMessageId;
    }
    if (input.quote) {
      body.quote = { message_id: input.quote.messageId };
      if (input.quote.selectedText) {
        body.quote.selected_text = input.quote.selectedText;
      }
    }
    if (input.parts && input.parts.length > 0) {
      body.parts = input.parts;
    }
    if (input.clientMessageId) {
      body.client_message_id = input.clientMessageId;
    }
    return this.fetch(`/api/channels/${channelId}/messages/${messageId}/thread`, {
      method: "POST",
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(SEND_TIMEOUT_MS),
    });
  }

  async markChannelThreadRead(channelId: string, messageId: string): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/messages/${messageId}/thread/read`, { method: "POST" });
  }

  async followChannelThread(channelId: string, messageId: string): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/messages/${messageId}/thread/follow`, { method: "PUT" });
  }

  async unfollowChannelThread(channelId: string, messageId: string): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/messages/${messageId}/thread/follow`, { method: "DELETE" });
  }

  async listChannelAttachments(channelId: string): Promise<Attachment[]> {
    return this.fetch(`/api/channels/${channelId}/attachments`);
  }

  async getChannelStats(channelId: string): Promise<ChannelStats> {
    return this.fetch(`/api/channels/${channelId}/stats`);
  }

  async getChannelGoal(
    channelId: string,
    options?: { signal?: AbortSignal },
  ): Promise<ChannelGoalEnvelope> {
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/goal`, abortInit(options));
    return parseWithFallback(raw, ChannelGoalEnvelopeSchema, { goal: null }, {
      endpoint: "GET /api/channels/:id/goal",
    });
  }

  async getWorkGraph(graphId: string): Promise<WorkGraphDetail | null> {
    const raw = await this.fetch<unknown>(`/api/work-graphs/${encodeURIComponent(graphId)}`);
    return parseWithFallback(raw, WorkGraphDetailSchema.nullable(), null, {
      endpoint: "GET /api/work-graphs/:id",
    });
  }

  async createChannelGoal(
    channelId: string,
    input: CreateChannelGoalRequest,
  ): Promise<ChannelGoalEnvelope> {
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/goal`, {
      method: "POST",
      body: JSON.stringify(input),
    });
    return parseWithFallback(raw, ChannelGoalEnvelopeSchema, { goal: null }, {
      endpoint: "POST /api/channels/:id/goal",
    });
  }

  async bootstrapChannelGoalControlPlane(
    channelId: string,
    input: BootstrapChannelGoalControlPlaneRequest,
  ): Promise<ChannelGoalEnvelope> {
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/goal/bootstrap`, {
      method: "POST",
      body: JSON.stringify(input),
    });
    return parseWithFallback(raw, ChannelGoalEnvelopeSchema, { goal: null }, {
      endpoint: "POST /api/channels/:id/goal/bootstrap",
    });
  }

  async updateChannelGoal(
    channelId: string,
    input: UpdateChannelGoalRequest,
  ): Promise<ChannelGoalEnvelope> {
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/goal`, {
      method: "PATCH",
      body: JSON.stringify(input),
    });
    return parseWithFallback(raw, ChannelGoalEnvelopeSchema, { goal: null }, {
      endpoint: "PATCH /api/channels/:id/goal",
    });
  }

  async listChannelGoalProcesses(channelId: string): Promise<ChannelGoalProcessListEnvelope> {
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/goal/process`);
    return parseWithFallback(
      raw,
      ChannelGoalProcessListEnvelopeSchema,
      { goal_id: "", processes: [] },
      { endpoint: "GET /api/channels/:id/goal/process" },
    );
  }

  async getChannelGoalProcess(
    channelId: string,
    managerAgentId: string,
  ): Promise<ChannelGoalProcessEnvelope> {
    const raw = await this.fetch<unknown>(
      `/api/channels/${channelId}/goal/process/${managerAgentId}`,
    );
    return parseWithFallback(raw, ChannelGoalProcessEnvelopeSchema, { process: null }, {
      endpoint: "GET /api/channels/:id/goal/process/:agentId",
    });
  }

  async listChannelGoalSubgoals(channelId: string): Promise<ChannelGoalSubgoalListEnvelope> {
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/goal/subgoals`);
    return parseWithFallback(raw, ChannelGoalSubgoalListEnvelopeSchema, EMPTY_CHANNEL_GOAL_SUBGOAL_LIST, {
      endpoint: "GET /api/channels/:id/goal/subgoals",
    });
  }

  async createChannelGoalSubgoal(
    channelId: string,
    input: CreateChannelGoalSubgoalRequest,
  ): Promise<ChannelGoalSubgoalEnvelope> {
    const raw = await this.fetch<unknown>(`/api/channels/${channelId}/goal/subgoals`, {
      method: "POST",
      body: JSON.stringify(input),
    });
    return parseWithFallback(raw, ChannelGoalSubgoalEnvelopeSchema, { subgoal: null }, {
      endpoint: "POST /api/channels/:id/goal/subgoals",
    });
  }

  async updateChannelGoalSubgoal(
    channelId: string,
    subgoalId: string,
    input: UpdateChannelGoalSubgoalRequest,
  ): Promise<ChannelGoalSubgoalEnvelope> {
    const raw = await this.fetch<unknown>(
      `/api/channels/${channelId}/goal/subgoals/${encodeURIComponent(subgoalId)}`,
      { method: "PATCH", body: JSON.stringify(input) },
    );
    return parseWithFallback(raw, ChannelGoalSubgoalEnvelopeSchema, { subgoal: null }, {
      endpoint: "PATCH /api/channels/:id/goal/subgoals/:subgoalId",
    });
  }

  async resolveChannelGoalSubgoal(
    channelId: string,
    subgoalId: string,
    input: ResolveChannelGoalSubgoalRequest,
  ): Promise<ChannelGoalSubgoalEnvelope> {
    const raw = await this.fetch<unknown>(
      `/api/channels/${channelId}/goal/subgoals/${encodeURIComponent(subgoalId)}/resolve`,
      { method: "POST", body: JSON.stringify(input) },
    );
    return parseWithFallback(raw, ChannelGoalSubgoalEnvelopeSchema, { subgoal: null }, {
      endpoint: "POST /api/channels/:id/goal/subgoals/:subgoalId/resolve",
    });
  }

  async clearChannelGoalSubgoalWaitingOn(
    channelId: string,
    subgoalId: string,
    input: ClearChannelGoalSubgoalWaitingOnRequest,
  ): Promise<ChannelGoalSubgoalEnvelope> {
    const raw = await this.fetch<unknown>(
      `/api/channels/${channelId}/goal/subgoals/${encodeURIComponent(subgoalId)}/waiting-on/clear`,
      { method: "POST", body: JSON.stringify(input) },
    );
    return parseWithFallback(raw, ChannelGoalSubgoalEnvelopeSchema, { subgoal: null }, {
      endpoint: "POST /api/channels/:id/goal/subgoals/:subgoalId/waiting-on/clear",
    });
  }

  async listChannelProjectFiles(channelId: string): Promise<ChannelProjectFiles> {
    return this.fetch(`/api/channels/${channelId}/project-files`);
  }

  async getChannelProjectFile(channelId: string, path: string): Promise<ChannelProjectFileContent> {
    return this.fetch(`/api/channels/${channelId}/project-files/content?path=${encodeURIComponent(path)}`);
  }

  async markChannelRead(channelId: string): Promise<MarkChannelReadResult> {
    return this.fetch<MarkChannelReadResult>(`/api/channels/${channelId}/read`, {
      method: "POST",
    });
  }

  async setChannelTyping(channelId: string, isTyping: boolean): Promise<void> {
    await this.fetch(`/api/channels/${channelId}/typing`, {
      method: "POST",
      body: JSON.stringify({ is_typing: isTyping }),
    });
  }

  async getChannelProject(
    channelId: string,
    options?: { signal?: AbortSignal },
  ): Promise<{ project_id: string }> {
    return this.fetch(`/api/channels/${channelId}/project`, abortInit(options));
  }

  async setChannelProject(channelId: string, projectId: string | null): Promise<{ project_id: string }> {
    return this.fetch(`/api/channels/${channelId}/project`, {
      method: "PUT",
      body: JSON.stringify({ project_id: projectId }),
    });
  }

  // The group channels attached to a project (#574 / #629 — project detail page).
  // The endpoint responds with an envelope `{ channels, total }`; unwrap it so
  // callers get the list directly.
  async listProjectChannels(projectId: string, workspaceId: string): Promise<ProjectChannel[]> {
    const res = await this.fetch<{ channels: ProjectChannel[]; total: number }>(
      `/api/projects/${projectId}/channels?workspace_id=${encodeURIComponent(workspaceId)}`,
    );
    return res.channels ?? [];
  }

  // Set (or clear, with `null`) the group an issue is associated with (#574 /
  // #629 — Issue Properties). The server enforces the 1:1 constraint.
  async setIssueChannel(
    issueId: string,
    channelId: string | null,
    workspaceId: string,
  ): Promise<{ channel_id: string }> {
    return this.fetch(
      `/api/issues/${issueId}/channel?workspace_id=${encodeURIComponent(workspaceId)}`,
      {
        method: "PUT",
        body: JSON.stringify({ channel_id: channelId }),
      },
    );
  }

  async importLarkChannelMessage(data: {
    lark_chat_id: string;
    external_message_id?: string;
    author_name?: string;
    content: string;
  }): Promise<ChannelMessage> {
    return this.fetch("/api/channels/lark/messages", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async cancelTaskById(taskId: string): Promise<CancelTaskResponse> {
    const raw = await this.fetch<unknown>(`/api/tasks/${taskId}/cancel`, { method: "POST" });
    return parseWithFallback(raw, CancelTaskResponseSchema, EMPTY_CANCEL_TASK_RESPONSE, {
      endpoint: "POST /api/tasks/{taskId}/cancel",
    });
  }

  async listAttachments(issueId: string): Promise<Attachment[]> {
    return this.fetch(`/api/issues/${issueId}/attachments`);
  }

  // Fetches a fresh attachment metadata record. The server re-signs
  // `download_url` on every call (30 min expiry), so the click-time
  // download flow uses this endpoint to avoid handing the user a stale
  // signed URL cached in TanStack Query.
  async getAttachment(id: string): Promise<Attachment> {
    const raw = await this.fetch<unknown>(`/api/attachments/${id}`);
    return parseWithFallback(raw, AttachmentResponseSchema, EMPTY_ATTACHMENT, {
      endpoint: "GET /api/attachments/{id}",
    });
  }

  async deleteAttachment(id: string): Promise<void> {
    await this.fetch(`/api/attachments/${id}`, { method: "DELETE" });
  }

  // Fetches the raw bytes of a text-previewable attachment.
  //
  // The endpoint sidesteps CloudFront CORS (not configured on the CDN) and
  // bypasses Content-Disposition: attachment for the `text/*` family, both
  // of which would otherwise prevent the renderer from getting the body.
  // The server always replies with `text/plain; charset=utf-8` for safety;
  // the original MIME ships back in the `X-Original-Content-Type` header so
  // the preview dispatcher can choose between markdown / html / plain code.
  //
  // Routes through `fetchRaw` so it inherits the standard auth headers,
  // 401 → handleUnauthorized recovery, request-id logging, and ApiError
  // shape. 413 / 415 are translated to typed `Preview*Error` instances so
  // the modal can render specific fallbacks instead of generic failure.
  async getAttachmentTextContent(
    id: string,
  ): Promise<{ text: string; originalContentType: string }> {
    let res: Response;
    try {
      res = await this.fetchRaw(`/api/attachments/${id}/content`);
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 413) throw new PreviewTooLargeError();
        if (err.status === 415) throw new PreviewUnsupportedError();
      }
      throw err;
    }
    return {
      text: await res.text(),
      originalContentType: res.headers.get("X-Original-Content-Type") ?? "",
    };
  }

  // Projects
  async listProjects(params?: { status?: string }): Promise<ListProjectsResponse> {
    const search = new URLSearchParams();
    if (params?.status) search.set("status", params.status);
    return this.fetch(`/api/projects?${search}`);
  }

  async getProject(id: string): Promise<Project> {
    return this.fetch(`/api/projects/${id}`);
  }

  async createProject(data: CreateProjectRequest): Promise<Project> {
    return this.fetch("/api/projects", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateProject(id: string, data: UpdateProjectRequest): Promise<Project> {
    return this.fetch(`/api/projects/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteProject(id: string): Promise<void> {
    await this.fetch(`/api/projects/${id}`, { method: "DELETE" });
  }

  // Project resources
  async listProjectResources(
    projectId: string,
  ): Promise<ListProjectResourcesResponse> {
    return this.fetch(`/api/projects/${projectId}/resources`);
  }

  async createProjectResource(
    projectId: string,
    data: CreateProjectResourceRequest,
  ): Promise<ProjectResource> {
    return this.fetch(`/api/projects/${projectId}/resources`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateProjectResource(
    projectId: string,
    resourceId: string,
    data: UpdateProjectResourceRequest,
  ): Promise<ProjectResource> {
    return this.fetch(`/api/projects/${projectId}/resources/${resourceId}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteProjectResource(
    projectId: string,
    resourceId: string,
  ): Promise<void> {
    await this.fetch(`/api/projects/${projectId}/resources/${resourceId}`, {
      method: "DELETE",
    });
  }

  // Labels
  async listLabels(): Promise<ListLabelsResponse> {
    return this.fetch(`/api/labels`);
  }

  async getLabel(id: string): Promise<Label> {
    return this.fetch(`/api/labels/${id}`);
  }

  async createLabel(data: CreateLabelRequest): Promise<Label> {
    return this.fetch(`/api/labels`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateLabel(id: string, data: UpdateLabelRequest): Promise<Label> {
    return this.fetch(`/api/labels/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteLabel(id: string): Promise<void> {
    await this.fetch(`/api/labels/${id}`, { method: "DELETE" });
  }

  async listLabelsForIssue(issueId: string): Promise<IssueLabelsResponse> {
    return this.fetch(`/api/issues/${issueId}/labels`);
  }

  async attachLabel(issueId: string, labelId: string): Promise<IssueLabelsResponse> {
    return this.fetch(`/api/issues/${issueId}/labels`, {
      method: "POST",
      body: JSON.stringify({ label_id: labelId }),
    });
  }

  async detachLabel(issueId: string, labelId: string): Promise<IssueLabelsResponse> {
    return this.fetch(`/api/issues/${issueId}/labels/${labelId}`, {
      method: "DELETE",
    });
  }

  // Pins
  async listPins(): Promise<PinnedItem[]> {
    return this.fetch("/api/pins");
  }

  async createPin(data: CreatePinRequest): Promise<PinnedItem> {
    return this.fetch("/api/pins", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async deletePin(itemType: PinnedItemType, itemId: string): Promise<void> {
    await this.fetch(`/api/pins/${itemType}/${itemId}`, { method: "DELETE" });
  }

  async reorderPins(data: ReorderPinsRequest): Promise<void> {
    await this.fetch("/api/pins/reorder", {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // GitHub integration
  async getGitHubConnectURL(workspaceId: string): Promise<GitHubConnectResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/github/connect`);
  }

  async listGitHubInstallations(workspaceId: string): Promise<ListGitHubInstallationsResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/github/installations`);
  }

  async deleteGitHubInstallation(workspaceId: string, installationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/github/installations/${installationId}`, {
      method: "DELETE",
    });
  }

  async listIssuePullRequests(issueId: string): Promise<{ pull_requests: GitHubPullRequest[] }> {
    return this.fetch(`/api/issues/${issueId}/pull-requests`);
  }

  // Lark integration
  async listLarkInstallations(workspaceId: string): Promise<ListLarkInstallationsResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/lark/installations`);
  }

  async beginLarkInstall(
    workspaceId: string,
    agentId: string,
    region: "feishu" | "lark",
  ): Promise<BeginLarkInstallResponse> {
    // The user picks the cloud explicitly in the UI ("Bind to Feishu"
    // vs "Bind to Lark"), and the backend POSTs the device-flow `begin`
    // against the corresponding accounts host (accounts.feishu.cn vs
    // accounts.larksuite.com) so the QR renders against the right
    // cloud up front. Empty / omitted region still resolves to Feishu
    // server-side (RegionOrDefault) — we surface region as a required
    // arg here so every call site is forced to make a deliberate
    // choice rather than silently defaulting to mainland.
    const search = new URLSearchParams({ agent_id: agentId, region });
    return this.fetch(`/api/workspaces/${workspaceId}/lark/install/begin?${search.toString()}`, {
      method: "POST",
    });
  }

  async getLarkInstallStatus(workspaceId: string, sessionId: string): Promise<LarkInstallStatusResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/lark/install/${sessionId}/status`);
  }

  async deleteLarkInstallation(workspaceId: string, installationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/lark/installations/${installationId}`, {
      method: "DELETE",
    });
  }

  async redeemLarkBindingToken(token: string): Promise<RedeemLarkBindingTokenResponse> {
    return this.fetch(`/api/lark/binding/redeem`, {
      method: "POST",
      body: JSON.stringify({ token }),
    });
  }

  // Research Fleet
  async ensureResearchFleet(
    expectedWorkspaceId?: string,
  ): Promise<import("../types/research").ResearchFleet> {
    const { ResearchFleetSchema } = await import("../research/schemas");
    const raw = await this.fetch("/api/research/fleet/ensure", { method: "POST" });
    const rawFleet =
      raw && typeof raw === "object" && !Array.isArray(raw)
        ? (raw as Record<string, unknown>)
        : null;
    if (!rawFleet || !Array.isArray(rawFleet.members)) {
      throw new Error("POST /api/research/fleet/ensure response failed schema validation");
    }
    const parsed = ResearchFleetSchema.safeParse(raw);
    if (!parsed.success) {
      throw new Error("POST /api/research/fleet/ensure response failed schema validation");
    }
    const fleet = parsed.data;
    const memberIds = fleet.members.map((member) => member.id);
    const agentIds = fleet.members.map((member) => member.agent_id);
    if (
      !fleet.id ||
      !fleet.workspace_id ||
      (expectedWorkspaceId != null && fleet.workspace_id !== expectedWorkspaceId) ||
      fleet.members.some((member) => !member.id || !member.agent_id) ||
      new Set(memberIds).size !== memberIds.length ||
      new Set(agentIds).size !== agentIds.length ||
      (fleet.lead_agent_id != null && !agentIds.includes(fleet.lead_agent_id))
    ) {
      throw new Error("POST /api/research/fleet/ensure response failed identity validation");
    }
    return fleet;
  }

  async listResearchSessions(
    expectedWorkspaceId?: string,
  ): Promise<import("../types/research").ListResearchSessionsResponse> {
    const { ListResearchSessionsResponseSchema } = await import("../research/schemas");
    const raw = await this.fetch("/api/research/sessions");
    const rawSessions =
      raw && typeof raw === "object" && !Array.isArray(raw)
        ? (raw as Record<string, unknown>).sessions
        : null;
    if (
      !Array.isArray(rawSessions) ||
      rawSessions.some((session) => {
        if (!session || typeof session !== "object" || Array.isArray(session)) return true;
        const value = session as Record<string, unknown>;
        const required = ["id", "workspace_id", "status", "current_stage"];
        if (required.some((key) => typeof value[key] !== "string" || value[key] === "")) {
          return true;
        }
        // V6 runs have no fleet; empty/missing fleet_id is valid.
        return value.fleet_id != null && typeof value.fleet_id !== "string";
      })
    ) {
      throw new Error("GET /api/research/sessions response failed schema validation");
    }
    const parsed = ListResearchSessionsResponseSchema.safeParse(raw);
    if (!parsed.success) {
      throw new Error("GET /api/research/sessions response failed schema validation");
    }
    const sessions = parsed.data.sessions;
    const ids = sessions.map((session) => session.id);
    if (
      sessions.some(
        (session) =>
          !session.id ||
          !session.workspace_id ||
          (expectedWorkspaceId != null && session.workspace_id !== expectedWorkspaceId),
      ) ||
      new Set(ids).size !== ids.length
    ) {
      throw new Error("GET /api/research/sessions response failed identity validation");
    }
    return parsed.data;
  }

  async createResearchSession(
    data: import("../types/research").CreateResearchSessionRequest,
    expectedWorkspaceId?: string,
  ): Promise<import("../types/research").CreateResearchSessionResponse> {
    const { CreateResearchSessionResponseSchema } = await import("../research/schemas");
    const raw = await this.fetch("/api/research/sessions", {
      method: "POST",
      body: JSON.stringify({
        goal: data.goal,
        title: data.title,
        client_request_id: data.clientRequestId,
        depth_tier: data.depthTier,
        language: data.language,
        source_weights: data.sourceWeights,
        orchestrator_version: data.orchestratorVersion,
        director_agent_id: data.directorAgentId,
      }),
    });
    const parsed = CreateResearchSessionResponseSchema.safeParse(raw);
    if (!parsed.success) {
      throw new Error("POST /api/research/sessions response failed schema validation");
    }
    const response = parsed.data;
    const sessionId = response.session.id;
    const workspaceId = response.session.workspace_id;
    const scopedEntities = [
      ...(response.nodes ?? []),
      ...(response.edges ?? []),
      ...(response.messages ?? []),
    ];
    if (
      !sessionId ||
      !workspaceId ||
      (expectedWorkspaceId != null && workspaceId !== expectedWorkspaceId) ||
      (response.fleet != null &&
        (!response.fleet.id ||
          response.fleet.workspace_id !== workspaceId ||
          (response.session.fleet_id !== "" &&
            response.session.fleet_id !== response.fleet.id))) ||
      scopedEntities.some((entity) => entity.session_id !== sessionId) ||
      (response.run?.run.session_id != null &&
        response.run.run.session_id !== sessionId) ||
      (response.run?.run.workspace_id != null &&
        response.run.run.workspace_id !== workspaceId)
    ) {
      throw new Error("POST /api/research/sessions response failed identity validation");
    }
    return response;
  }

  async getResearchSessionSnapshot(
    id: string,
  ): Promise<import("../types/research").ResearchSessionSnapshot> {
    const { ResearchSessionSnapshotSchema } = await import("../research/schemas");
    const raw = await this.fetch(`/api/research/sessions/${id}`);
    const parsed = ResearchSessionSnapshotSchema.safeParse(raw);
    if (!parsed.success) {
      throw new Error(
        "GET /api/research/sessions/:id response failed schema validation",
      );
    }
    const data = parsed.data;
    const sessionScoped = [
      ...data.nodes,
      ...data.edges,
      ...data.sources,
      ...data.evals,
      ...data.messages,
      ...(data.report ? [data.report] : []),
    ];
    const hasSessionMismatch =
      data.session.id !== id ||
      sessionScoped.some(
        (entity) => entity.session_id !== "" && entity.session_id !== id,
      ) ||
      (data.run?.run.session_id != null && data.run.run.session_id !== id);
    if (hasSessionMismatch) {
      throw new Error(
        "GET /api/research/sessions/:id response failed session validation",
      );
    }
    return data;
  }

  async getResearchPresence(
    id: string,
  ): Promise<import("../research/queries").ResearchPresenceResponse> {
    const { ResearchPresenceResponseSchema } = await import("../research/schemas");
    const raw = await this.fetch(`/api/research/sessions/${id}/presence`);
    const parsed = parseWithFallback(
      raw,
      ResearchPresenceResponseSchema,
      { session_id: id, presence: {} },
      { endpoint: "GET /api/research/sessions/:id/presence" },
    );
    if (parsed.session_id !== "" && parsed.session_id !== id) {
      throw new Error(
        "GET /api/research/sessions/:id/presence response failed session validation",
      );
    }
    return { ...parsed, session_id: parsed.session_id || id };
  }

  /**
   * GET the LRM-1505 typed research star graph (nodes/edges/clusters/lineage)
   * for one render pass. Validated against the real typed-graph contract — no
   * fabricated topology. Returns the normalized response or the empty fallback
   * when the endpoint reports an empty/drop-graph state.
   */
  async getResearchGraphTyped(
    id: string,
    options?: { limit?: number; offset?: number },
  ): Promise<import("../research/graph-typed").TypedGraphResponse> {
    const { TypedGraphResponseSchema } = await import("../research/graph-typed");
    const params = new URLSearchParams();
    if (options?.limit != null) params.set("limit", String(options.limit));
    if (options?.offset != null) params.set("offset", String(options.offset));
    const qs = params.toString();
    const raw = await this.fetch(
      `/api/research/sessions/${id}/graph/typed${qs ? `?${qs}` : ""}`,
    );
    const parsed = TypedGraphResponseSchema.safeParse(raw);
    if (!parsed.success) {
      throw new Error(
        `GET /api/research/sessions/:id/graph/typed response failed schema validation`,
      );
    }
    const data = parsed.data;
    const hasSessionMismatch =
      (data.session_id !== "" && data.session_id !== id) ||
      data.nodes.some(
        (node) => node.session_id !== "" && node.session_id !== id,
      ) ||
      data.edges.some(
        (edge) => edge.session_id !== "" && edge.session_id !== id,
      ) ||
      data.clusters.some(
        (cluster) => cluster.session_id !== "" && cluster.session_id !== id,
      );
    if (hasSessionMismatch) {
      throw new Error(
        `GET /api/research/sessions/:id/graph/typed response failed session validation`,
      );
    }
    return { ...data, ...(data.session_id ? {} : { session_id: id }) };
  }

  async postResearchNodeCommand(
    sessionId: string,
    nodeId: string,
    data: import("../types/research").ResearchNodeCommandRequest,
  ): Promise<import("../types/research").ResearchNodeCommandResponse> {
    const { ResearchNodeCommandResponseSchema, EMPTY_RESEARCH_NODE_COMMAND } = await import("../research/schemas");
    const raw = await this.fetch(`/api/research/sessions/${sessionId}/nodes/${nodeId}/commands`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, ResearchNodeCommandResponseSchema, EMPTY_RESEARCH_NODE_COMMAND, {
      endpoint: "POST /api/research/sessions/:id/nodes/:nodeId/commands",
    });
  }

  /** Posts one user message to the selected Research Director. */
  async postResearchMessage(
    id: string,
    data: import("../types/research").PostResearchMessageRequest,
  ): Promise<import("../types/research").ResearchMessage> {
    const { ResearchMessageSchema } = await import("../research/schemas");
    const raw = await this.fetch(`/api/research/sessions/${id}/messages`, {
      method: "POST",
      body: JSON.stringify({
        body: data.body,
        client_request_id: data.clientRequestId,
        target_agent_id: data.targetAgentId,
        selected_research_refs: data.selectedResearchRefs?.map((reference) => ({
          stable_id: reference.stableId,
          kind: reference.kind,
          entity_id: reference.entityId,
          revision: reference.revision,
          content_hash: reference.contentHash,
          display_summary: reference.displaySummary,
        })),
      }),
    });
    const result = ResearchMessageSchema.safeParse(raw);
    if (!result.success) {
      throw new Error(
        "POST /api/research/sessions/:id/messages response failed schema validation",
      );
    }
    if (result.data.session_id !== "" && result.data.session_id !== id) {
      throw new Error(
        "POST /api/research/sessions/:id/messages response failed session validation",
      );
    }
    return { ...result.data, session_id: result.data.session_id || id };
  }

  async steerResearchRun(
    id: string,
    data: import("../types/research").SteerResearchRunRequest,
  ): Promise<import("../types/research").ResearchRun> {
    type SteerResponse = { run: import("../types/research").ResearchRun };
    const { SteerResearchRunResponseSchema } = await import("../research/schemas");
    const raw = await this.fetch(`/api/research/sessions/${id}/steer`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    const parsed = parseWithFallback<SteerResponse | null>(
      raw,
      SteerResearchRunResponseSchema,
      null,
      { endpoint: "POST /api/research/sessions/:id/steer" },
    );
    if (parsed === null) {
      throw new Error("Invalid research steering response");
    }
    if (parsed.run.session_id !== id) {
      throw new Error(
        "POST /api/research/sessions/:id/steer response failed session validation",
      );
    }
    return parsed.run;
  }

  private async parseResearchSessionMutationResponse(
    raw: unknown,
    id: string,
    endpoint: string,
  ): Promise<import("../types/research").ResearchSession> {
    const { ResearchSessionSchema } = await import("../research/schemas");
    const result = ResearchSessionSchema.safeParse(raw);
    if (!result.success) {
      throw new Error(`${endpoint} response failed schema validation`);
    }
    if (result.data.id !== id) {
      throw new Error(`${endpoint} response failed session validation`);
    }
    return result.data;
  }

  async confirmResearchSession(id: string): Promise<import("../types/research").ResearchSession> {
    const raw = await this.fetch(`/api/research/sessions/${id}/confirm`, {
      method: "POST",
    });
    return this.parseResearchSessionMutationResponse(
      raw,
      id,
      "POST /api/research/sessions/:id/confirm",
    );
  }

  async stopResearchSession(id: string): Promise<import("../types/research").ResearchSession> {
    const raw = await this.fetch(`/api/research/sessions/${id}/stop`, {
      method: "POST",
    });
    return this.parseResearchSessionMutationResponse(
      raw,
      id,
      "POST /api/research/sessions/:id/stop",
    );
  }

  async deleteResearchSession(id: string): Promise<void> {
    await this.fetch(`/api/research/sessions/${id}`, { method: "DELETE" });
  }

  async researchSessionHandoff(
    id: string,
    data: import("../types/research").ResearchHandoffRequest,
  ): Promise<import("../types/research").ResearchSession> {
    const raw = await this.fetch(`/api/research/sessions/${id}/handoff`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return this.parseResearchSessionMutationResponse(
      raw,
      id,
      "POST /api/research/sessions/:id/handoff",
    );
  }

  /** LRM-911 / LRM-913 — list end-of-round judgment cards. */
  async listResearchProductRoundCards(
    sessionId: string,
  ): Promise<import("../types/research").ListResearchProductRoundCardsResponse> {
    const {
      ListResearchProductRoundCardsResponseSchema,
      EMPTY_RESEARCH_PRODUCT_ROUNDS,
    } = await import("../research/schemas");
    try {
      const raw = await this.fetch(
        `/api/research/sessions/${sessionId}/product-rounds`,
      );
      const result = ListResearchProductRoundCardsResponseSchema.safeParse(raw);
      if (!result.success) {
        throw new Error(
          "GET /api/research/sessions/:id/product-rounds response failed schema validation",
        );
      }
      if (
        result.data.rounds.some(
          (card) => card.session_id !== "" && card.session_id !== sessionId,
        )
      ) {
        throw new Error(
          "GET /api/research/sessions/:id/product-rounds response failed session validation",
        );
      }
      return {
        rounds: result.data.rounds.map((card) => ({
          ...card,
          session_id: card.session_id || sessionId,
        })),
      };
    } catch (error) {
      if (error instanceof ApiError && (error.status === 404 || error.status === 501)) {
        // Optional capability is genuinely absent; D5 keeps its process fallback.
        return EMPTY_RESEARCH_PRODUCT_ROUNDS;
      }
      throw error;
    }
  }

  async getResearchProductRoundCard(
    sessionId: string,
    round: number,
  ): Promise<import("../types/research").ResearchProductRoundCard> {
    const { ResearchProductRoundCardSchema } = await import("../research/schemas");
    const raw = await this.fetch(
      `/api/research/sessions/${sessionId}/product-rounds/${round}`,
    );
    const result = ResearchProductRoundCardSchema.safeParse(raw);
    if (!result.success) {
      throw new Error(
        "GET /api/research/sessions/:id/product-rounds/:round response failed schema validation",
      );
    }
    if (result.data.session_id !== "" && result.data.session_id !== sessionId) {
      throw new Error(
        "GET /api/research/sessions/:id/product-rounds/:round response failed session validation",
      );
    }
    return {
      ...result.data,
      session_id: result.data.session_id || sessionId,
    };
  }

  // ---- Ronaldo / Director V6 Projection (authoritative unreleased contract) ----

  async getResearchV6DirectorProjectionSnapshot(
    workspaceId: string,
    runId: string,
    options?: { cursor?: string; signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorProjectionSnapshot> {
    const { parseResearchV6DirectorProjectionSnapshot } = await import(
      "../research-v6/director-schemas"
    );
    const params = new URLSearchParams();
    if (options?.cursor) params.set("cursor", options.cursor);
    const query = params.toString();
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/projection/snapshot${query ? `?${query}` : ""}`,
      { signal: options?.signal },
    );
    const snapshot = parseResearchV6DirectorProjectionSnapshot(raw);
    if (snapshot.workspaceId !== workspaceId || snapshot.runId !== runId) {
      throw new Error("Director V6 projection snapshot identity mismatch");
    }
    return snapshot;
  }

  async replaceResearchV6Director(
    workspaceId: string,
    runId: string,
    request: import("../types/research-v6-director").ResearchV6DirectorAssignmentRequest,
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorAssignment | null> {
    const { ResearchV6DirectorAssignmentSchema } = await import(
      "../research-v6/director-schemas"
    );
    const raw = await this.fetch(`/api/research/sessions/${encodeURIComponent(runId)}/director`, {
      method: "PUT",
      body: JSON.stringify({
        director_agent_id: request.directorAgentId,
        expected_state_version: request.expectedStateVersion,
        reason: request.reason,
        client_request_id: request.clientRequestId,
      }),
    });
    const parsed = parseWithFallback<z.output<typeof ResearchV6DirectorAssignmentSchema> | null>(
      raw,
      ResearchV6DirectorAssignmentSchema,
      null,
      { endpoint: "PUT Director V6 assignment" },
    );
    if (parsed === null) return null;
    if (parsed.workspace_id !== workspaceId || parsed.run_id !== runId) {
      throw new Error("Director V6 assignment identity mismatch");
    }
    return {
      id: parsed.id,
      workspaceId: parsed.workspace_id,
      runId: parsed.run_id,
      directorAgentId: parsed.director_agent_id,
      status: parsed.status,
      reason: parsed.reason,
      generation: parsed.generation,
      stateVersion: parsed.state_version,
    };
  }

  async getResearchV6DirectorProjectionSlice(
    workspaceId: string,
    runId: string,
    request: import("../types/research-v6-director").ResearchV6DirectorProjectionSliceRequest,
    options?: { signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorProjectionSnapshot> {
    const {
      encodeResearchV6DirectorProjectionSliceRequest,
      parseResearchV6DirectorProjectionSnapshot,
    } = await import("../research-v6/director-schemas");
    const encoded = encodeResearchV6DirectorProjectionSliceRequest(request);
    const params = new URLSearchParams({
      root: encoded.root,
      depth: String(encoded.depth),
      snapshot_id: encoded.snapshot_id,
    });
    if (encoded.cursor) params.set("cursor", encoded.cursor);
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/projection/slice?${params.toString()}`,
      { signal: options?.signal },
    );
    const snapshot = parseResearchV6DirectorProjectionSnapshot(raw);
    if (snapshot.workspaceId !== workspaceId || snapshot.runId !== runId) {
      throw new Error("Director V6 projection slice identity mismatch");
    }
    if (snapshot.snapshotId !== encoded.snapshot_id) {
      throw new Error("Director V6 projection slice snapshot mismatch");
    }
    return snapshot;
  }

  async getResearchV6DirectorProjectionDeltaPage(
    workspaceId: string,
    runId: string,
    after: number,
    options?: { cursor?: string; signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorProjectionDeltaPage> {
    if (!Number.isSafeInteger(after) || after < 0) {
      throw new Error("Director V6 projection delta 'after' must be a non-negative integer");
    }
    const { parseResearchV6DirectorProjectionDeltaPage } = await import(
      "../research-v6/director-schemas"
    );
    const params = new URLSearchParams({ after: String(after) });
    if (options?.cursor) params.set("cursor", options.cursor);
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/projection/deltas?${params.toString()}`,
      { signal: options?.signal },
    );
    const page = parseResearchV6DirectorProjectionDeltaPage(raw);
    if (page.runId !== runId || page.deltas.some((delta) => delta.workspaceId !== workspaceId || delta.runId !== runId)) {
      return {
        runId,
        deltas: [],
        nextCursor: null,
        resyncRequired: true,
      };
    }
    return page;
  }

  async resumeResearchV6DirectorProjection(
    workspaceId: string,
    runId: string,
    request: import("../types/research-v6-director").ResearchV6DirectorProjectionResumeRequest,
    options?: { signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorProjectionDeltaPage> {
    const {
      parseResearchV6DirectorProjectionDeltaPage,
      encodeResearchV6DirectorProjectionResumeRequest,
    } = await import("../research-v6/director-schemas");
    const body = encodeResearchV6DirectorProjectionResumeRequest(request);
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/projection/resume`,
      {
        method: "POST",
        body: JSON.stringify(body),
        signal: options?.signal,
      },
    );
    const page = parseResearchV6DirectorProjectionDeltaPage(raw);
    if (page.runId !== runId || page.deltas.some((delta) => delta.workspaceId !== workspaceId || delta.runId !== runId)) {
      return {
        runId,
        deltas: [],
        nextCursor: null,
        resyncRequired: true,
      };
    }
    return page;
  }

  async getResearchV6DirectorProjectionNodeDetail(
    workspaceId: string,
    runId: string,
    snapshotId: string,
    nodeId: string,
    view: import("../types/research-v6-director").ResearchV6DirectorNodeDetailView = "brief",
    options?: { signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorNodeDetail> {
    const { parseResearchV6DirectorNodeDetail } = await import(
      "../research-v6/director-schemas"
    );
    const query = new URLSearchParams({ snapshot_id: snapshotId, view });
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/projection/nodes/${encodeURIComponent(nodeId)}?${query.toString()}`,
      { signal: options?.signal },
    );
    const detail = parseResearchV6DirectorNodeDetail(raw);
    if (detail.node.id !== nodeId) {
      throw new Error("Director V6 node detail identity mismatch");
    }
    void workspaceId;
    return detail;
  }

  async getResearchV6DirectorWorkActivity(
    workspaceId: string,
    runId: string,
    workItemId: string,
    options?: { signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorWorkActivity> {
    const { parseResearchV6DirectorWorkActivity } = await import(
      "../research-v6/director-schemas"
    );
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/work-items/${encodeURIComponent(workItemId)}/activity`,
      { signal: options?.signal },
    );
    const activity = parseResearchV6DirectorWorkActivity(raw);
    if (activity === null || activity.workItemId !== workItemId) {
      throw new Error("Director V6 work activity identity mismatch");
    }
    void workspaceId;
    return activity;
  }

  async getResearchV6DirectorReports(
    workspaceId: string,
    runId: string,
    options?: { signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorReportMetadata[]> {
    const { parseResearchV6DirectorReportList } = await import(
      "../research-v6/director-schemas"
    );
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/reports`,
      { signal: options?.signal },
    );
    void workspaceId;
    return parseResearchV6DirectorReportList(raw);
  }

  async getResearchV6DirectorReport(
    workspaceId: string,
    runId: string,
    reportId: string,
    options?: { signal?: AbortSignal },
  ): Promise<import("../types/research-v6-director").ResearchV6DirectorReportDetail> {
    const { parseResearchV6DirectorReportDetail } = await import(
      "../research-v6/director-schemas"
    );
    const raw = await this.fetch(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/reports/${encodeURIComponent(reportId)}`,
      { signal: options?.signal },
    );
    const report = parseResearchV6DirectorReportDetail(raw);
    if (report.id !== reportId) {
      throw new Error("Director V6 report detail identity mismatch");
    }
    void workspaceId;
    return report;
  }

  async getResearchV6DirectorReportCompiled(
    workspaceId: string,
    runId: string,
    reportId: string,
    options?: { signal?: AbortSignal },
  ): Promise<string> {
    const res = await this.fetchRaw(
      `/api/research/v6/runs/${encodeURIComponent(runId)}/reports/${encodeURIComponent(reportId)}/compiled`,
      { signal: options?.signal },
    );
    const mediaType = (res.headers.get("content-type") ?? "").toLowerCase();
    if (!mediaType.startsWith("text/html")) {
      throw new Error("Director V6 compiled report is not HTML");
    }
    const html = await res.text();
    if (html.length === 0 || html.length > 24 * 1024 * 1024) {
      throw new Error("Director V6 compiled report is empty or too large");
    }
    void workspaceId;
    return html;
  }

  async getResearchV6Release(): Promise<{
    workspace_id: string;
    create_enabled: boolean;
    maintenance_reason: string;
    paused_run_count: number;
  }> {
    const { ResearchV6ReleaseSchema } = await import("../research/schemas");
    const raw = await this.fetch("/api/research/v6/release");
    return parseWithFallback(raw, ResearchV6ReleaseSchema, {
      workspace_id: "",
      create_enabled: true,
      maintenance_reason: "",
      paused_run_count: 0,
    }, { endpoint: "GET /api/research/v6/release" });
  }

  async listResearchMonitors(): Promise<{ monitors: Array<{ id: string; status: string; last_cycle_status?: string }> }> {
    const { ResearchMonitorListSchema } = await import("../research/schemas");
    const raw = await this.fetch("/api/research/v6/monitors");
    return parseWithFallback(raw, ResearchMonitorListSchema, { monitors: [] }, {
      endpoint: "GET /api/research/v6/monitors",
    });
  }

  async getResearchProductionWindow(): Promise<{
    llm_judge: boolean;
    quality_signal: string;
    report?: { sufficient_data?: boolean; within_bounds?: boolean };
  }> {
    const { ResearchProductionWindowSchema } = await import("../research/schemas");
    const raw = await this.fetch("/api/research/v6/production-window");
    return parseWithFallback(raw, ResearchProductionWindowSchema, {
      llm_judge: false,
      quality_signal: "user_confirmed_delivery",
    }, { endpoint: "GET /api/research/v6/production-window" });
  }
}
