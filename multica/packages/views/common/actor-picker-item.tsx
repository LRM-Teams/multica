"use client";

import type { ReactNode } from "react";
import {
  resolveActorIdentityPresentation,
  type ActorIdentityFields,
} from "@multica/core/identity";
import { ActorAvatar } from "./actor-avatar";
import { ActorIdentityRow } from "./actor-identity-row";
import { PickerItem } from "../issues/components/pickers/property-picker";

export interface ActorPickerItemProps {
  actorType: "member" | "agent";
  actorId: string;
  identity: ActorIdentityFields;
  /** Fallback when identity fields are empty (usually actor id). */
  fallback: string;
  selected: boolean;
  onClick: () => void;
  disabled?: boolean;
  tooltip?: ReactNode;
  showStatusDot?: boolean;
  /** Weak @handle row. Defaults to identity rules. */
  showHandle?: boolean;
  /** Trailing affordance (e.g. private-agent lock icon). */
  trailing?: ReactNode;
  labelClassName?: string;
}

/**
 * Picker row primitive for member/agent surfaces: avatar + identity stack.
 * Converges assignee picker, agent picker, DM invite lists, etc.
 */
export function ActorPickerItem({
  actorType,
  actorId,
  identity,
  fallback,
  selected,
  onClick,
  disabled,
  tooltip,
  showStatusDot = actorType === "agent",
  showHandle,
  trailing,
  labelClassName,
}: ActorPickerItemProps) {
  const presentation = resolveActorIdentityPresentation(identity, fallback);

  return (
    <PickerItem selected={selected} disabled={disabled} onClick={onClick} tooltip={tooltip}>
      <ActorAvatar
        actorType={actorType}
        actorId={actorId}
        size={18}
        showStatusDot={showStatusDot}
      />
      <ActorIdentityRow
        displayName={presentation.displayName}
        handle={presentation.handle}
        showHandle={showHandle ?? presentation.showHandleLabel}
        primaryClassName={`truncate ${labelClassName ?? ""}`.trim()}
      />
      {trailing}
    </PickerItem>
  );
}