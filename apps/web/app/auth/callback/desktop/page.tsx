"use client";

import { Suspense, useEffect, useReducer } from "react";
import { useSearchParams } from "next/navigation";
import { paths } from "@multica/core/paths";
import { api } from "@multica/core/api";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";

type DesktopState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "handoff"; token: string };

// Desktop OAuth handoff (#223 Phase 2). The web callback Route Handler bounces
// `platform:desktop` here so the token exchange + `multica://` deep-link stay on
// the client — the token never touches a server Location/log. `redirect_uri`
// MUST remain the public `/auth/callback` the code was issued for (RFC 6749),
// NOT this `/auth/callback/desktop` page.
function DesktopCallbackContent() {
  const searchParams = useSearchParams();
  // One reducer for the whole handoff state — a single dispatch per outcome
  // keeps the effect to one state update on each path.
  const [state, dispatch] = useReducer(
    (_prev: DesktopState, next: DesktopState) => next,
    { status: "loading" },
  );

  useEffect(() => {
    const code = searchParams.get("code");
    if (!code) {
      dispatch({ status: "error", message: "Missing authorization code" });
      return;
    }
    // redirect_uri MUST stay the public /auth/callback the code was issued for.
    const redirectUri = `${window.location.origin}/auth/callback`;
    // Exchange for the token only (no web session) and hand off via deep-link.
    api
      .googleLogin(code, redirectUri)
      .then(({ token }) => {
        dispatch({ status: "handoff", token });
        window.location.href = `multica://auth/callback?token=${encodeURIComponent(token)}`;
      })
      .catch((err) => {
        dispatch({ status: "error", message: err instanceof Error ? err.message : "Login failed" });
      });
  }, [searchParams]);

  if (state.status === "handoff") {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl">Opening Multica</CardTitle>
            <CardDescription>
              You should see a prompt to open the Multica desktop app. If nothing
              happens, click the button below.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            <Button
              variant="outline"
              onClick={() => {
                window.location.href = `multica://auth/callback?token=${encodeURIComponent(state.token)}`;
              }}
            >
              Open Multica Desktop
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (state.status === "error") {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl">Login Failed</CardTitle>
            <CardDescription>{state.message}</CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            <a href={paths.login()} className="text-primary underline-offset-4 hover:underline">
              Back to login
            </a>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">Signing in...</CardTitle>
          <CardDescription>Please wait while we complete your login</CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}

export default function DesktopCallbackPage() {
  return (
    <Suspense fallback={null}>
      <DesktopCallbackContent />
    </Suspense>
  );
}
