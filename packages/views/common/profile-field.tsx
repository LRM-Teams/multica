import type { ReactNode } from "react";
import { HelpCircle } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";

/**
 * Labelled block inside a profile panel section. The agent and human panels
 * are deliberately separate components (they do not share features), but
 * their field chrome is one contract — keep it here so the two shells stay
 * visually identical without merging them.
 *
 * The label sits a step below `ProfileSectionHeading` on purpose (Frank,
 * 2026-08-21). It used to be styled identically to a section heading, which
 * made "DISPLAY NAME" and "INFO" read as the same level and flattened the
 * panel. Smaller, lighter, wider-tracked keeps a field a field.
 *
 * The value slot imposes no layout: values bring their own (pickers are
 * inline-flex, `InlineFieldEditor` expands into a textarea), and a flex
 * wrapper here would fight both.
 */
export function ProfileField({
  label,
  hint,
  children,
}: {
  label: string;
  /**
   * Puts a question mark next to the label instead of spending a line on
   * explanatory text — and instead of a confirm dialog after the fact. What a
   * choice means belongs next to the choice, readable before it is made.
   */
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="flex items-center gap-1 text-[10px] font-medium uppercase tracking-[0.09em] text-muted-foreground/70">
        {label}
        {hint && (
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  aria-label={hint}
                  className="inline-flex rounded-full text-muted-foreground/50 transition-colors hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
              }
            >
              <HelpCircle className="size-3" aria-hidden />
            </TooltipTrigger>
            <TooltipContent side="top" className="max-w-64">
              {hint}
            </TooltipContent>
          </Tooltip>
        )}
      </span>
      <div className="min-w-0 text-[13px]">{children}</div>
    </div>
  );
}

/**
 * Section heading for a profile panel. One definition because the two shells
 * had drifted: the agent detail inspector used 10px/500/wider, the side
 * panels 11px/600/wide, for the same kind of heading over the same kind of
 * content. `action` holds the section's edit control beside the label.
 */
export function ProfileSectionHeading({
  label,
  action,
}: {
  label: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex items-center gap-1">
      <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      {action}
    </div>
  );
}
