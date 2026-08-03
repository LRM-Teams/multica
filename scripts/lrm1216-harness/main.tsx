/**
 * LRM-1216 gate-shot harness — real AgentOpenDmButton in a list+detail chrome
 * that mirrors the Agents page entry points (rail icon box + labeled header).
 * Temporary tooling: delete after shots are attached.
 */
import { StrictMode, useState } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { AgentOpenDmButton } from "../../packages/views/agents/components/agent-open-dm-button";
import zhAgents from "../../packages/views/locales/zh-Hans/agents.json";
import { cn } from "../../packages/ui/lib/utils";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { agents: zhAgents },
});

const AGENTS = [
  { id: "a1", name: "Atlas", desc: "前端缺陷与刀单", selected: true },
  { id: "a2", name: "Beckham", desc: "群管 / 刀单分发", selected: false },
  { id: "a3", name: "Morgan", desc: "产品终审", selected: false },
];

function FakeAvatar({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="inline-flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground"
    >
      {name.slice(0, 1)}
    </span>
  );
}

function AgentsListSurface() {
  const [selectedId, setSelectedId] = useState(AGENTS[0].id);
  const selected = AGENTS.find((a) => a.id === selectedId) ?? AGENTS[0];

  return (
    <div
      data-testid="lrm1216-agents-surface"
      className="mx-auto flex min-h-[520px] max-w-5xl overflow-hidden rounded-lg border bg-background shadow-sm"
    >
      <div className="flex w-[320px] shrink-0 flex-col border-r">
        <div className="flex h-12 items-center gap-2 px-4">
          <h2 className="text-sm font-semibold">全部智能体</h2>
          <span className="font-mono text-xs text-muted-foreground/60">
            {AGENTS.length}
          </span>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto py-1">
          {AGENTS.map((agent) => {
            const selectedRow = agent.id === selectedId;
            return (
              <div
                key={agent.id}
                data-testid={`lrm1216-agent-row-${agent.id}`}
                className={cn(
                  "flex w-full items-stretch border-l-2 transition-colors",
                  selectedRow
                    ? "border-primary bg-accent"
                    : "border-transparent hover:bg-accent/50",
                )}
              >
                <button
                  type="button"
                  onClick={() => setSelectedId(agent.id)}
                  className="flex min-w-0 flex-1 items-center gap-3 py-2.5 pl-3 pr-1.5 text-left"
                >
                  <FakeAvatar name={agent.name} />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium">{agent.name}</p>
                    <p className="truncate text-xs text-muted-foreground">
                      {agent.desc}
                    </p>
                  </div>
                </button>
                <div className="flex shrink-0 items-center pr-2">
                  <AgentOpenDmButton agentId={agent.id} variant="icon" />
                </div>
              </div>
            );
          })}
        </div>
      </div>

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-start justify-between gap-3 border-b px-6 py-4">
          <div className="flex min-w-0 items-center gap-3">
            <FakeAvatar name={selected.name} />
            <div className="min-w-0">
              <p className="truncate text-base font-semibold">{selected.name}</p>
              <p className="mt-0.5 truncate text-xs text-muted-foreground">
                {selected.desc}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
            <AgentOpenDmButton agentId={selected.id} variant="labeled" />
          </div>
        </div>
        <div className="flex flex-1 items-center justify-center p-6 text-sm text-muted-foreground">
          列表右侧消息框 / 详情顶栏「私聊」→ 开或建 DM
        </div>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <div className="min-h-screen bg-background p-6">
        <p className="mb-4 text-xs text-muted-foreground">
          LRM-1216 · Agents 页每条 Agent 进私聊入口（与 LRM-283 资料卡分轨）
        </p>
        <AgentsListSurface />
      </div>
    </I18nextProvider>
  </StrictMode>,
);
