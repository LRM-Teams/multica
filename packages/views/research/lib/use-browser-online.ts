"use client";

import { useEffect, useState } from "react";
import { readBrowserOnline } from "./network-status";

/** LRM-833 — subscribe to browser online/offline for research connectivity chrome. */
export function useBrowserOnline(): boolean {
  const [online, setOnline] = useState(readBrowserOnline);

  useEffect(() => {
    const onOnline = () => setOnline(true);
    const onOffline = () => setOnline(false);
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    setOnline(readBrowserOnline());
    return () => {
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
    };
  }, []);

  return online;
}
