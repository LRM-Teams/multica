import { beforeEach, describe, expect, it } from "vitest";
import { useResearchV6DirectorDisplayStore } from "./director-display-store";

describe("Director V6 projection display store", () => {
  beforeEach(() => useResearchV6DirectorDisplayStore.getState().clear());

  it("drops all display state when projection identity changes", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.setProjectionIdentity("workspace", "run", "snapshot-a");
    store.selectNode("node-a");
    store.beginExpansion("node-a", "request-a");
    store.commitExpansion("node-a", "request-a", "slice-a", ["child"]);
    store.setProjectionIdentity("workspace", "run", "snapshot-b");
    expect(useResearchV6DirectorDisplayStore.getState()).toMatchObject({
      selectedNodeId: null,
      expandedByRoot: {},
      transition: null,
    });
  });

  it("ignores a stale expansion response", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.beginExpansion("root", "old");
    store.beginExpansion("root", "new");
    store.commitExpansion("root", "old", "stale-slice", ["stale"]);
    expect(useResearchV6DirectorDisplayStore.getState().expandedByRoot).toEqual(
      {},
    );
  });

  it("commits exact revealed ids and emits one expansion transaction", () => {
    const store = useResearchV6DirectorDisplayStore.getState();
    store.beginExpansion("root", "request");
    store.commitExpansion("root", "request", "slice", [
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
    store.commitExpansion("root", "request", "slice", ["child"]);
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
