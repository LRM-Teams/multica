"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuthStore } from "@multica/core/auth";
import { paths } from "@multica/core/paths";
import { DeviceConfirmPage } from "@multica/views/device";

function DeviceConfirmPageContent() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const searchParams = useSearchParams();
  const userCode = searchParams.get("user_code");

  // Requires login (spec: GET /api/device/pending needs auth) — bounce
  // through /login and back with ?next= so the code survives the round trip.
  useEffect(() => {
    if (!isLoading && !user) {
      const next = userCode
        ? `${paths.device()}?user_code=${encodeURIComponent(userCode)}`
        : paths.device();
      // react-doctor-disable-next-line react-doctor/nextjs-no-client-side-redirect -- gated on the async useAuthStore subscription resolving (isLoading), not a user event; same established pattern as invite/[id]/page.tsx and login-page.tsx's cli-callback bounce
      router.replace(`${paths.login()}?next=${encodeURIComponent(next)}`);
    }
  }, [isLoading, user, router, userCode]);

  if (isLoading || !user) return null;

  return <DeviceConfirmPage userCode={userCode} />;
}

export default function Page() {
  return (
    <Suspense fallback={null}>
      <DeviceConfirmPageContent />
    </Suspense>
  );
}
