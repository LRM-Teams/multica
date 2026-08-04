/**
 * LRM-1300 gate-shot harness — channel "remove member" confirm surface.
 *
 * BEFORE = the pre-#2119 markup (real Sheet primitives, side="bottom",
 * `inset-x-0` full-bleed, destructive Button `min-h-11 w-full`) exactly as it
 * stood at `b7d4af4:packages/views/channels/components/channels-page.tsx`.
 * AFTER  = the landed markup (real AlertDialog primitives) at `d718c1d`.
 * SPEC   = AFTER plus the design-gate proposal for the pending state
 *          (spinner + "移除中…" instead of a silent disabled button).
 *
 * Query params:
 *   ?variant=before|after|spec
 *   ?state=default|pending|longname
 *   ?theme=light|dark
 *
 * Temporary tooling: delete after the shots are attached to LRM-1300.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Loader2Icon } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../packages/ui/components/ui/alert-dialog";
import { Button } from "../../packages/ui/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "../../packages/ui/components/ui/sheet";
import zhChannels from "../../packages/views/locales/zh-Hans/channels.json";
import "./harness.css";

const params = new URLSearchParams(window.location.search);
const theme = params.get("theme") === "dark" ? "dark" : "light";
const variant = (params.get("variant") || "after") as "before" | "after" | "spec";
const state = (params.get("state") || "default") as
  | "default"
  | "pending"
  | "longname";
document.documentElement.classList.add(theme);

const SHORT_NAME = "贝克汉姆";
const LONG_NAME = "调研舰队·信源策略与人机边界审查代理（长名压力样本）";
const name = state === "longname" ? LONG_NAME : SHORT_NAME;
const pending = state === "pending";

const members = zhChannels.members as Record<string, string>;
const title = members.remove_confirm_title.replace("{{name}}", name);
const description = members.remove_confirm_description;
const confirmLabel = members.remove_confirm;
const cancelLabel = members.remove_cancel;
const confirmingLabel = members.remove_confirming;

/** Channel-page-ish backdrop so the overlay/width relation is honest. */
function PageBackdrop() {
  return (
    <div className="min-h-dvh bg-background p-4">
      <div className="mx-auto grid max-w-[1200px] gap-3 md:grid-cols-3">
        {Array.from({ length: 9 }).map((_, i) => (
          <div
            key={i}
            className="flex items-center gap-3 rounded-lg border border-border bg-card p-3"
          >
            <div className="size-9 shrink-0 rounded-full bg-muted" />
            <div className="grid gap-1.5">
              <div className="h-3 w-28 rounded bg-muted" />
              <div className="h-2.5 w-40 rounded bg-muted/70" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

/** BEFORE — pre-#2119 full-bleed bottom Sheet (real Sheet primitives). */
function BeforeSurface() {
  return (
    <Sheet open onOpenChange={() => {}}>
      <SheetContent
        side="bottom"
        showCloseButton={false}
        className="gap-0 rounded-t-2xl p-0"
        data-testid="lrm1300-surface"
      >
        <SheetHeader className="space-y-1 p-4 pb-2 text-left">
          <SheetTitle data-testid="lrm1300-title">{title}</SheetTitle>
          <SheetDescription data-testid="lrm1300-description">
            {description}
          </SheetDescription>
        </SheetHeader>
        <SheetFooter className="gap-2 p-4 pt-2" data-testid="lrm1300-footer">
          <Button
            type="button"
            variant="destructive"
            className="min-h-11 w-full"
            disabled={pending}
            data-testid="lrm1300-confirm"
          >
            {confirmLabel}
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="min-h-10 w-full"
            data-testid="lrm1300-cancel"
          >
            {cancelLabel}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

/** AFTER — landed AlertDialog (real primitives). SPEC adds the pending affordance. */
function AfterSurface({ spec }: { spec: boolean }) {
  const showSpinner = spec && pending;
  return (
    <AlertDialog open onOpenChange={() => {}}>
      <AlertDialogContent data-testid="lrm1300-surface">
        <AlertDialogHeader>
          <AlertDialogTitle data-testid="lrm1300-title">{title}</AlertDialogTitle>
          <AlertDialogDescription data-testid="lrm1300-description">
            {description}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter data-testid="lrm1300-footer">
          <AlertDialogCancel disabled={pending} data-testid="lrm1300-cancel">
            {cancelLabel}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending}
            data-testid="lrm1300-confirm"
          >
            {showSpinner ? (
              <>
                <Loader2Icon className="animate-spin" aria-hidden="true" />
                {confirmingLabel}
              </>
            ) : (
              confirmLabel
            )}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PageBackdrop />
    {variant === "before" ? (
      <BeforeSurface />
    ) : (
      <AfterSurface spec={variant === "spec"} />
    )}
  </StrictMode>,
);
