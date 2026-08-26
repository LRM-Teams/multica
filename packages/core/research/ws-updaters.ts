import type { QueryClient } from "@tanstack/react-query";
import type { WSMessage } from "../types/events";
import type {
  ListResearchProductRoundCardsResponse,
  ResearchSessionSnapshot,
} from "../types/research";
import { researchV6DirectorProjectionKeys } from "../hooks/research-v6/director-queries";
import { researchKeys, type ResearchPresenceMap } from "./queries";
import { ResearchProductRoundCardSchema } from "./schemas";
import { applyTypedGraphWsPatch } from "./typed-graph-cache";

const listRefreshTimers = new WeakMap<QueryClient, Map<string, ReturnType<typeof setTimeout>>>();
const v6ProjectionRefreshTimers = new WeakMap<
  QueryClient,
  Map<string, ReturnType<typeof setTimeout>>
>();

function scheduleSessionListRefresh(qc: QueryClient, wsId: string) {
  let timers = listRefreshTimers.get(qc);
  if (!timers) {
    timers = new Map();
    listRefreshTimers.set(qc, timers);
  }
  if (timers.has(wsId)) return;
  const timer = setTimeout(() => {
    timers?.delete(wsId);
    void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
  }, 1000);
  timers.set(wsId, timer);
}

function scheduleV6ProjectionRefresh(
  qc: QueryClient,
  wsId: string,
  runId: string,
) {
  let timers = v6ProjectionRefreshTimers.get(qc);
  if (!timers) {
    timers = new Map();
    v6ProjectionRefreshTimers.set(qc, timers);
  }
  const timerKey = `${wsId}:${runId}`;
  if (timers.has(timerKey)) return;
  const timer = setTimeout(() => {
    timers?.delete(timerKey);
    void qc.invalidateQueries({
      queryKey: researchV6DirectorProjectionKeys.snapshot(wsId, runId),
    });
    void qc.invalidateQueries({
      queryKey: researchV6DirectorProjectionKeys.reports(wsId, runId),
    });
  }, 500);
  timers.set(timerKey, timer);
}

function sessionIdFromPayload(payload: Record<string, unknown>): string | null {
  if (typeof payload.session_id === "string") return payload.session_id;
  const session = payload.session as { id?: string } | undefined;
  return session?.id ?? null;
}

function conflictsWithSession(value: unknown, sessionId: string): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const embedded = (value as Record<string, unknown>).session_id;
  return typeof embedded === "string" && embedded !== "" && embedded !== sessionId;
}

function hasStringField(value: unknown, field: string): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const fieldValue = (value as Record<string, unknown>)[field];
  return typeof fieldValue === "string" && fieldValue !== "";
}

function isGraphNodePatch(value: unknown): boolean {
  return hasStringField(value, "id") && hasStringField(value, "node_type");
}

function isGraphEdgePatch(value: unknown): boolean {
  return (
    hasStringField(value, "id") &&
    hasStringField(value, "from_node_id") &&
    hasStringField(value, "to_node_id") &&
    hasStringField(value, "edge_type")
  );
}

function invalidateSessionSnapshot(
  qc: QueryClient,
  wsId: string,
  sessionId: string,
) {
  void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
}

function patchSnapshot(
  qc: QueryClient,
  wsId: string,
  sessionId: string,
  patch: (prev: ResearchSessionSnapshot) => ResearchSessionSnapshot,
) {
  qc.setQueryData<ResearchSessionSnapshot>(
    researchKeys.snapshot(wsId, sessionId),
    (prev) => (prev ? patch(prev) : prev),
  );
}

export function applyResearchWSEvent(
  qc: QueryClient,
  wsId: string,
  message: WSMessage,
) {
  const payload = (message.payload ?? {}) as Record<string, unknown>;
  if (message.type === "research_projection_v6:delta") {
    const runId = typeof payload.run_id === "string" ? payload.run_id : "";
    if (runId) scheduleV6ProjectionRefresh(qc, wsId, runId);
    return;
  }
  const sessionId = sessionIdFromPayload(payload);
  if (message.type.startsWith("research_session:")) {
    scheduleSessionListRefresh(qc, wsId);
  }
  if (!sessionId) {
    if (message.type === "research_session:status_changed") {
      scheduleSessionListRefresh(qc, wsId);
    }
    return;
  }

  switch (message.type) {
    case "research_session:graph_updated": {
      const node = payload.node as ResearchSessionSnapshot["nodes"][number] | undefined;
      const edge = payload.edge as ResearchSessionSnapshot["edges"][number] | null | undefined;
      const edges = payload.edges as ResearchSessionSnapshot["edges"] | undefined;
      const graphVersion =
        typeof payload.graph_version === "number" && Number.isFinite(payload.graph_version)
          ? payload.graph_version
          : undefined;
      if (
        (node !== undefined && !isGraphNodePatch(node)) ||
        (edge != null && !isGraphEdgePatch(edge)) ||
        (Array.isArray(edges) &&
          edges.some((incoming) => !isGraphEdgePatch(incoming))) ||
        conflictsWithSession(node, sessionId) ||
        conflictsWithSession(edge, sessionId) ||
        (Array.isArray(edges) &&
          edges.some((incoming) => conflictsWithSession(incoming, sessionId)))
      ) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        void qc.invalidateQueries({
          queryKey: researchKeys.graphTypedInfinite(wsId, sessionId),
        });
        void qc.invalidateQueries({
          queryKey: ["research", wsId, "graph-typed", sessionId],
        });
        break;
      }
      patchSnapshot(qc, wsId, sessionId, (prev) => {
        const nodes = node
          ? [...prev.nodes.filter((n) => n.id !== node.id), node]
          : prev.nodes;
        let nextEdges = prev.edges;
        if (edge) {
          nextEdges = [...nextEdges.filter((entry) => entry.id !== edge.id), edge];
        }
        if (Array.isArray(edges)) {
          for (const incoming of edges) {
            nextEdges = [...nextEdges.filter((entry) => entry.id !== incoming.id), incoming];
          }
        }
        return { ...prev, nodes, edges: nextEdges };
      });
      const patchResult = applyTypedGraphWsPatch(qc, wsId, sessionId, {
        node,
        edge: edge ?? undefined,
        edges,
        graphVersion,
      });
      if (patchResult.needsResync || !patchResult.patched) {
        void qc.invalidateQueries({
          queryKey: researchKeys.graphTypedInfinite(wsId, sessionId),
        });
        void qc.invalidateQueries({
          queryKey: ["research", wsId, "graph-typed", sessionId],
        });
      }
      break;
    }
    case "research_session:sources_updated": {
      const source = payload.source as ResearchSessionSnapshot["sources"][number] | undefined;
      if (!source) break;
      if (!hasStringField(source, "id")) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      if (conflictsWithSession(source, sessionId)) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      patchSnapshot(qc, wsId, sessionId, (prev) => ({
        ...prev,
        sources: [...prev.sources.filter((s) => s.id !== source.id), source],
      }));
      break;
    }
    case "research_session:report_updated": {
      const report = payload.report as ResearchSessionSnapshot["report"];
      if (report !== null && !hasStringField(report, "id")) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      if (conflictsWithSession(report, sessionId)) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      patchSnapshot(qc, wsId, sessionId, (prev) => ({ ...prev, report }));
      break;
    }
    case "research_session:message": {
      const msg = payload.message as ResearchSessionSnapshot["messages"][number] | undefined;
      if (!msg) break;
      if (!hasStringField(msg, "id")) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      if (conflictsWithSession(msg, sessionId)) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      // Upsert by id so streaming/stop mirrors can grow body in place (LRM-820)
      // without remounting the whole feed as a new card.
      patchSnapshot(qc, wsId, sessionId, (prev) => {
        const idx = prev.messages.findIndex((m) => m.id === msg.id);
        if (idx < 0) return { ...prev, messages: [...prev.messages, msg] };
        const next = prev.messages.slice();
        next[idx] = { ...next[idx], ...msg };
        return { ...prev, messages: next };
      });
      break;
    }
    case "research_session:stage_eval": {
      const ev = payload.eval as ResearchSessionSnapshot["evals"][number] | undefined;
      if (!ev) break;
      if (!hasStringField(ev, "id")) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      if (conflictsWithSession(ev, sessionId)) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      patchSnapshot(qc, wsId, sessionId, (prev) => ({
        ...prev,
        evals: [ev, ...prev.evals.filter((e) => e.id !== ev.id)],
      }));
      break;
    }
    case "research_session:product_round": {
      const parsed = ResearchProductRoundCardSchema.safeParse(payload.card);
      if (!parsed.success) break;
      if (conflictsWithSession(parsed.data, sessionId)) {
        void qc.invalidateQueries({
          queryKey: researchKeys.productRounds(wsId, sessionId),
        });
        break;
      }
      const card = {
        ...parsed.data,
        session_id: parsed.data.session_id || sessionId,
      };
      qc.setQueryData<ListResearchProductRoundCardsResponse>(
        researchKeys.productRounds(wsId, sessionId),
        (prev) => {
          const rounds = prev?.rounds ?? [];
          const index = rounds.findIndex((round) => round.id === card.id);
          if (index < 0) return { rounds: [...rounds, card] };
          const next = rounds.slice();
          next[index] = card;
          return { rounds: next };
        },
      );
      break;
    }
    case "research_session:status_changed": {
      if (payload.deleted === true) {
        qc.removeQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
        qc.removeQueries({
          queryKey: researchKeys.graphTypedInfinite(wsId, sessionId),
        });
        qc.removeQueries({
          queryKey: ["research", wsId, "graph-typed", sessionId],
        });
        qc.removeQueries({ queryKey: researchKeys.presence(wsId, sessionId) });
        qc.removeQueries({
          queryKey: researchKeys.productRounds(wsId, sessionId),
        });
        break;
      }
      const session = payload.session as ResearchSessionSnapshot["session"] | undefined;
      if (
        (session !== undefined && !hasStringField(session, "id")) ||
        conflictsWithSession(session, sessionId) ||
        (session?.id && session.id !== sessionId)
      ) {
        invalidateSessionSnapshot(qc, wsId, sessionId);
        break;
      }
      if (session) {
        patchSnapshot(qc, wsId, sessionId, (prev) => ({ ...prev, session }));
      }
      break;
    }
    case "research_session:presence": {
      const agentId = typeof payload.agent_id === "string" ? payload.agent_id : null;
      const activity = typeof payload.activity === "string" ? payload.activity : "";
      if (!agentId) break;
      const updatedAtRaw = payload.updated_at;
      const updatedAt =
        typeof updatedAtRaw === "number" && Number.isFinite(updatedAtRaw)
          ? updatedAtRaw
          : Date.now();
      qc.setQueryData<ResearchPresenceMap>(
        researchKeys.presence(wsId, sessionId),
        (prev) => {
          const next: ResearchPresenceMap = { ...(prev ?? {}) };
          if (!activity.trim()) {
            delete next[agentId];
            return next;
          }
          next[agentId] = {
            ...(next[agentId] ?? {
              phase: "idle", role: "", name: "", avatarUrl: null, fleetMemberId: null,
              taskId: null, nodeId: null, branchId: null, stage: null,
              expiresAt: null, staleReason: null,
            }),
            activity, updatedAt,
          };
          return next;
        },
      );
      break;
    }
    default:
      break;
  }
}
