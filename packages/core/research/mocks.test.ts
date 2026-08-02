import { describe, expect, it } from "vitest";
import {
  mockCreateResearchSessionResponse,
  mockResearchSessionsList,
  mockResearchSessionsListEmpty,
  mockResearchSnapshotClarification,
  mockResearchSnapshotDefault,
  mockResearchSnapshotEmpty,
  mockResearchSnapshotError,
  mockResearchSnapshotLoading,
  researchMocks,
} from "./mocks";
import {
  CreateResearchSessionResponseSchema,
  ListResearchSessionsResponseSchema,
  ResearchSessionSnapshotSchema,
} from "./schemas";

describe("LRM-841 research mocks", () => {
  it("default snapshot satisfies snapshot schema and drives populated UI", () => {
    const parsed = ResearchSessionSnapshotSchema.parse(mockResearchSnapshotDefault);
    expect(parsed.session.status).toBe("running");
    expect(parsed.nodes.length).toBeGreaterThanOrEqual(5);
    expect(parsed.edges.length).toBeGreaterThanOrEqual(3);
    expect(parsed.sources.length).toBeGreaterThanOrEqual(2);
    expect(parsed.report).not.toBeNull();
    expect(parsed.messages.some((m) => m.card_kind === "process")).toBe(true);
  });

  it("empty snapshot drives first-visit empty state", () => {
    const parsed = ResearchSessionSnapshotSchema.parse(mockResearchSnapshotEmpty);
    expect(parsed.session.status).toBe("drafting");
    expect(parsed.nodes).toHaveLength(0);
    expect(parsed.sources).toHaveLength(0);
    expect(parsed.report).toBeNull();
    expect(parsed.messages).toHaveLength(0);
  });

  it("loading snapshot keeps session row but no data for skeleton rendering", () => {
    const parsed = ResearchSessionSnapshotSchema.parse(mockResearchSnapshotLoading);
    expect(parsed.session.id).toBe("sess-loading");
    expect(parsed.nodes).toHaveLength(0);
    expect(parsed.edges).toHaveLength(0);
  });

  it("error snapshot exposes wake_failed process card for LRM-823/828", () => {
    const parsed = ResearchSessionSnapshotSchema.parse(mockResearchSnapshotError);
    const process = parsed.messages.find((m) => m.card_kind === "process");
    expect(process).toBeDefined();
    const meta = process?.meta as Record<string, unknown> | undefined;
    expect(meta?.op).toBe("wake_failed");
    expect(meta?.reason).toBe("runtime_offline");
  });

  it("clarification snapshot exposes list + form clarification_question cards (LRM-822)", () => {
    const parsed = ResearchSessionSnapshotSchema.parse(mockResearchSnapshotClarification);
    const clarifies = parsed.messages.filter((m) => {
      const meta = m.meta as Record<string, unknown> | undefined;
      return meta?.op === "clarification_question";
    });
    expect(clarifies.length).toBe(2);
    const layouts = clarifies.map(
      (m) => (m.meta as Record<string, unknown>).layout as string,
    );
    expect(layouts).toContain("list");
    expect(layouts).toContain("form");
    expect(researchMocks.snapshots.clarification.session.id).toBe("sess-clarify");
  });

  it("awaitingConfirm snapshot exposes awaiting_user_confirm for LRM-840 gate controls", () => {
    const parsed = ResearchSessionSnapshotSchema.parse(
      researchMocks.snapshots.awaitingConfirm,
    );
    expect(parsed.session.status).toBe("awaiting_user_confirm");
    expect(parsed.session.current_stage).toBe("s4_delivery");
    expect(parsed.session.id).toBe("sess-awaiting-confirm");
  });

  it("list mocks satisfy list schema", () => {
    expect(ListResearchSessionsResponseSchema.parse(mockResearchSessionsList).sessions).toHaveLength(1);
    expect(ListResearchSessionsResponseSchema.parse(mockResearchSessionsListEmpty).sessions).toHaveLength(0);
  });

  it("create response satisfies create schema", () => {
    const parsed = CreateResearchSessionResponseSchema.parse(mockCreateResearchSessionResponse);
    expect(parsed.session.id).toBe(mockResearchSnapshotDefault.session.id);
    expect(parsed.nodes.length).toBeGreaterThan(0);
    expect(parsed.messages.length).toBeGreaterThan(0);
  });

  it("mock api mirrors client surface", async () => {
    const [fleet, list, snapshot, presence] = await Promise.all([
      researchMocks.api.ensureResearchFleet(),
      researchMocks.api.listResearchSessions(),
      researchMocks.api.getResearchSessionSnapshot(),
      researchMocks.api.getResearchPresence(),
    ]);
    expect(fleet.members.length).toBeGreaterThan(0);
    expect(list.sessions).toHaveLength(1);
    expect(snapshot.session.id).toBe(mockResearchSnapshotDefault.session.id);
    expect(presence.presence["agent-mock-scout"].activity).toContain("benchmark");
  });
});
