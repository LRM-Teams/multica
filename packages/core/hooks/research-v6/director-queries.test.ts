import { describe, expect, it } from "vitest";
import { ApiError } from "../../api/client";
import {
  isResearchV6ProjectionResyncError,
  researchV6DirectorProjectionKeys,
  researchV6DirectorSlicePageRequest,
} from "./director-queries";

const WORKSPACE_ID = "00000000-0000-4000-8000-000000000001";
const RUN_ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";

describe("Director V6 projection query identities", () => {
  it("recognizes only the structured expired-snapshot response", () => {
    expect(
      isResearchV6ProjectionResyncError(
        new ApiError("expired", 409, "Conflict", {
          code: "research.v6.projection_resync_required",
        }),
      ),
    ).toBe(true);
    expect(
      isResearchV6ProjectionResyncError(
        new ApiError("other conflict", 409, "Conflict", {
          code: "research.v6.state_version_conflict",
        }),
      ),
    ).toBe(false);
    expect(
      isResearchV6ProjectionResyncError(
        new ApiError("wrong status", 500, "Internal Server Error", {
          code: "research.v6.projection_resync_required",
        }),
      ),
    ).toBe(false);
  });

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

  it("refreshes selected Work activity when its Projection revision changes", () => {
    const first = researchV6DirectorProjectionKeys.workActivity(
      WORKSPACE_ID,
      RUN_ID,
      "work-item-1",
      "2026-08-21T00:00:00Z",
    );
    const dispatched = researchV6DirectorProjectionKeys.workActivity(
      WORKSPACE_ID,
      RUN_ID,
      "work-item-1",
      "2026-08-21T00:00:01Z",
    );

    expect(first).not.toEqual(dispatched);
    expect(dispatched).toEqual([
      "research-v6-director-projection",
      WORKSPACE_ID,
      RUN_ID,
      "work-activity",
      "work-item-1",
      "2026-08-21T00:00:01Z",
    ]);
  });

  it("constructs only depth=1 requests for every cursor page", () => {
    expect(
      researchV6DirectorSlicePageRequest(
        { root: "insight:one", snapshotId: SNAPSHOT_ID },
        "opaque",
      ),
    ).toEqual({
      root: "insight:one",
      snapshotId: SNAPSHOT_ID,
      depth: 1,
      cursor: "opaque",
    });
  });
});
