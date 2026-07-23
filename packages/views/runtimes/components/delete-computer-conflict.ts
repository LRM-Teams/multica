import { ApiError } from "@multica/core/api";
import type { Agent } from "@multica/core/types";

export type ComputerDeleteConflictCode =
  | "computer_has_online_runtimes"
  | "computer_has_active_agents"
  | "computer_has_active_squads"
  | "computer_has_active_tasks";

export interface ComputerDeleteConflict {
  code: ComputerDeleteConflictCode;
  activeAgents: Agent[];
  message: string;
}

const CONFLICT_CODES = new Set<string>([
  "computer_has_online_runtimes",
  "computer_has_active_agents",
  "computer_has_active_squads",
  "computer_has_active_tasks",
]);

export function parseComputerDeleteConflict(
  err: unknown,
): ComputerDeleteConflict | null {
  if (!(err instanceof ApiError)) return null;
  if (err.status !== 409) return null;
  const body = err.body;
  if (!body || typeof body !== "object") return null;
  const record = body as Record<string, unknown>;
  const code = record.code;
  if (typeof code !== "string" || !CONFLICT_CODES.has(code)) return null;

  const message =
    typeof record.error === "string" && record.error
      ? record.error
      : err.message;

  let activeAgents: Agent[] = [];
  if (code === "computer_has_active_agents") {
    const rawAgents = record.active_agents;
    if (Array.isArray(rawAgents)) {
      activeAgents = rawAgents.filter(
        (a): a is Agent =>
          typeof a === "object" &&
          a !== null &&
          typeof (a as Record<string, unknown>).id === "string" &&
          typeof (a as Record<string, unknown>).name === "string",
      );
    }
  }

  return {
    code: code as ComputerDeleteConflictCode,
    activeAgents,
    message,
  };
}
