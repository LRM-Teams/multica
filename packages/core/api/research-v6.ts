import type { ApiClient } from "./client";
import type { ResearchV6ProjectionTransport } from "../types/research-v6";

/**
 * Research V6 Graph Projection transport.
 *
 * Consumes the backend Projection read model over HTTP + WS. Production paths
 * call the real endpoints via the configured ApiClient — no contract fixtures
 * are ever silently injected here. Inject a fixture-backed transport explicitly
 * in tests / dev harness only.
 */
export function createResearchV6ProjectionTransport(api: ApiClient): ResearchV6ProjectionTransport {
  return {
    loadSnapshot: (runId) => api.getResearchV6ProjectionSnapshot(runId),
    loadDeltaPage: (runId, fromSequenceExclusive) =>
      api.getResearchV6ProjectionDeltaPage(runId, fromSequenceExclusive),
    resume: (runId, lastConfirmedSequence) =>
      api.resumeResearchV6Projection(runId, lastConfirmedSequence),
  };
}
