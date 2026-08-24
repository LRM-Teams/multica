"use client";

import { useQuery } from "@tanstack/react-query";
import type { RuntimeModel } from "@multica/core/types";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import { InspectorField } from "./inspector-field";
import { useT } from "../../../i18n";
import { ThinkingPicker } from "./thinking-picker";

/**
 * Thinking field for the agent inspector's Runtime config block. (It renders
 * an InspectorField now, not a PropRow — the block moved to label-above-value
 * so that Model could stop trailing Runtime without a label of its own.) Hidden when the runtime doesn't
 * expose a reasoning/effort catalog at all (`thinkingDiscovery`, #59 —
 * backend-owned, never a hardcoded provider list) OR the active model
 * specifically has no `supported_levels` advertised, AND nothing is
 * persisted. `thinkingDiscovery` is checked first and independently of
 * `levels`: it's the coarser, runtime-level capability signal and is
 * treated as authoritative even in the (should-be-impossible) case where
 * stale per-model catalog data disagrees with it. If the agent already
 * has a `thinking_level` saved (model swap into a non-thinking runtime,
 * or the daemon / CLI catalog shrank and dropped the entry), we still
 * render the row so the user can see the orphan token the backend is
 * still sending and explicit-clear it via the picker footer. PR1's
 * per-model invalid behavior is daemon-side warn/drop, not a synchronous
 * DB clear, so the frontend has to surface the persisted state honestly.
 *
 * Reuses the shared runtime-models query so it hits the same 60s cache
 * as the model picker; no extra round-trip on the inspector's hot path.
 * The sibling ModelPicker mounts unconditionally next to this row, so
 * the shared query subscription is established by the inspector mount
 * itself — returning null here does NOT cancel discovery.
 */
export function ThinkingPropRow({
  runtimeId,
  model,
  value,
  canEdit,
  onChange,
}: {
  runtimeId: string | null;
  model: string;
  value: string;
  canEdit: boolean;
  onChange: (next: string) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const { data: catalog } = useQuery(runtimeModelsOptions(runtimeId));

  const models = catalog?.models ?? [];
  // Missing/undefined from older servers ⇒ false ⇒ row stays hidden (#59).
  const thinkingDiscovery = catalog?.thinkingDiscovery === true;
  const entry = pickModelEntry(models, model);
  const levels = entry?.thinking?.supported_levels ?? [];
  if (!value && (!thinkingDiscovery || levels.length === 0)) return null;

  return (
    <InspectorField label={t(($) => $.inspector.prop_thinking)}>
      <ThinkingPicker
        value={value}
        levels={levels}
        canEdit={canEdit}
        onChange={onChange}
      />
    </InspectorField>
  );
}

function pickModelEntry(
  models: RuntimeModel[],
  model: string,
): RuntimeModel | undefined {
  if (model) return models.find((m) => m.id === model);
  return models.find((m) => m.default);
}
