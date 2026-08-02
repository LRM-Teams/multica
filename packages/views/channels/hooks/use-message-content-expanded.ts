"use client";

import { useCallback, useEffect, useState } from "react";

/**
 * Survives Virtuoso row remounts when expanding a long bubble (LRM-987).
 * Without this, 「查看更多」grows the row → list remeasures → remount →
 * local useState resets → collapse again → flicker / "can't open".
 *
 * Value is the collapse identity that was expanded (message id + body fingerprint).
 */
const expandedIdentityByMessageId = new Map<string, string>();

/** Test-only: clear remount-survival cache. */
export function resetMessageContentExpandedMemoryForTests(): void {
  expandedIdentityByMessageId.clear();
}

/**
 * Whether this message body's 「查看更多」is expanded, keyed so a recycled /
 * remounted bubble keeps the user's choice (same idea as LRM-690 process fold).
 */
export function useMessageContentExpanded(messageId: string, collapseIdentity: string): {
  contentExpanded: boolean;
  expand: () => void;
  collapse: () => void;
} {
  const [expandedForIdentity, setExpandedForIdentity] = useState<string | null>(() => {
    return expandedIdentityByMessageId.get(messageId) ?? null;
  });

  // Body edits / part-count changes invalidate a stale expand fingerprint.
  useEffect(() => {
    const saved = expandedIdentityByMessageId.get(messageId);
    if (saved == null) {
      setExpandedForIdentity(null);
      return;
    }
    if (saved !== collapseIdentity) {
      expandedIdentityByMessageId.delete(messageId);
      setExpandedForIdentity(null);
      return;
    }
    setExpandedForIdentity(saved);
  }, [messageId, collapseIdentity]);

  const expand = useCallback(() => {
    expandedIdentityByMessageId.set(messageId, collapseIdentity);
    setExpandedForIdentity(collapseIdentity);
  }, [messageId, collapseIdentity]);

  const collapse = useCallback(() => {
    expandedIdentityByMessageId.delete(messageId);
    setExpandedForIdentity(null);
  }, [messageId]);

  return {
    contentExpanded: expandedForIdentity === collapseIdentity,
    expand,
    collapse,
  };
}
