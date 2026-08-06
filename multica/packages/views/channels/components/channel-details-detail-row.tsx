import type { ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";

/** LRM-494 — single tappable/static row inside a details section card. */
export function ChannelDetailsDetailRow({
  icon,
  label,
  value,
  onClick,
  disabled,
  trailing,
  destructive,
  testId,
}: {
  icon: ReactNode;
  label: string;
  value?: string;
  onClick?: () => void;
  disabled?: boolean;
  trailing?: ReactNode;
  destructive?: boolean;
  testId?: string;
}) {
  const content = (
    <>
      <span
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground",
          destructive && "bg-destructive/10 text-destructive",
          disabled && "opacity-50",
        )}
      >
        {icon}
      </span>
      <span className="min-w-0 flex-1 text-left">
        <span
          className={cn(
            "block truncate text-sm font-medium",
            destructive ? "text-destructive" : "text-foreground",
            disabled && "text-muted-foreground",
          )}
        >
          {label}
        </span>
      </span>
      {value ? (
        <span className="shrink-0 text-sm text-muted-foreground">{value}</span>
      ) : null}
      {trailing}
      {onClick && !trailing ? (
        <ChevronRight
          className={cn("size-4 shrink-0 text-muted-foreground", disabled && "opacity-40")}
          aria-hidden="true"
        />
      ) : null}
    </>
  );

  if (!onClick) {
    return (
      <div data-testid={testId} className="flex min-h-11 items-center gap-3 px-3.5 py-2.5">
        {content}
      </div>
    );
  }

  return (
    <button
      type="button"
      data-testid={testId}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex min-h-11 w-full items-center gap-3 px-3.5 py-2.5 text-left transition-colors",
        disabled ? "cursor-not-allowed opacity-60" : "hover:bg-muted/60",
      )}
    >
      {content}
    </button>
  );
}
