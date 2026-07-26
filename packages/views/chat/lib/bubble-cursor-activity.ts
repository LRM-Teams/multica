import type { ChatTimelineItem } from "@multica/core/chat";

export type BubbleToolKind = "todo" | "task" | "plan" | "other";

export type BubbleTodoStatus = "pending" | "in_progress" | "completed" | "cancelled";

export interface BubbleTodoItem {
  id: string;
  content: string;
  status: BubbleTodoStatus;
}

export interface BubbleSubagentItem {
  id: string;
  title: string;
  detail?: string;
  status: "running" | "done";
  tool: string;
}

export interface BubblePlanCard {
  title?: string;
  body: string;
}

export interface BubbleCursorPanels {
  plan: BubblePlanCard | null;
  todos: BubbleTodoItem[];
  subagents: BubbleSubagentItem[];
}

/** Normalize Cursor / Multica tool slugs for matching. */
export function normalizeToolSlug(tool?: string | null): string {
  return (tool ?? "").toLowerCase().replace(/[-_\s]/g, "");
}

export function classifyBubbleToolKind(tool?: string | null): BubbleToolKind {
  const n = normalizeToolSlug(tool);
  if (!n) return "other";
  if (n.includes("todo") || n.includes("todolist") || n === "settodolist") return "todo";
  if (
    n.includes("createplan") ||
    n.includes("updateplan") ||
    n === "plan" ||
    n.endsWith("plan") ||
    n.includes("planmode")
  ) {
    return "plan";
  }
  if (
    n === "task" ||
    n.startsWith("task") ||
    n.includes("subagent") ||
    n.includes("bestofn") ||
    n.includes("launchagent")
  ) {
    return "task";
  }
  return "other";
}

export function friendlyBubbleToolLabel(tool?: string | null): string {
  const raw = (tool ?? "").trim();
  if (!raw) return "tool";
  const slug = raw.toLowerCase();
  const map: Record<string, string> = {
    read_file: "Read",
    read: "Read",
    write_file: "Write",
    write: "Write",
    edit_file: "Edit",
    edit: "Edit",
    multi_edit: "Edit",
    shell: "Shell",
    bash: "Shell",
    exec: "Shell",
    run_terminal_cmd: "Shell",
    grep: "Grep",
    glob: "Glob",
    glob_file_search: "Glob",
    web_search: "Web",
    websearch: "Web",
    todo_write: "Todo",
    todowrite: "Todo",
    set_todo_list: "Todo",
    task: "Task",
    create_plan: "Plan",
    update_plan: "Plan",
  };
  if (map[slug]) return map[slug];
  const kind = classifyBubbleToolKind(slug);
  if (kind === "todo") return "Todo";
  if (kind === "task") return "Task";
  if (kind === "plan") return "Plan";
  return raw;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function coerceStatus(raw: unknown): BubbleTodoStatus {
  const s = String(raw ?? "")
    .toLowerCase()
    .replace(/[-\s]/g, "_");
  if (s === "completed" || s === "complete" || s === "done") return "completed";
  if (s === "in_progress" || s === "inprogress" || s === "running" || s === "active") {
    return "in_progress";
  }
  if (s === "cancelled" || s === "canceled" || s === "skipped") return "cancelled";
  return "pending";
}

function todoContent(entry: Record<string, unknown>): string {
  for (const key of ["content", "text", "title", "description", "task"]) {
    const v = entry[key];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
}

/** Parse the latest TodoWrite-shaped tool input into checklist items. */
export function extractTodoItems(input?: Record<string, unknown> | null): BubbleTodoItem[] {
  if (!input) return [];
  const list =
    (Array.isArray(input.todos) && input.todos) ||
    (Array.isArray(input.items) && input.items) ||
    (Array.isArray(input.tasks) && input.tasks) ||
    null;
  if (!list) return [];
  const out: BubbleTodoItem[] = [];
  list.forEach((raw, index) => {
    const entry = asRecord(raw);
    if (!entry) return;
    const content = todoContent(entry);
    if (!content) return;
    const id =
      (typeof entry.id === "string" && entry.id) ||
      (typeof entry.uuid === "string" && entry.uuid) ||
      `todo-${index}`;
    out.push({ id, content, status: coerceStatus(entry.status) });
  });
  return out;
}

function extractPlanCard(input?: Record<string, unknown> | null): BubblePlanCard | null {
  if (!input) return null;
  const title =
    (typeof input.name === "string" && input.name.trim()) ||
    (typeof input.title === "string" && input.title.trim()) ||
    undefined;
  for (const key of ["plan", "content", "overview", "summary", "text"]) {
    const v = input[key];
    if (typeof v === "string" && v.trim()) {
      return { title, body: v.trim() };
    }
  }
  // Some runtimes nest under `plan: { content }`
  const nested = asRecord(input.plan);
  if (nested) {
    for (const key of ["content", "overview", "summary", "text", "body"]) {
      const v = nested[key];
      if (typeof v === "string" && v.trim()) {
        return {
          title:
            title ||
            (typeof nested.name === "string" ? nested.name : undefined) ||
            (typeof nested.title === "string" ? nested.title : undefined),
          body: v.trim(),
        };
      }
    }
  }
  return null;
}

function extractSubagentItem(item: ChatTimelineItem): BubbleSubagentItem | null {
  const input = item.input ?? null;
  const title =
    (typeof input?.description === "string" && input.description.trim()) ||
    (typeof input?.name === "string" && input.name.trim()) ||
    (typeof input?.subagent_type === "string" && String(input.subagent_type)) ||
    (typeof input?.prompt === "string" && String(input.prompt).slice(0, 80)) ||
    friendlyBubbleToolLabel(item.tool);
  const detail =
    (typeof input?.prompt === "string" && input.prompt.trim()) ||
    (typeof input?.description === "string" && input.description.trim()) ||
    undefined;
  return {
    id: `subagent-${item.seq}`,
    title,
    detail: detail && detail !== title ? detail.slice(0, 240) : undefined,
    status: "running",
    tool: item.tool ?? "task",
  };
}

/**
 * Derive Cursor-like Plan / Todo / Subagent panels from a timeline middle
 * segment. Later matching tool_use wins for todos/plan; all task tools collect.
 */
export function deriveBubbleCursorPanels(items: ChatTimelineItem[]): BubbleCursorPanels {
  let plan: BubblePlanCard | null = null;
  let todos: BubbleTodoItem[] = [];
  const subagents: BubbleSubagentItem[] = [];
  const taskResults = new Map<string, boolean>();

  for (const item of items) {
    if (item.type === "tool_result" && item.tool) {
      // Pair by tool name loosely — mark latest task of that name done.
      for (let i = subagents.length - 1; i >= 0; i--) {
        const s = subagents[i];
        if (s && normalizeToolSlug(s.tool) === normalizeToolSlug(item.tool) && s.status === "running") {
          s.status = "done";
          taskResults.set(s.id, true);
          break;
        }
      }
      continue;
    }
    if (item.type !== "tool_use") continue;
    const kind = classifyBubbleToolKind(item.tool);
    if (kind === "todo") {
      const parsed = extractTodoItems(item.input ?? null);
      if (parsed.length) todos = parsed;
      continue;
    }
    if (kind === "plan") {
      const card = extractPlanCard(item.input ?? null);
      if (card) plan = card;
      continue;
    }
    if (kind === "task") {
      const sub = extractSubagentItem(item);
      if (sub) subagents.push(sub);
    }
  }

  // Mark trailing tasks without results as still running; earlier ones without
  // results stay running too (accurate enough for bubble tree).
  void taskResults;

  return { plan, todos, subagents };
}

/** Active (last non-text) tool summary for the enhanced process fold header. */
export function activeBubbleStepSummary(items: ChatTimelineItem[]): string | null {
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i];
    if (!item || item.type === "text") continue;
    if (item.type === "thinking") return "Thinking";
    if (item.type === "tool_use") {
      const label = friendlyBubbleToolLabel(item.tool);
      const summary = bubbleToolSummary(item);
      return summary ? `${label} · ${summary}` : label;
    }
    if (item.type === "error") return "Error";
  }
  return null;
}

export function bubbleToolSummary(item: ChatTimelineItem): string {
  if (!item.input) return "";
  const inp = item.input as Record<string, unknown>;
  for (const key of [
    "query",
    "file_path",
    "path",
    "target_file",
    "pattern",
    "description",
    "command",
    "cmd",
    "prompt",
    "skill",
  ]) {
    const v = inp[key];
    if (typeof v === "string" && v.trim()) {
      const s = v.trim();
      if (key === "file_path" || key === "path" || key === "target_file") return shortenPath(s);
      return s.length > 80 ? `${s.slice(0, 80)}…` : s;
    }
  }
  for (const v of Object.values(inp)) {
    if (typeof v === "string" && v.trim() && v.trim().length < 120) {
      return v.trim();
    }
  }
  return "";
}

function shortenPath(p: string): string {
  const parts = p.split("/");
  if (parts.length <= 3) return p;
  return `.../${parts.slice(-2).join("/")}`;
}
