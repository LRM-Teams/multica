import type { ReactNode } from "react";

/** LRM-494 — bordered section card for channel-details overview rows. */
export function ChannelDetailsSectionCard({
  title,
  children,
}: {
  title?: string;
  children: ReactNode;
}) {
  return (
    <section className="overflow-hidden rounded-xl border border-border bg-card">
      {title ? (
        <p className="border-b border-border px-3.5 py-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {title}
        </p>
      ) : null}
      <div className="divide-y divide-border">{children}</div>
    </section>
  );
}
