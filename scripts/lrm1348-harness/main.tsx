/**
 * LRM-1348 gate-shot harness — mounts the REAL `ChannelPresenceCluster` so the
 * Working list Stop / Stop all sit inside the REAL Base UI Portal overlay
 * (`HoverCard` on a fine pointer, `Popover` on narrow) that the defect depends
 * on.
 *
 * Why a browser is required: the bug is a two-step browser behaviour that jsdom
 * does not implement. (1) Chromium moves focus to `<body>` when the element that
 * currently has focus becomes natively `disabled`. (2) The overlay treats that
 * focus-out as a dismiss and unmounts its whole subtree, so `Stop all` and the
 * other rows' `Stop` leave the DOM after a single keyboard Stop.
 *
 * The harness owns `stoppingTaskId` exactly like `channels-page.tsx` does: the
 * first Stop click latches the pending phase and never clears it, which is the
 * in-flight window the user is stuck in.
 *
 * Query params:
 *   ?theme=light|dark
 *
 * Temporary tooling: delete after the shots are attached to LRM-1348.
 */
import { StrictMode, useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { ChannelPresenceCluster } from "../../packages/views/channels/components/channel-agents-live-cue";
import type { ChannelActiveTask, ChannelMemberBrief } from "../../packages/core/types";
import zhChannels from "../../packages/views/locales/zh-Hans/channels.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { channels: zhChannels, common: zhCommon },
});

const params = new URLSearchParams(window.location.search);
document.documentElement.classList.add(
  params.get("theme") === "dark" ? "dark" : "light",
);

const members: ChannelMemberBrief[] = [
  {
    member_type: "user",
    member_id: "user-frank",
    name: "frank",
    display_name: "Frank",
    avatar_url: null,
  },
  {
    member_type: "agent",
    member_id: "agent-beckham",
    name: "beckham",
    display_name: "贝克汉姆",
    avatar_url: null,
  },
  {
    member_type: "agent",
    member_id: "agent-wendy",
    name: "wendy",
    display_name: "Wendy",
    avatar_url: null,
  },
  {
    member_type: "agent",
    member_id: "agent-nash",
    name: "nash",
    display_name: "Nash",
    avatar_url: null,
  },
];

/** Three live rows: enough that "Stop all" renders and the sibling rows are
 *  observably present or gone after the first keyboard Stop. */
const tasks: ChannelActiveTask[] = [
  {
    agent_id: "agent-beckham",
    agent_name: "贝克汉姆",
    task_id: "task-1",
    status: "running",
    kind: "reply",
    reason: "mention",
    inbox_event_id: "inbox-1",
  },
  {
    agent_id: "agent-wendy",
    agent_name: "Wendy",
    task_id: "task-2",
    status: "running",
    kind: "reply",
    reason: "mention",
    inbox_event_id: "inbox-2",
  },
  {
    agent_id: "agent-nash",
    agent_name: "Nash",
    task_id: "task-3",
    status: "queued",
    kind: "reply",
    reason: "mention",
    inbox_event_id: "inbox-3",
  },
];

function Harness() {
  // Same shape as `channels-page.tsx`: a single in-flight id, latched by the
  // click and only cleared when the stop request settles. The harness never
  // settles it, which is exactly the window under test.
  const [stoppingTaskId, setStoppingTaskId] = useState<string | null>(null);

  return (
    <div className="flex min-h-screen w-full flex-col gap-4 bg-background p-4">
      {/* A real focusable neighbour before the cluster, so a focus drop to
          <body> is distinguishable from focus landing on adjacent chrome. */}
      <button
        type="button"
        data-testid="harness-before-anchor"
        className="w-fit rounded-md border border-border px-2 py-1 text-xs text-muted-foreground"
      >
        前一个可聚焦控件
      </button>
      <div className="flex items-center justify-end">
        <ChannelPresenceCluster
          members={members}
          memberCount={4}
          agentCount={3}
          tasks={tasks}
          stoppingTaskId={stoppingTaskId}
          canStop
          onStopTask={(task) => setStoppingTaskId(task.task_id)}
          onStopAll={() => setStoppingTaskId("__all__")}
          onOpenMembers={() => {}}
        />
      </div>
      <output
        data-testid="harness-phase"
        className="w-fit font-mono text-[11px] text-muted-foreground"
      >
        stoppingTaskId={String(stoppingTaskId)}
      </output>
    </div>
  );
}

const client = new QueryClient({
  defaultOptions: { queries: { retry: false, staleTime: Infinity } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <Harness />
      </I18nextProvider>
    </QueryClientProvider>
  </StrictMode>,
);
