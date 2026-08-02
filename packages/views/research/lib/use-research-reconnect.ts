"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../../i18n/use-t";
import type { ReconnectPhase } from "./network-status";

/**
 * LRM-833 — when the browser returns online after an offline stretch, refetch
 * research data and toast success; on failure leave a manual retry phase.
 */
export function useResearchReconnect({
  online,
  reconnect,
}: {
  online: boolean;
  reconnect: () => Promise<unknown>;
}): {
  phase: ReconnectPhase;
  retry: () => void;
} {
  const { t } = useT("research");
  const [phase, setPhase] = useState<ReconnectPhase>("idle");
  const prevOnlineRef = useRef<boolean | null>(null);
  const reconnectRef = useRef(reconnect);
  reconnectRef.current = reconnect;
  const tRef = useRef(t);
  tRef.current = t;

  const runReconnect = useCallback(async () => {
    setPhase("reconnecting");
    try {
      await reconnectRef.current();
      setPhase("idle");
      toast.success(tRef.current(($) => $.connectivity.reconnected));
    } catch {
      setPhase("failed");
      showErrorToast(tRef.current(($) => $.connectivity.reconnect_failed));
    }
  }, []);

  useEffect(() => {
    const prev = prevOnlineRef.current;
    prevOnlineRef.current = online;
    if (prev === null) return;
    if (prev === true && !online) {
      setPhase("idle");
      return;
    }
    if (prev === false && online) {
      void runReconnect();
    }
  }, [online, runReconnect]);

  const retry = useCallback(() => {
    if (!online) return;
    void runReconnect();
  }, [online, runReconnect]);

  return { phase, retry };
}
