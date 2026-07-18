"use client";

import { createContext, use, useMemo, useTransition } from "react";
import type { NavigationAdapter } from "./types";

const NavigationContext = createContext<NavigationAdapter | null>(null);
const NavigationPendingContext = createContext<boolean>(false);

export function NavigationProvider({
  value,
  children,
}: {
  value: NavigationAdapter;
  children: React.ReactNode;
}) {
  // Wrap push/replace in startTransition so any caller of useNavigation()
  // (sidebar AppLink, command palette, modal post-create jumps) gets a
  // React pending signal during route commit. On web this stays true until
  // Next.js commits the new RSC payload; on desktop it flips off quickly
  // because react-router commits synchronously — both are correct.
  const [isPending, startTransition] = useTransition();
  const wrapped = useMemo<NavigationAdapter>(
    () => ({
      ...value,
      push: (path: string) => startTransition(() => value.push(path)),
      replace: (path: string) => startTransition(() => value.replace(path)),
    }),
    [value],
  );
  return (
    <NavigationContext.Provider value={wrapped}>
      <NavigationPendingContext.Provider value={isPending}>
        {children}
      </NavigationPendingContext.Provider>
    </NavigationContext.Provider>
  );
}

export function useNavigation(): NavigationAdapter {
  const ctx = useOptionalNavigation();
  if (!ctx)
    throw new Error("useNavigation must be used within NavigationProvider");
  return ctx;
}

/**
 * Read navigation context for display-only components that can render outside
 * an application shell. Page and action code should continue using
 * useNavigation(), which requires the provider.
 */
export function useOptionalNavigation(): NavigationAdapter | null {
  return use(NavigationContext);
}

/** True while a transition-wrapped push/replace is committing. */
export function useIsNavigating(): boolean {
  return use(NavigationPendingContext);
}
