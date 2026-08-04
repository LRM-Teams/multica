import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const here = import.meta.dirname;

/**
 * LRM-1366 gate-shot harness (temporary tooling).
 *
 * Mounts the REAL `DmList` inside the REAL conversation-list chrome
 * (`aside.bg-sidebar`, same classes as `channels-page.tsx`'s `listPane`) and
 * drives it through the REAL `dmListOptions` React Query + REAL `fetch`
 * against `/api/dm`, because the defect is a browser-CSS fact jsdom cannot
 * see: in the light theme `--muted` (the old `Skeleton` fill) resolves to the
 * same `#f6f6f4` as `--sidebar`, so the pending DM region paints three rows of
 * invisible placeholders and reads as blank.
 *
 * `/api/dm` is served here, not stubbed in the client:
 *   ?state=pending    → the request is accepted and never answered, so the
 *                       query stays genuinely `isPending` (the hard-refresh
 *                       window Frank screenshotted in LRM-1364)
 *   ?state=all-pinned → 200 with DMs that are all `pinned_at`, i.e. the second
 *                       silent hole (rows live in the PINNED section above)
 *   ?state=rows       → 200 with ordinary unpinned rows (control)
 * Every other `/api/*` GET answers `[]`: only the DM region is under test.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1366.
 */
const DMS = {
  rows: [
    {
      id: "dm-1",
      source: "dm_channel",
      peer: { type: "user", id: "user-frank", name: "Frank" },
      unread: 0,
      updated_at: "2026-08-04T08:00:00Z",
      last_message: {
        id: "m1",
        content: "私信刷新之后也半天才能显示出来",
        created_at: "2026-08-04T08:00:00Z",
        author_type: "user",
        author_id: "user-frank",
      },
    },
    {
      id: "dm-2",
      source: "dm_channel",
      peer: { type: "agent", id: "agent-beckham", name: "贝克汉姆" },
      unread: 2,
      real_unread: 2,
      updated_at: "2026-08-04T07:40:00Z",
      last_message: {
        id: "m2",
        content: "LRM-1366 归你，出根因和证据",
        created_at: "2026-08-04T07:40:00Z",
        author_type: "agent",
        author_id: "agent-beckham",
      },
    },
  ],
  "all-pinned": [
    {
      id: "dm-p1",
      source: "dm_channel",
      peer: { type: "user", id: "user-frank", name: "Frank" },
      unread: 0,
      pinned_at: "2026-08-04T06:00:00Z",
      updated_at: "2026-08-04T08:00:00Z",
    },
  ],
};

function dmApi() {
  return {
    name: "lrm1366-dm-api",
    configureServer(server: { middlewares: { use: (fn: unknown) => void } }) {
      server.middlewares.use(
        (
          req: { url?: string; headers: Record<string, string | undefined> },
          res: { setHeader: (k: string, v: string) => void; end: (b?: string) => void },
          next: () => void,
        ) => {
          const url = req.url ?? "";
          if (!url.startsWith("/api/")) return next();
          if (url.startsWith("/api/dm")) {
            // `api.listDMs()` is the shipped call — it cannot carry a harness
            // query param, so the case travels on the cookie main.tsx sets
            // before mount (fetch runs with `credentials: "include"`).
            const state =
              /harness-state=([\w-]+)/.exec(req.headers.cookie ?? "")?.[1] ?? "pending";
            // Held open on purpose: a real, never-settling GET /api/dm is the
            // only faithful way to hold React Query in `isPending`.
            if (state === "pending") return;
            res.setHeader("content-type", "application/json");
            res.end(JSON.stringify(DMS[state as keyof typeof DMS] ?? []));
            return;
          }
          res.setHeader("content-type", "application/json");
          res.end("[]");
        },
      );
    },
  };
}

function stubModules() {
  const rules = [
    [/^@multica\/core\/hooks$/, resolve(here, "stubs/core-hooks.ts")],
    [/^@multica\/core\/workspace\/hooks$/, resolve(here, "stubs/workspace-hooks.ts")],
    [/^\.\.\/\.\.\/common\/actor-avatar$/, resolve(here, "stubs/actor-avatar.tsx")],
    [/^\.\.\/\.\.\/common\/use-open-dm$/, resolve(here, "stubs/use-open-dm.ts")],
  ];
  const coreRoot = resolve(here, "../../packages/core");
  return {
    name: "lrm1366-stubs",
    enforce: "pre" as const,
    resolveId(source: string, importer?: string) {
      for (const [pattern, file] of rules) {
        if ((pattern as RegExp).test(source)) return file as string;
      }
      // `packages/core/dm/mutations.ts` reaches `useWorkspaceId` through the
      // relative `../hooks`, which the package-specifier rule above cannot see.
      // Scope it to importers inside packages/core so `packages/views/**`'s own
      // `../hooks` directories keep resolving normally.
      if (source === "../hooks" && importer?.startsWith(coreRoot)) {
        return resolve(here, "stubs/core-hooks.ts");
      }
      return null;
    },
  };
}

export default defineConfig({
  root: resolve(here, "."),
  plugins: [stubModules(), dmApi(), react(), tailwindcss()],
  server: { port: 5366, strictPort: true },
});
