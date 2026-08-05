/**
 * LRM-1366 gate-shot harness — the REAL `DmList` in the REAL conversation-list
 * chrome, fed by the REAL `dmListOptions` query over a REAL `fetch("/api/dm")`.
 *
 * Why a browser is required: the defect is a computed-colour identity. In the
 * light theme `--muted` (the shipped `Skeleton` fill) resolves to `--page-bg`,
 * which is also `--sidebar`, so the pending DM region paints three placeholder
 * rows at 1.00:1 against their own backdrop — invisible. jsdom resolves no
 * custom properties and paints nothing, so a unit test can only assert class
 * names; only Chromium can report "the placeholder and its backdrop are the
 * same pixel", which is what Frank's LRM-1364 screenshot actually shows.
 *
 * Query params:
 *   ?theme=light|dark
 *   ?state=pending|all-pinned|rows   (also drives the harness-state cookie the
 *                                     dev-server /api/dm middleware reads)
 *
 * Temporary tooling: delete after the shots are attached to LRM-1366.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { ApiClient, setApiInstance } from "../../packages/core/api";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { DmList } from "../../packages/views/channels/components/dm-list";
import zhChannels from "../../packages/views/locales/zh-Hans/channels.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const params = new URLSearchParams(window.location.search);
const state = params.get("state") ?? "pending";
document.cookie = `harness-state=${state}; path=/`;
document.documentElement.classList.add(
  params.get("theme") === "dark" ? "dark" : "light",
);

setApiInstance(new ApiClient(""));

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { channels: zhChannels, common: zhCommon },
});

const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});

/**
 * Same wrapper the product ships: `channels-page.tsx`'s `listPane` is an
 * `aside.bg-sidebar` with a scrolling `min-h-0 flex-1 overflow-y-auto px-2 pb-2`
 * body. The backdrop is the whole point of this harness, so it is copied
 * verbatim rather than approximated.
 */
function Harness() {
  return (
    <div className="flex h-screen min-h-0 bg-background">
      <aside className="flex min-h-0 w-full flex-col border-r border-border bg-sidebar md:w-72 md:shrink-0">
        <div className="flex items-center gap-2 px-4 pb-1 pt-4">
          <h2 className="flex-1 text-lg font-semibold">消息</h2>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
          <DmList activeId={null} currentUserName="Frank" onSelect={() => {}} />
          <div className="mt-1 px-2 py-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            频道
          </div>
        </div>
      </aside>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <Harness />
      </I18nextProvider>
    </QueryClientProvider>
  </StrictMode>,
);
