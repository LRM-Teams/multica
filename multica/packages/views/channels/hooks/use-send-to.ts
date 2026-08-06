import { useCallback } from "react";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCreateOrFindDM } from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";

/** A candidate `send-to` target resolved from the composer action. */
export interface SendToTarget {
  type: "user" | "agent";
  id: string;
  /**
   * Workspace the target belongs to, when known. Absent = assumed local (the
   * common case where the picker only surfaces same-workspace actors); a value
   * that differs from the current workspace is rejected.
   */
  workspaceId?: string | null;
}

/** Context the guardrails evaluate a target against. */
export interface SendToContext {
  currentUserId: string | null;
  workspaceId: string;
}

/**
 * Why a `send-to` target was rejected. This is an INTERNAL discriminator for
 * telemetry / dev logging only — it is deliberately never surfaced to the user,
 * so a not-found target is indistinguishable from an unauthorized one (Iris
 * non-leaky standard: no target-existence disclosure).
 */
export type SendToRejection = "self" | "cross_workspace" | "unresolved";

export type SendToEvaluation =
  | { ok: true; target: { peer_type: "user" | "agent"; peer_id: string } }
  | { ok: false; reason: SendToRejection };

/**
 * Pure `send-to` guardrails: decide whether a 1:1 DM may be resolved-or-created
 * for `{ me, target }` before any server round-trip.
 *
 * - self-DM (a 1:1 with yourself) → rejected;
 * - cross-workspace target → rejected;
 * - missing/unresolved target → rejected.
 *
 * The rejection `reason` is for the caller's own logs; every rejection collapses
 * to one generic visible error at the UI layer (see {@link useSendTo}).
 */
export function evaluateSendToTarget(
  target: SendToTarget | null | undefined,
  ctx: SendToContext,
): SendToEvaluation {
  if (!target) return { ok: false, reason: "unresolved" };
  if (target.type === "user" && target.id === ctx.currentUserId) {
    return { ok: false, reason: "self" };
  }
  if (target.workspaceId != null && target.workspaceId !== ctx.workspaceId) {
    return { ok: false, reason: "cross_workspace" };
  }
  return { ok: true, target: { peer_type: target.type, peer_id: target.id } };
}

export interface SendToHandlers {
  /** The resolved DM to compose into (proceed to send). */
  onResolved: (dm: DMItem) => void;
  /**
   * The target could not be reached — self / cross-workspace / not-found /
   * unauthorized. Intentionally zero-arg so the visible layer CANNOT render a
   * reason-specific message (non-leaky by construction).
   */
  onUnavailable: () => void;
}

export interface SendTo {
  sendTo: (target: SendToTarget | null | undefined, handlers: SendToHandlers) => void;
}

/**
 * The `send-to` composer action: resolve-or-create the 1:1 DM for
 * `{ me, target }` then hand the caller the DM to send into. Applies the
 * {@link evaluateSendToTarget} guardrails locally (self / cross-workspace never
 * touch the server) and collapses BOTH a local rejection and a server
 * create-or-find failure into the single `onUnavailable` path, so a target that
 * does not exist is indistinguishable from one the caller may not reach.
 */
export function useSendTo(): SendTo {
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const workspaceId = useWorkspaceId();
  const { mutate: createOrFindDm } = useCreateOrFindDM();

  const sendTo = useCallback(
    (target: SendToTarget | null | undefined, handlers: SendToHandlers) => {
      const evaluation = evaluateSendToTarget(target, { currentUserId, workspaceId });
      if (!evaluation.ok) {
        handlers.onUnavailable();
        return;
      }
      createOrFindDm(evaluation.target, {
        onSuccess: (dm: DMItem) => handlers.onResolved(dm),
        // Server-side not-found / unauthorized → same generic path as a local
        // rejection. No error detail is passed through.
        onError: () => handlers.onUnavailable(),
      });
    },
    [currentUserId, workspaceId, createOrFindDm],
  );

  return { sendTo };
}
