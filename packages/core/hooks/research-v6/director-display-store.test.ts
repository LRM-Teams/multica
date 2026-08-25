import { beforeEach, describe, expect, it } from "vitest";
import { useResearchV6DirectorDisplayStore } from "./director-display-store";

describe("Director V6 projection display store", () => {
  beforeEach(() => useResearchV6DirectorDisplayStore.getState().clear());

  it("keeps committed expansion display while the same run rebases snapshots", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.setProjectionIdentity("workspace", "run", "snapshot-a");
    store.selectNode("node-a");
    store.beginExpansion("node-a", "request-a");
    store.commitExpansion(
      "node-a",
      "request-a",
      "snapshot-a",
      "slice-a",
      ["child"],
    );
    store.setProjectionIdentity("workspace", "run", "snapshot-b");
    expect(useResearchV6DirectorDisplayStore.getState()).toMatchObject({
      scope: "workspace:run",
      identity: "workspace:run:snapshot-b",
      selectedNodeId: null,
      expandedByRoot: {
        "node-a": {
          snapshotId: "snapshot-a",
          sliceKey: "slice-a",
          revealedNodeIds: ["child"],
        },
      },
      transition: null,
    });
  });

  it("drops expansion display when the run scope changes", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.setProjectionIdentity("workspace", "run-a", "snapshot-a");
    store.beginExpansion("node-a", "request-a");
    store.commitExpansion(
      "node-a",
      "request-a",
      "snapshot-a",
      "slice-a",
      ["child"],
    );

    store.setProjectionIdentity("workspace", "run-b", "snapshot-b");

    expect(useResearchV6DirectorDisplayStore.getState()).toMatchObject({
      scope: "workspace:run-b",
      expandedByRoot: {},
    });
  });

  it("ignores a stale expansion response", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.beginExpansion("root", "old");
    store.beginExpansion("root", "new");
    store.commitExpansion(
      "root",
      "old",
      "snapshot",
      "stale-slice",
      ["stale"],
    );
    expect(useResearchV6DirectorDisplayStore.getState().expandedByRoot).toEqual(
      {},
    );
  });

  it("commits exact revealed ids and emits one expansion transaction", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.beginExpansion("root", "request");
    store.commitExpansion("root", "request", "snapshot", "slice", [
      "root",
      "child-a",
      "child-a",
      "child-b",
    ]);
    expect(useResearchV6DirectorDisplayStore.getState().transition).toMatchObject({
      kind: "expand",
      rootNodeId: "root",
      revealedNodeIds: ["child-a", "child-b"],
    });
  });

  it("collapses an invalidated server slice without restoring canonical nodes", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.beginExpansion("root", "request");
    store.commitExpansion(
      "root",
      "request",
      "snapshot",
      "slice",
      ["child"],
    );
    store.invalidateSliceKeys(["slice"]);
    const state = useResearchV6DirectorDisplayStore.getState();
    expect(state.expandedByRoot).toEqual({});
    expect(state.transition).toMatchObject({
      kind: "collapse",
      rootNodeId: "root",
      revealedNodeIds: ["child"],
    });
  });
});
