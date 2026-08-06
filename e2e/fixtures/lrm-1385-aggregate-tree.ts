export type Lrm1385AggregateNode = {
  key: string;
  parentKey: string | null;
  childKeys: readonly string[];
  title: string;
  nodeType: "goal" | "probe" | "finding";
  themeKey: string;
  assessment: "trusted" | "pending_review" | "detour";
};

export type Lrm1385AggregateFixture = {
  nodes: readonly Lrm1385AggregateNode[];
  rootKey: string;
  branchKeys: readonly string[];
  initiallySelectedBranchKey: string;
  initialVisibleKeys: readonly string[];
  hiddenGroupCount: number;
};

const branchLeafCounts = [11, 11, 11, 11, 11, 11, 11, 10] as const;

/**
 * A deterministic 96-node session-shaped fixture for the aggregate-tree gate.
 * It intentionally exceeds the first visible window; tests must never weaken it
 * by generating a smaller graph when the UI cannot render the complete sample.
 */
export function createLrm1385AggregateFixture(): Lrm1385AggregateFixture {
  const rootKey = "root";
  const branchKeys = branchLeafCounts.map((_, index) => `branch-${index + 1}`);
  const nodes: Lrm1385AggregateNode[] = [];

  nodes.push({
    key: rootKey,
    parentKey: null,
    childKeys: branchKeys,
    title: "96-node research goal",
    nodeType: "goal",
    themeKey: "goal",
    assessment: "pending_review",
  });

  branchLeafCounts.forEach((leafCount, branchIndex) => {
    const branchKey = branchKeys[branchIndex];
    const childKeys = Array.from(
      { length: leafCount },
      (_, leafIndex) => `${branchKey}-leaf-${leafIndex + 1}`,
    );
    nodes.push({
      key: branchKey,
      parentKey: rootKey,
      childKeys,
      title: `Research group ${branchIndex + 1}`,
      nodeType: "probe",
      themeKey: `theme-${branchIndex + 1}`,
      assessment: branchIndex === 2 ? "detour" : "pending_review",
    });
    childKeys.forEach((key, leafIndex) => {
      nodes.push({
        key,
        parentKey: branchKey,
        childKeys: [],
        title: `Group ${branchIndex + 1} finding ${leafIndex + 1}`,
        nodeType: "finding",
        themeKey: `theme-${branchIndex + 1}`,
        assessment: leafIndex % 3 === 0 ? "trusted" : "pending_review",
      });
    });
  });

  const initiallySelectedBranchKey = branchKeys[0];
  const initialVisibleKeys = [
    rootKey,
    ...branchKeys,
    ...nodes.find((node) => node.key === initiallySelectedBranchKey)!.childKeys,
  ];

  return {
    nodes,
    rootKey,
    branchKeys,
    initiallySelectedBranchKey,
    initialVisibleKeys,
    hiddenGroupCount: branchKeys.length - 1,
  };
}

