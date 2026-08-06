"use client";

import { useEffect, useRef } from "react";
import { getApi } from "../api";
import { useAuthStore } from "../auth";

const PRESENCE_INTERVAL_MS = 5 * 60 * 1000;

/** Awards honor presence XP while the user is authenticated and the tab is visible. */
export function HonorPresenceHeartbeat() {
  const userId = useAuthStore((s) => s.user?.id);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    if (!userId) return;

    const api = getApi();
    let cancelled = false;

    const ping = () => {
      if (cancelled || document.visibilityState !== "visible") return;
      void api.postHonorPresence().catch(() => {
        /* honor must never block the app */
      });
    };

    ping();
    timerRef.current = setInterval(ping, PRESENCE_INTERVAL_MS);

    const onVisibility = () => {
      if (document.visibilityState === "visible") ping();
    };
    document.addEventListener("visibilitychange", onVisibility);

    return () => {
      cancelled = true;
      if (timerRef.current) clearInterval(timerRef.current);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [userId]);

  return null;
}
