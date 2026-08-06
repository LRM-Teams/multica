"use client";

import { Check, Circle, ListTodo, Network, NotebookPen, Loader2 } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import type {
  BubblePlanCard,
  BubbleSubagentItem,
  BubbleTodoItem,
} from "../lib/bubble-cursor-activity";

export function BubblePlanPanel({ plan }: { plan: BubblePlanCard }) {
  const { t } = useT("chat");
  const preview =
    plan.body.length > 480 ? `${plan.body.slice(0, 480)}…` : plan.body;
  return (
    <section
      className="rounded-lg border border-border/80 bg-muted/25 px-2.5 py-2 space-y-1"
      aria-label={t(($) => $.bubble_cursor.plan_title)}
    >
      <div className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
        <NotebookPen className="size-3 shrink-0" />
        <span>{plan.title?.trim() || t(($) => $.bubble_cursor.plan_title)}</span>
      </div>
      <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words text-xs leading-relaxed text-foreground/90">
        {preview}
      </pre>
    </section>
  );
}

export function BubbleTodoPanel({ todos }: { todos: BubbleTodoItem[] }) {
  const { t } = useT("chat");
  if (!todos.length) return null;
  const done = todos.filter((x) => x.status === "completed").length;
  return (
    <section
      className="rounded-lg border border-border/80 bg-muted/25 px-2.5 py-2 space-y-1.5"
      aria-label={t(($) => $.bubble_cursor.todo_title)}
    >
      <div className="flex items-center justify-between gap-2 text-[11px] font-medium text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <ListTodo className="size-3 shrink-0" />
          {t(($) => $.bubble_cursor.todo_title)}
        </span>
        <span className="font-mono tabular-nums">
          {t(($) => $.bubble_cursor.todo_progress, { done, total: todos.length })}
        </span>
      </div>
      <ul className="space-y-1">
        {todos.map((todo) => (
          <li key={todo.id} className="flex items-start gap-1.5 text-xs">
            <TodoStatusIcon status={todo.status} />
            <span
              className={cn(
                "min-w-0 flex-1 leading-snug",
                todo.status === "completed" && "text-muted-foreground line-through",
                todo.status === "cancelled" && "text-muted-foreground/70 line-through",
                todo.status === "in_progress" && "text-foreground",
              )}
            >
              {todo.content}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function TodoStatusIcon({ status }: { status: BubbleTodoItem["status"] }) {
  if (status === "completed") {
    return <Check className="mt-0.5 size-3 shrink-0 text-emerald-600" aria-hidden />;
  }
  if (status === "in_progress") {
    return <Loader2 className="mt-0.5 size-3 shrink-0 animate-spin text-foreground/70" aria-hidden />;
  }
  if (status === "cancelled") {
    return <Circle className="mt-0.5 size-3 shrink-0 text-muted-foreground/50" aria-hidden />;
  }
  return <Circle className="mt-0.5 size-3 shrink-0 text-muted-foreground/70" aria-hidden />;
}

export function BubbleSubagentPanel({ items }: { items: BubbleSubagentItem[] }) {
  const { t } = useT("chat");
  if (!items.length) return null;
  return (
    <section
      className="rounded-lg border border-border/80 bg-muted/25 px-2.5 py-2 space-y-1.5"
      aria-label={t(($) => $.bubble_cursor.subagent_title)}
    >
      <div className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
        <Network className="size-3 shrink-0" />
        <span>{t(($) => $.bubble_cursor.subagent_title)}</span>
        <span className="font-mono tabular-nums text-muted-foreground/80">
          {items.length}
        </span>
      </div>
      <ul className="space-y-1.5 border-l border-border/70 ml-1.5 pl-2.5">
        {items.map((item) => (
          <li key={item.id} className="min-w-0 space-y-0.5">
            <div className="flex items-center gap-1.5 text-xs">
              {item.status === "running" ? (
                <Loader2 className="size-3 shrink-0 animate-spin text-foreground/70" aria-hidden />
              ) : (
                <Check className="size-3 shrink-0 text-emerald-600" aria-hidden />
              )}
              <span className="truncate font-medium text-foreground">{item.title}</span>
              <span className="shrink-0 text-[10px] uppercase tracking-wide text-muted-foreground">
                {item.status === "running"
                  ? t(($) => $.bubble_cursor.subagent_running)
                  : t(($) => $.bubble_cursor.subagent_done)}
              </span>
            </div>
            {item.detail ? (
              <p className="pl-4 text-[11px] leading-snug text-muted-foreground line-clamp-2">
                {item.detail}
              </p>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
