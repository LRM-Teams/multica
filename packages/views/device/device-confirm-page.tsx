"use client";

import { useReducer, type FormEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, type DevicePending } from "@multica/core/api";
import { useLogout } from "../auth";
import { DragStrip } from "../platform";
import { Time } from "../i18n/time";
import { useT } from "../i18n";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { LogOut, MonitorSmartphone, ShieldAlert } from "lucide-react";

export interface DeviceConfirmPageProps {
  /**
   * `user_code` from `verification_uri_complete` (`?user_code=XXXX-XXXX`).
   * Null when the user opened the bare `verification_uri` and must type
   * the code (RFC 8628 §3.3).
   */
  userCode: string | null;
}

type ConfirmOutcome = "approved" | "denied";

type DeviceConfirmState = {
  typedCode: string;
  submittedCode: string | null;
  lookupAttempt: number;
  matchesDevice: boolean;
  outcome: ConfirmOutcome | null;
  confirming: ConfirmOutcome | null;
  confirmError: string | null;
  raceExpired: boolean;
};

type DeviceConfirmAction =
  | { type: "type"; value: string }
  | { type: "submit" }
  | { type: "match"; value: boolean }
  | { type: "confirming"; value: ConfirmOutcome }
  | { type: "confirmed"; value: ConfirmOutcome }
  | { type: "confirm-error"; value: string }
  | { type: "race-expired" };

const initialState: DeviceConfirmState = {
  typedCode: "",
  submittedCode: null,
  lookupAttempt: 0,
  matchesDevice: false,
  outcome: null,
  confirming: null,
  confirmError: null,
  raceExpired: false,
};

function reduceDeviceConfirm(
  state: DeviceConfirmState,
  action: DeviceConfirmAction,
): DeviceConfirmState {
  switch (action.type) {
    case "type":
      return { ...state, typedCode: action.value, submittedCode: null };
    case "submit": {
      const next = state.typedCode.trim();
      if (!next) return state;
      return {
        ...state,
        submittedCode: next,
        lookupAttempt: state.lookupAttempt + 1,
      };
    }
    case "match":
      return { ...state, matchesDevice: action.value };
    case "confirming":
      return { ...state, confirming: action.value, confirmError: null };
    case "confirmed":
      return { ...state, outcome: action.value, confirming: null };
    case "confirm-error":
      return { ...state, confirmError: action.value, confirming: null };
    case "race-expired":
      return { ...state, raceExpired: true, confirming: null };
  }
}

function formatUserCode(raw: string): string {
  const cleaned = raw.toUpperCase().replace(/[^A-Z2-9]/g, "");
  if (cleaned.length === 8) return `${cleaned.slice(0, 4)}-${cleaned.slice(4)}`;
  return raw.trim();
}

/**
 * `/device` — RFC 8628 device-code confirmation. Two start states:
 * type-in at `verification_uri`, or display-and-confirm-match when the
 * user arrived via `verification_uri_complete`.
 */
export function DeviceConfirmPage({ userCode }: DeviceConfirmPageProps) {
  const { t } = useT("device");
  const fromCompleteURI = !!userCode;
  const [state, dispatch] = useReducer(reduceDeviceConfirm, initialState);
  const lookupCode = userCode ?? state.submittedCode;

  const {
    data: pending,
    isLoading,
    error: fetchError,
  } = useQuery<DevicePending>({
    queryKey: ["device-pending", lookupCode, state.lookupAttempt],
    queryFn: () => api.getDevicePending(lookupCode as string),
    enabled: !!lookupCode,
    retry: false,
  });

  const lookupNotFound =
    !!lookupCode &&
    (state.raceExpired ||
      (fetchError instanceof ApiError && fetchError.status === 404));

  const handleEnter = (e: FormEvent) => {
    e.preventDefault();
    dispatch({ type: "submit" });
  };

  const handleConfirm = async (approve: boolean) => {
    if (!lookupCode) return;
    const kind: ConfirmOutcome = approve ? "approved" : "denied";
    dispatch({ type: "confirming", value: kind });
    try {
      const res = await api.confirmDevice(lookupCode, approve);
      dispatch({ type: "confirmed", value: res.status });
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        dispatch({ type: "race-expired" });
      } else {
        dispatch({
          type: "confirm-error",
          value: e instanceof Error ? e.message : t(($) => $.errors.confirm_failed),
        });
      }
    }
  };

  if (state.outcome) {
    return (
      <DeviceShell>
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-4 py-12 text-center">
            <div
              className={
                state.outcome === "approved"
                  ? "flex h-12 w-12 items-center justify-center rounded-full bg-primary/10"
                  : "flex h-12 w-12 items-center justify-center rounded-full bg-muted"
              }
            >
              <MonitorSmartphone
                className={
                  state.outcome === "approved"
                    ? "h-6 w-6 text-primary"
                    : "h-6 w-6 text-muted-foreground"
                }
              />
            </div>
            <h2 className="text-lg font-semibold">
              {state.outcome === "approved"
                ? t(($) => $.done.approved_title)
                : t(($) => $.done.denied_title)}
            </h2>
            <p className="text-sm text-muted-foreground">
              {state.outcome === "approved"
                ? t(($) => $.done.approved_description)
                : t(($) => $.done.denied_description)}
            </p>
          </CardContent>
        </Card>
      </DeviceShell>
    );
  }

  if (!lookupCode) {
    return (
      <DeviceShell>
        <EnterCodeCard
          typedCode={state.typedCode}
          onTypedCodeChange={(value) => dispatch({ type: "type", value })}
          onSubmit={handleEnter}
          error={null}
          submitting={false}
        />
      </DeviceShell>
    );
  }

  if (isLoading) {
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

  if (lookupNotFound || !pending) {
    if (!fromCompleteURI) {
      return (
        <DeviceShell>
          <EnterCodeCard
            typedCode={state.typedCode}
            onTypedCodeChange={(value) => dispatch({ type: "type", value })}
            onSubmit={handleEnter}
            error={t(($) => $.enter.invalid)}
            submitting={false}
          />
        </DeviceShell>
      );
    }
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

  const actionsDisabled =
    state.confirming !== null || (fromCompleteURI && !state.matchesDevice);

  return (
    <DeviceShell>
      <Card className="w-full max-w-md">
        <CardContent className="flex flex-col items-center gap-6 py-12">
          <div className="flex h-14 w-14 items-center justify-center rounded-full bg-primary/10">
            <MonitorSmartphone className="h-7 w-7 text-primary" />
          </div>

          <div className="text-center space-y-2">
            <h2 className="text-xl font-semibold">
              {fromCompleteURI ? t(($) => $.match.title) : t(($) => $.main.title)}
            </h2>
            <p className="text-sm text-muted-foreground">
              {pending.client_hint
                ? t(($) => $.main.requested_from, { client_hint: pending.client_hint })
                : t(($) => $.main.requested_from_unknown)}
            </p>
            <p className="text-sm text-muted-foreground">
              <Time kind="relative" value={pending.created_at} />
            </p>
          </div>

          {fromCompleteURI && (
            <div className="w-full space-y-3">
              <div className="rounded-lg border border-border bg-muted/40 px-4 py-3 text-center">
                <p className="text-xs text-muted-foreground">{t(($) => $.match.code_label)}</p>
                <p className="mt-1 font-mono text-2xl tracking-[0.2em]">
                  {formatUserCode(lookupCode)}
                </p>
              </div>
              <p className="text-sm text-muted-foreground text-center">
                {t(($) => $.match.instruction)}
              </p>
              <label className="flex items-start gap-3 text-sm">
                <Checkbox
                  checked={state.matchesDevice}
                  onCheckedChange={(next) =>
                    dispatch({ type: "match", value: next === true })
                  }
                  aria-label={t(($) => $.match.checkbox)}
                />
                <span>{t(($) => $.match.checkbox)}</span>
              </label>
            </div>
          )}

          <div className="flex gap-3 w-full">
            <Button
              variant="outline"
              className="flex-1"
              onClick={() => handleConfirm(false)}
              disabled={actionsDisabled}
            >
              {state.confirming === "denied"
                ? t(($) => $.main.denying)
                : t(($) => $.main.deny)}
            </Button>
            <Button
              className="flex-1"
              onClick={() => handleConfirm(true)}
              disabled={actionsDisabled}
            >
              {state.confirming === "approved"
                ? t(($) => $.main.approving)
                : t(($) => $.main.approve)}
            </Button>
          </div>

          {state.confirmError && (
            <p className="text-sm text-destructive text-center">{state.confirmError}</p>
          )}
        </CardContent>
      </Card>
    </DeviceShell>
  );
}

function EnterCodeCard({
  typedCode,
  onTypedCodeChange,
  onSubmit,
  error,
  submitting,
}: {
  typedCode: string;
  onTypedCodeChange: (value: string) => void;
  onSubmit: (e: FormEvent) => void;
  error: string | null;
  submitting: boolean;
}) {
  const { t } = useT("device");
  return (
    <Card className="w-full max-w-md">
      <CardContent className="flex flex-col gap-6 py-12">
        <div className="flex flex-col items-center gap-3 text-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-full bg-primary/10">
            <MonitorSmartphone className="h-7 w-7 text-primary" />
          </div>
          <h2 className="text-xl font-semibold">{t(($) => $.enter.title)}</h2>
          <p className="text-sm text-muted-foreground">{t(($) => $.enter.description)}</p>
        </div>
        <form className="space-y-4" onSubmit={onSubmit}>
          <div className="space-y-2">
            <Label htmlFor="device-user-code">{t(($) => $.enter.label)}</Label>
            <Input
              id="device-user-code"
              name="user_code"
              autoComplete="one-time-code"
              value={typedCode}
              onChange={(e) => onTypedCodeChange(e.target.value)}
              placeholder={t(($) => $.enter.placeholder)}
              aria-invalid={error ? true : undefined}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <Button type="submit" className="w-full" disabled={submitting || !typedCode.trim()}>
            {submitting ? t(($) => $.enter.submitting) : t(($) => $.enter.submit)}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

function DeviceShell({ children }: { children: ReactNode }) {
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
