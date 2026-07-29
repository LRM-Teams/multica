/* eslint-disable import-x/no-extraneous-dependencies -- test fixture: vitest types for hoisted vi.mock factories */
import { vi, type Mock } from "vitest";

/**
 * Shared `vi.mock` factories for the `channels-page-*` test files (#1364 step 2).
 *
 * WHY THIS SHAPE. `vi.mock` is hoisted above imports, so a factory cannot be
 * referenced as an imported binding — doing so fails with
 * `ReferenceError: Cannot access '__vi_import_0__' before initialization`.
 * The factory *body*, however, runs at mock-resolution time, so a dynamic
 * `import()` inside it is legal. Call sites therefore look like:
 *
 *     vi.mock("@multica/core/paths", async (importOriginal) => {
 *       const { pathsMock } = await import("./__fixtures/channels-page-mocks");
 *       return pathsMock(importOriginal);
 *     });
 *
 * The `vi.mock` call stays in the test file (hoisting requires it); only the
 * body is shared. Overriding one module in one file means not calling the
 * shared factory there — no opt-out machinery needed.
 *
 * WHAT BELONGS HERE. Only factories that were already byte-identical across
 * every `channels-page-*` file. Measured 2026-07-29: 6 of the 15 commonly
 * mocked modules, 26 lines per file × 7 files = 182 duplicated lines.
 *
 * WHAT DOES NOT BELONG HERE. `core/api`, `core/channels`, `channel-message-list`
 * and `dm-conversation` have a *different* factory in all seven files — they are
 * per-test fixtures, not duplication, and forcing them in would be the mistake
 * this document warns about. Keep them in the file that owns them.
 *
 * Every factory takes `importOriginal` and spreads it, so named exports the test
 * still needs (e.g. a real `ApiError` to construct) survive the mock.
 */
type ImportOriginal<T> = () => Promise<T>;

export const authMock = () => ({
  useAuthStore: (selector: (s: { user: { id: string; name: string } }) => unknown) =>
    selector({ user: { id: "user-1", name: "Alice" } }),
});

export const dmMock = async (
  importOriginal: ImportOriginal<typeof import("@multica/core/dm")>,
) => ({
  ...(await importOriginal()),
  dmListOptions: () => ({ queryKey: ["dm-list"], queryFn: async () => [] }),
});

export const hooksMock = async (
  importOriginal: ImportOriginal<typeof import("@multica/core/hooks")>,
) => ({
  ...(await importOriginal()),
  useWorkspaceId: () => "ws-1",
});

// TS2742: an inferred `vi.fn()` type cannot be named without pointing at
// @vitest/spy's path inside .pnpm, which is not portable. Annotate explicitly.
export const fileUploadMock = (): {
  useFileUpload: () => { uploadWithToast: Mock };
} => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
});

export const pathsMock = async (
  importOriginal: ImportOriginal<typeof import("@multica/core/paths")>,
) => ({
  ...(await importOriginal()),
  useWorkspacePaths: () => ({
    channels: () => "/w/test/channels",
    channelDetail: (id: string) => `/w/test/channels/${id}`,
  }),
});

export const realtimeMock = async (
  importOriginal: ImportOriginal<typeof import("@multica/core/realtime")>,
): Promise<typeof import("@multica/core/realtime") & { useWSEvent: Mock }> => ({
  ...(await importOriginal()),
  useWSEvent: vi.fn(),
});
