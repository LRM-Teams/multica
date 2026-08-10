"use client";

import * as React from "react";

/**
 * LRM-1386 — surface variant for inserted @mention tokens in the composer.
 *
 * `"default"` → the Slack soft-bg pill (issue comments, Activity, notes).
 * `"plain"`   → shell-less brand-ink text (chat composer: channel / DM / chat
 *               FAB). Carried as React context (not a Tiptap node attribute /
 *               extension option) so the presentational choice never leaks into
 *               serialized message content or the typed MentionOptions schema.
 */
export type MentionVariant = "default" | "plain";

const MentionVariantContext = React.createContext<MentionVariant>("default");

export function MentionVariantProvider({
  value,
  children,
}: {
  value: MentionVariant;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <MentionVariantContext.Provider value={value}>
      {children}
    </MentionVariantContext.Provider>
  );
}

export function useMentionVariant(): MentionVariant {
  return React.useContext(MentionVariantContext);
}
