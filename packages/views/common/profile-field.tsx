import type { ReactNode } from "react";

/**
 * Labelled block inside a profile panel section. The agent and human panels
 * are deliberately separate components (they do not share features), but
 * their field chrome is one contract — keep it here so the two shells stay
 * visually identical without merging them.
 */
export function ProfileField({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      {children}
    </div>
  );
}
