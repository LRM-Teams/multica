"use client";

import { HoverCard, HoverCardContent, HoverCardTrigger } from "@multica/ui/components/ui/hover-card";

export function NoteShareSummary({
  shareNames,
  sharedToPrefix,
  currentSharesLabel,
  sharedEtcLabel,
}: {
  shareNames: string[];
  sharedToPrefix: string;
  currentSharesLabel: string;
  sharedEtcLabel: string;
}) {
  if (shareNames.length === 0) return null;

  return (
    <span className="inline-flex min-w-0 items-center gap-1">
      <span className="text-muted-foreground">{sharedToPrefix}</span>
      <HoverCard>
        <HoverCardTrigger
          render={
            <button
              type="button"
              aria-label={currentSharesLabel}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
              }}
            />
          }
          className="inline-flex max-w-36 items-center truncate rounded-sm font-medium text-foreground/70 underline decoration-muted-foreground/50 underline-offset-2 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        >
          <span className="truncate">{shareNames[0]}</span>
        </HoverCardTrigger>
        <HoverCardContent align="start" className="w-64 max-w-[calc(100vw-2rem)] text-xs">
          <div className="mb-1.5 font-medium text-foreground">{currentSharesLabel}</div>
          <ul className="space-y-1">
            {shareNames.map((name, index) => (
              <li key={`${name}:${index}`} className="truncate text-muted-foreground">
                {name}
              </li>
            ))}
          </ul>
        </HoverCardContent>
      </HoverCard>
      {shareNames.length > 1 && <span className="text-muted-foreground/70">{sharedEtcLabel}</span>}
    </span>
  );
}
