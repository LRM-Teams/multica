"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, type DevicePending } from "@multica/core/api";
import { useLogout } from "../auth";
import { DragStrip } from "../platform";
import { Time } from "../i18n/time";
import { useT } from "../i18n";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { LogOut, MonitorSmartphone, ShieldAlert } from "lucide-react";

export interface DeviceConfirmPageProps {
  /**
   * `user_code` read from the CLI-printed link (`?user_code=XXXX-XXXX`).
   * Null when the link is missing/malformed the code entirely — the page
   * has no manual-entry fallback (Frank/Parker 2026-07-31: zero input,
   * ever; the recovery path is re-running the CLI command, not typing).
   */
  userCode: string | null;
}

type ConfirmOutcome = "approved" | "denied";

/**
 * `/device` — RFC 8628 device-code confirmation (task #36). The CLI opens
 * this page with the code already in the URL; the only interaction is
 * Approve/Deny. There is deliberately no code-entry UI (see userCode prop
 * doc) — every failure mode routes back to "re-run the CLI command."
 */
export function DeviceConfirmPage({ userCode }: DeviceConfirmPageProps) {
  const { t } = useT("device");
  const [outcome, setOutcome] = useState<ConfirmOutcome | null>(null);
  const [confirming, setConfirming] = useState<ConfirmOutcome | null>(null);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  // Set when a confirm click 404s (code expired/consumed between page load
  // and click) — same terminal "expired" UI as a 404 on the initial fetch,
  // tracked separately from react-query state since setQueryData(key,
  // undefined) is a documented no-op (an updater returning undefined means
  // "don't change the cache", not "clear it").
  const [raceExpired, setRaceExpired] = useState(false);

  const {
    data: pending,
    isLoading,
    error: fetchError,
  } = useQuery<DevicePending>({
    queryKey: ["device-pending", userCode],
    queryFn: () => api.getDevicePending(userCode as string),
    enabled: !!userCode,
    retry: false,
  });

  const notFound =
    !userCode ||
    raceExpired ||
    (fetchError instanceof ApiError && fetchError.status === 404);

  const handleConfirm = async (approve: boolean) => {
    if (!userCode) return;
    const kind: ConfirmOutcome = approve ? "approved" : "denied";
    setConfirming(kind);
    setConfirmError(null);
    try {
      const res = await api.confirmDevice(userCode, approve);
      setOutcome(res.status);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        // The code expired/was consumed between page load and click —
        // same "go re-run the CLI" recovery as the initial fetch 404.
        setRaceExpired(true);
      } else {
        setConfirmError(
          e instanceof Error ? e.message : t(($) => $.errors.confirm_failed),
        );
      }
    } finally {
      setConfirming(null);
    }
  };

  if (outcome) {
    return (
      <DeviceShell>
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-4 py-12 text-center">
            <div
              className={
                outcome === "approved"
                  ? "flex h-12 w-12 items-center justify-center rounded-full bg-primary/10"
                  : "flex h-12 w-12 items-center justify-center rounded-full bg-muted"
              }
            >
              <MonitorSmartphone
                className={
                  outcome === "approved"
                    ? "h-6 w-6 text-primary"
                    : "h-6 w-6 text-muted-foreground"
                }
              />
            </div>
            <h2 className="text-lg font-semibold">
              {outcome === "approved"
                ? t(($) => $.done.approved_title)
                : t(($) => $.done.denied_title)}
            </h2>
            <p className="text-sm text-muted-foreground">
              {outcome === "approved"
                ? t(($) => $.done.approved_description)
                : t(($) => $.done.denied_description)}
            </p>
          </CardContent>
        </Card>
      </DeviceShell>
    );
  }

  if (isLoading && userCode) {
    return (
      <DeviceShell>
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-4 py-12">
            <Skeleton className="h-12 w-12 rounded-full" />
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-4 w-64" />
          </CardContent>
        </Card>
      </DeviceShell>
    );
  }

  if (notFound || !pending) {
    return (
      <DeviceShell>
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-4 py-12 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted">
              <ShieldAlert className="h-6 w-6 text-muted-foreground" />
            </div>
            <h2 className="text-lg font-semibold">{t(($) => $.not_found.title)}</h2>
            <p className="text-sm text-muted-foreground">
              {t(($) => $.not_found.description)}
            </p>
          </CardContent>
        </Card>
      </DeviceShell>
    );
  }

  return (
    <DeviceShell>
      <Card className="w-full max-w-md">
        <CardContent className="flex flex-col items-center gap-6 py-12">
          <div className="flex h-14 w-14 items-center justify-center rounded-full bg-primary/10">
            <MonitorSmartphone className="h-7 w-7 text-primary" />
          </div>

          <div className="text-center space-y-2">
            <h2 className="text-xl font-semibold">{t(($) => $.main.title)}</h2>
            <p className="text-sm text-muted-foreground">
              {pending.client_hint
                ? t(($) => $.main.requested_from, { client_hint: pending.client_hint })
                : t(($) => $.main.requested_from_unknown)}
            </p>
            <p className="text-sm text-muted-foreground">
              <Time kind="relative" value={pending.created_at} />
            </p>
          </div>

          <div className="flex gap-3 w-full">
            <Button
              variant="outline"
              className="flex-1"
              onClick={() => handleConfirm(false)}
              disabled={confirming !== null}
            >
              {confirming === "denied" ? t(($) => $.main.denying) : t(($) => $.main.deny)}
            </Button>
            <Button
              className="flex-1"
              onClick={() => handleConfirm(true)}
              disabled={confirming !== null}
            >
              {confirming === "approved" ? t(($) => $.main.approving) : t(($) => $.main.approve)}
            </Button>
          </div>

          {confirmError && (
            <p className="text-sm text-destructive text-center">{confirmError}</p>
          )}
        </CardContent>
      </Card>
    </DeviceShell>
  );
}

function DeviceShell({ children }: { children: React.ReactNode }) {
  const { t } = useT("device");
  const logout = useLogout();
  return (
    <div className="relative flex min-h-svh flex-col bg-background">
      <DragStrip />
      <Button
        variant="ghost"
        size="sm"
        className="absolute top-16 right-12 text-muted-foreground hover:text-destructive"
        onClick={logout}
      >
        <LogOut />
        {t(($) => $.header.log_out)}
      </Button>
      <div className="flex flex-1 flex-col items-center justify-center px-6 pb-12">
        {children}
      </div>
    </div>
  );
}
