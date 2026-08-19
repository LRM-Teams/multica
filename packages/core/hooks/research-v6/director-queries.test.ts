import { describe, expect, it } from "vitest";
import {
  researchV6DirectorProjectionKeys,
  researchV6DirectorSlicePageRequest,
} from "./director-queries";

const WORKSPACE_ID = "00000000-0000-4000-8000-000000000001";
const RUN_ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";

describe("Director V6 projection query identities", () => {
  it("separates workspace, run, snapshot, root, and fixed depth", () => {
    expect(
      researchV6DirectorProjectionKeys.slice(
        WORKSPACE_ID,
        RUN_ID,
        SNAPSHOT_ID,
        "insight:one",
      ),
    ).toEqual([
      "research-v6-director-projection",
      WORKSPACE_ID,
      RUN_ID,
      "slice",
      SNAPSHOT_ID,
      "insight:one",
      1,
    ]);
  });

  it("keeps compiled HTML off the JSON report identity", () => {
    expect(
      researchV6DirectorProjectionKeys.reportCompiled(
        WORKSPACE_ID,
        RUN_ID,
        "00000000-0000-4000-8000-000000000701",
      ),
    ).toEqual([
      "research-v6-director-projection",
      WORKSPACE_ID,
      RUN_ID,
      "reports",
      "00000000-0000-4000-8000-000000000701",
      "compiled",
    ]);
  });

  it("constructs only depth=1 requests for every cursor page", () => {
    expect(
      researchV6DirectorSlicePageRequest(
        { root: "insight:one", snapshot_id: SNAPSHOT_ID },
        "opaque",
      ),
    ).toEqual({
      root: "insight:one",
      snapshot_id: SNAPSHOT_ID,
      depth: 1,
      cursor: "opaque",
    });
  });
});
