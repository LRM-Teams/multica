import { vi, type Mock } from "vitest";

/**
 * Shared `vi.mock` factories for the `channels-page-*` test files
 * (#1364 direction D / LRM-694 — maintainability, not a CI-time claim).
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
 * the consuming files. Currently 6 modules used by:
 *   channels-page-routing, message-actions, sidebar-preview, sidebar-skeleton,
 *   header-actions-overflow; plus hooks/paths/realtime/file-upload from
 *   channels-page.test.tsx (auth + dm stay inline there — different shapes).
 *
 * WHAT DOES NOT BELONG HERE. `core/api`, `core/channels`, `channel-message-list`
 * and per-suite auth/dm variants — they are per-test fixtures, not duplication.
 * Keep them in the file that owns them. Do not merge test files (§4 E was
 * SUPERSEDED by #1386).
 *
 * Every factory takes `importOriginal` and spreads it, so named exports the test
 * still needs (e.g. a real `ApiError` to construct) survive the mock.
 */
// Matches vitest's own `importOriginal`, which is a GENERIC function
// (`<T = unknown>() => Promise<T>`). Typing it as a plain `() => Promise<T>`
// never matches, which pushed every call site into `importOriginal as never` —
// the bluntest cast available, and one that would equally hide a real
// incompatibility later. The signature was the defect, not the call sites.
type ImportOriginal = <T = unknown>() => Promise<T>;

export const authMock = () => ({
  useAuthStore: (selector: (s: { user: { id: string; name: string } }) => unknown) =>
    selector({ user: { id: "user-1", name: "Alice" } }),
});

export const dmMock = async (
  importOriginal: ImportOriginal,
) => ({
  ...(await importOriginal<typeof import("@multica/core/dm")>()),
  dmListOptions: () => ({ queryKey: ["dm-list"], queryFn: async () => [] }),
});

/**
 * LRM-1399 — shared mock for the unified `@multica/core/conversations` module.
 * The page now reads ONE `GET /api/conversations` query as its single
 * CHANNELS+DM source, so every `channels-page-*` suite must provide a
 * `conversationsOptions` that rebuilds the items from its own channel fixture
 * (the shared `dmMock` keeps DMs empty, so no DM items here). Real split/
 * flatten helpers are preserved via `...actual`.
 *
 * `channelsFn` returns the file's channels array (fixture-dependent).
 */
export const conversationsMock = async (
  importOriginal: ImportOriginal,
  channelsFn: () => Array<{ id: string; [key: string]: unknown }>,
) => {
  const actual =
    await importOriginal<typeof import("@multica/core/conversations")>();
  return {
    ...actual,
    conversationsOptions: () => ({
      queryKey: ["conversations", "ws-1", "list"],
      queryFn: async () => ({
        items: channelsFn().map((channel) => ({ kind: "channel" as const, channel })),
        next_cursor: undefined,
      }),
      initialPageParam: null as string | null,
      getNextPageParam: () => undefined,
    }),
  };
};

export const hooksMock = async (
  importOriginal: ImportOriginal,
) => ({
  ...(await importOriginal<typeof import("@multica/core/hooks")>()),
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
  importOriginal: ImportOriginal,
) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    channels: () => "/w/test/channels",
    channelDetail: (id: string) => `/w/test/channels/${id}`,
  }),
});

export const realtimeMock = async (
  importOriginal: ImportOriginal,
): Promise<typeof import("@multica/core/realtime") & { useWSEvent: Mock }> => ({
  ...(await importOriginal<typeof import("@multica/core/realtime")>()),
  useWSEvent: vi.fn(),
});
