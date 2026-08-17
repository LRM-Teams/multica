import { describe, expect, it } from "vitest";
import type {
  ResearchV6DirectorProjectionDelta,
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorProjectionTransport,
} from "../../types/research-v6-director";
import {
  RESEARCH_V6_DIRECTOR_DELTA_EVENT,
  ResearchV6DirectorLiveController,
  type ResearchV6DirectorRealtimeBus,
} from "./director-controller";

const WORKSPACE_ID = "00000000-0000-4000-8000-000000000001";
const RUN_ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";
const HASH_A = `sha256:${"a".repeat(64)}`;
const HASH_B = `sha256:${"b".repeat(64)}`;

function snapshot(): ResearchV6DirectorProjectionSnapshot {
  return {
    contract_kind: "projection_snapshot",
    schema_version: 6,
    snapshot_id: SNAPSHOT_ID,
    workspace_id: WORKSPACE_ID,
    run_id: RUN_ID,
    through_event_sequence: 4,
    projection_hash: HASH_A,
    slice_key: "default",
    nodes: [],
    edges: [],
    density_bins: [],
    has_more: false,
  };
}

function delta(): ResearchV6DirectorProjectionDelta {
  return {
    contract_kind: "projection_delta",
    schema_version: 6,
    workspace_id: WORKSPACE_ID,
    run_id: RUN_ID,
    snapshot_id: SNAPSHOT_ID,
    event_sequence: 5,
    previous_projection_hash: HASH_A,
    projection_hash: HASH_B,
    upsert_nodes: [],
    remove_node_ids: [],
    upsert_edges: [],
    remove_edge_ids: [],
    invalidate_slice_keys: ["expand:root"],
  };
}

function bus() {
  let eventHandler: ((payload: unknown) => void) | null = null;
  let reconnectHandler: (() => void) | null = null;
  const realtime = {
    subscribeEvent(event, handler) {
      expect(event).toBe(RESEARCH_V6_DIRECTOR_DELTA_EVENT);
      eventHandler = handler;
      return () => {
        eventHandler = null;
      };
    },
    onBusReconnect(handler) {
      reconnectHandler = handler;
      return () => {
        reconnectHandler = null;
      };
    },
    onBusConnectionStatus() {
      return () => {};
    },
  } satisfies ResearchV6DirectorRealtimeBus;
  return {
    realtime,
    push: (payload: unknown) => eventHandler?.(payload),
    reconnect: () => reconnectHandler?.(),
  };
}

function transport(options?: {
  resumePage?: Awaited<ReturnType<ResearchV6DirectorProjectionTransport["resume"]>>;
}) {
  const resumeRequests: unknown[] = [];
  let snapshotLoads = 0;
  const value = {
    loadSnapshot: async () => {
      snapshotLoads += 1;
      return snapshot();
    },
    resume: async (_workspaceId, _runId, request) => {
      resumeRequests.push(request);
      return (
        options?.resumePage ?? {
          run_id: RUN_ID,
          deltas: [],
          next_cursor: null,
          resync_required: false,
        }
      );
    },
  } as Pick<
    ResearchV6DirectorProjectionTransport,
    "loadSnapshot" | "resume"
  > as ResearchV6DirectorProjectionTransport;
  return {
    value,
    resumeRequests,
    snapshotLoads: () => snapshotLoads,
  };
}

describe("ResearchV6DirectorLiveController", () => {
  it("applies a strict run-scoped delta and forwards slice invalidation", () => {
    const live = bus();
    const invalidated: Array<readonly string[]> = [];
    const controller = new ResearchV6DirectorLiveController(
      { workspaceId: WORKSPACE_ID, runId: RUN_ID },
      transport().value,
      live.realtime,
      { onInvalidateSliceKeys: (keys) => invalidated.push(keys) },
    );
    controller.seedSnapshotPage(snapshot());
    controller.connect();
    live.push({ run_id: RUN_ID, delta: delta() });
    expect(controller.getClient().getState().lastConfirmedSequence).toBe(5);
    expect(invalidated).toEqual([["expand:root"]]);
  });

  it("resumes with snapshot, sequence, and hash rather than sequence alone", async () => {
    const wire = transport({
      resumePage: {
        run_id: RUN_ID,
        deltas: [delta()],
        next_cursor: null,
        resync_required: false,
      },
    });
    const controller = new ResearchV6DirectorLiveController(
      { workspaceId: WORKSPACE_ID, runId: RUN_ID },
      wire.value,
      bus().realtime,
    );
    controller.seedSnapshotPage(snapshot());
    await controller.resumeNow();
    expect(wire.resumeRequests).toEqual([
      {
        snapshot_id: SNAPSHOT_ID,
        last_confirmed_sequence: 4,
        projection_hash: HASH_A,
      },
    ]);
    expect(controller.getClient().getState().projectionHash).toBe(HASH_B);
  });

  it("drops another run envelope without poisoning this run", () => {
    const live = bus();
    const controller = new ResearchV6DirectorLiveController(
      { workspaceId: WORKSPACE_ID, runId: RUN_ID },
      transport().value,
      live.realtime,
    );
    controller.seedSnapshotPage(snapshot());
    controller.connect();
    live.push({ run_id: "other-run", delta: { broken: true } });
    expect(controller.getLiveState().malformedFrameCount).toBe(0);
    expect(controller.getClient().getState().resyncRequired).toBe(false);
  });
});
