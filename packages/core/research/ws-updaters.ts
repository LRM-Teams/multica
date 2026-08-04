import type { QueryClient } from "@tanstack/react-query";
import type { WSMessage } from "../types/events";
import type {
  ListResearchProductRoundCardsResponse,
  ResearchSessionSnapshot,
} from "../types/research";
import { researchKeys, type ResearchPresenceMap } from "./queries";
import { ResearchProductRoundCardSchema } from "./schemas";

function sessionIdFromPayload(payload: Record<string, unknown>): string | null {
  if (typeof payload.session_id === "string") return payload.session_id;
  const session = payload.session as { id?: string } | undefined;
  return session?.id ?? null;
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
  const sessionId = sessionIdFromPayload(payload);
  if (!sessionId) {
    if (message.type === "research_session:status_changed") {
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
    }
    return;
  }

  switch (message.type) {
    case "research_session:graph_updated": {
      const node = payload.node as ResearchSessionSnapshot["nodes"][number] | undefined;
      const edge = payload.edge as ResearchSessionSnapshot["edges"][number] | null | undefined;
      patchSnapshot(qc, wsId, sessionId, (prev) => {
        const nodes = node
          ? [...prev.nodes.filter((n) => n.id !== node.id), node]
          : prev.nodes;
        const edges = edge
          ? [...prev.edges.filter((e) => e.id !== edge.id), edge]
          : prev.edges;
        return { ...prev, nodes, edges };
      });
      break;
    }
    case "research_session:sources_updated": {
      const source = payload.source as ResearchSessionSnapshot["sources"][number] | undefined;
      if (!source) break;
      patchSnapshot(qc, wsId, sessionId, (prev) => ({
        ...prev,
        sources: [...prev.sources.filter((s) => s.id !== source.id), source],
      }));
      break;
    }
    case "research_session:report_updated": {
      const report = payload.report as ResearchSessionSnapshot["report"];
      patchSnapshot(qc, wsId, sessionId, (prev) => ({ ...prev, report }));
      break;
    }
    case "research_session:message": {
      const msg = payload.message as ResearchSessionSnapshot["messages"][number] | undefined;
      if (!msg) break;
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
      patchSnapshot(qc, wsId, sessionId, (prev) => ({
        ...prev,
        evals: [ev, ...prev.evals.filter((e) => e.id !== ev.id)],
      }));
      break;
    }
    case "research_session:product_round": {
      const parsed = ResearchProductRoundCardSchema.safeParse(payload.card);
      if (!parsed.success) break;
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
        void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
        break;
      }
      const session = payload.session as ResearchSessionSnapshot["session"] | undefined;
      if (session) {
        patchSnapshot(qc, wsId, sessionId, (prev) => ({ ...prev, session }));
      }
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
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
              phase: "idle", role: "", fleetMemberId: null, taskId: null,
              nodeId: null, branchId: null, stage: null, expiresAt: null, staleReason: null,
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
