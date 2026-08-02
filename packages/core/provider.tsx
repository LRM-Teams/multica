"use client";

import { useEffect, useState } from "react";
import { onlineManager, QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "./query-client";
import { installQueryOnlineRecovery } from "./query-online";
import type { ReactNode } from "react";

export function QueryProvider({ children }: { children: ReactNode }) {
  const [queryClient] = useState(createQueryClient);

  // LRM-844: belt-and-suspenders after mount — createQueryClient already
  // installs the listener; re-assert online once the provider is live so a
  // spurious pre-mount offline cannot strand the first paint.
  useEffect(() => {
    installQueryOnlineRecovery();
    onlineManager.setOnline(true);
  }, []);

  return (
    <QueryClientProvider client={queryClient}>
      {children}
    </QueryClientProvider>
  );
}
