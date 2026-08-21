"use client";

import type { Agent, AgentRuntime, AgentRuntimeConfig, MemberWithUser } from "@multica/core/types";
import { ComputerInfoRow } from "./computer-info-row";
import { InspectorField } from "./inspector-field";
import { ModelPicker } from "./model-picker";
import { RuntimePicker } from "./runtime-picker";
import { ThinkingPropRow } from "./thinking-prop-row";
import { useT } from "../../../i18n";

/**
 * The read-only Runtime config block, shared by the agent detail inspector and
 * the agent side panel. Both used to hand-roll it — one with PropRow, one with
 * its own grid — which is how they drifted into showing the same four values
 * with different alignment and a different set of labels.
 *
 * Layout follows the hierarchy rather than flattening it (Frank, 2026-08-21):
 * the Computer sits on its own, and what runs on it groups below. A Computer's
 * daemon core hosts one runtime per provider, and the model and reasoning
 * level are properties of that runtime — so machine-then-configuration is what
 * the wider gap encodes, not decoration.
 *
 * Every value carries its own label now. Model previously trailed the Runtime
 * value unlabelled, which made "what am I looking at" a guess.
 */
export function RuntimeConfigBlock({
  agent,
  runtimeConfig,
  runtimes,
  members,
  currentUserId,
  wsId,
}: {
  agent: Agent;
  runtimeConfig: AgentRuntimeConfig | undefined;
  runtimes: AgentRuntime[];
  members: readonly MemberWithUser[];
  currentUserId: string | null;
  wsId: string;
}) {
  const { t } = useT("agents");

  return (
    <div className="flex flex-col gap-5">
      {/* The machine group is set apart by space, not a rule: a hairline here
          cut a small block in half and read as a divider between sections
          rather than a nesting level (Frank, 2026-08-21). */}
      <InspectorField label={t(($) => $.inspector.prop_computer)}>
        <ComputerInfoRow computer={runtimeConfig?.computer ?? null} />
      </InspectorField>
      <div className="flex flex-wrap gap-x-7 gap-y-4">
        <InspectorField label={t(($) => $.inspector.prop_runtime)}>
          <RuntimePicker
            value={agent.runtime_id}
            runtimes={runtimes}
            members={[...members]}
            currentUserId={currentUserId}
            canEdit={false}
            wsId={wsId}
            selectedProvider={runtimeConfig?.runtime?.provider ?? null}
            onChange={() => {}}
          />
        </InspectorField>
        <InspectorField label={t(($) => $.inspector.prop_model)}>
          <ModelPicker
            runtimeId={agent.runtime_id}
            value={runtimeConfig?.model ?? agent.model ?? ""}
            canEdit={false}
            onChange={() => {}}
          />
        </InspectorField>
        {/* Renders nothing when the runtime advertises no reasoning levels
            and none is persisted — see ThinkingPropRow. */}
        <ThinkingPropRow
          runtimeId={agent.runtime_id}
          model={runtimeConfig?.model ?? agent.model ?? ""}
          value={runtimeConfig?.thinking ?? agent.thinking_level ?? ""}
          canEdit={false}
          onChange={() => {}}
        />
      </div>
    </div>
  );
}
