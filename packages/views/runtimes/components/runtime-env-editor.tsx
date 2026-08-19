"use client";

import { api } from "@multica/core/api";
import { EnvEditor } from "../../agents/components/agent-env-editor";

/**
 * Runtime-level (machine-default) env editor. Auto-reveals on open and uses
 * the compact layout (plaintext values, no intro copy) so the dialog stays a
 * simple key/value list. Runtime env is the base layer; an agent's own
 * custom_env overrides it on key collision.
 */
export function RuntimeEnvEditor({
  runtimeId,
  onCancel,
  onSaved,
}: {
  runtimeId: string;
  onCancel?: () => void;
  onSaved?: () => void;
}) {
  return (
    <EnvEditor
      getEnv={() => api.getRuntimeEnv(runtimeId)}
      saveEnv={(env) => api.updateRuntimeEnv(runtimeId, { custom_env: env })}
      autoReveal
      simple
      onCancel={onCancel}
      onSaved={onSaved}
    />
  );
}
