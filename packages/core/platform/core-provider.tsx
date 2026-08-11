"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiClient } from "../api/client";
import { setApiInstance, setSchemaLogger } from "../api";
import { createAuthStore, registerAuthStore } from "../auth";
import { createChatStore, registerChatStore } from "../chat";
import {
  I18nProvider,
  LocaleAdapterProvider,
  UserLocaleSync,
} from "../i18n/react";
import { WSProvider } from "../realtime";
import { QueryProvider } from "../provider";
import { createLogger } from "../logger";
import { defaultStorage } from "./storage";
import { AuthInitializer } from "./auth-initializer";
import { HonorPresenceHeartbeat } from "./honor-presence-heartbeat";
import type { CoreProviderProps, ClientIdentity } from "./types";
import type { StorageAdapter } from "../types/storage";
import { configStore, type ServiceEnvironment } from "../config";
import { testComputerReleaseOptions } from "../releases/computer-metainfo";

// Module-level singletons — created once at first render, never recreated.
// Vite HMR preserves module-level state, so these survive hot reloads.
let initialized = false;
let authStore: ReturnType<typeof createAuthStore>;
let chatStore: ReturnType<typeof createChatStore>;

function ComputerReleasePrefetch({
  environment,
}: {
  environment: ServiceEnvironment;
}) {
  useQuery(testComputerReleaseOptions(environment === "test"));
  return null;
}

function initCore(
  apiBaseUrl: string,
  appUrl: string,
  environment: ServiceEnvironment,
  computerVersion: string,
  storage: StorageAdapter,
  onLogin?: () => void,
  onLogout?: () => void,
  cookieAuth?: boolean,
  identity?: ClientIdentity,
) {
  if (initialized) return;

  configStore.setState({
    environment,
    computerVersion: computerVersion.trim(),
    ...(appUrl.trim()
      ? { daemonAppUrl: appUrl.trim().replace(/\/+$/, "") }
      : {}),
  });

  const api = new ApiClient(apiBaseUrl, {
    logger: createLogger("api"),
    onUnauthorized: () => {
      storage.removeItem("multica_token");
    },
    identity,
  });
  setApiInstance(api);
  setSchemaLogger(createLogger("api-schema"));

  // In token mode, hydrate token from storage.
  if (!cookieAuth) {
    const token = storage.getItem("multica_token");
    if (token) api.setToken(token);
  }
  // Workspace identity is URL-driven: the [workspaceSlug] layout resolves
  // the slug and calls setCurrentWorkspace(slug, wsId) on mount. The api
  // client reads the slug from that singleton for the X-Workspace-Slug
  // header. No boot-time hydration from storage is required.

  authStore = createAuthStore({ api, storage, onLogin, onLogout, cookieAuth });
  registerAuthStore(authStore);

  chatStore = createChatStore({ storage });
  registerChatStore(chatStore);

  initialized = true;
}

export function CoreProvider({
  children,
  apiBaseUrl = "",
  appUrl = "",
  environment = "production",
  computerVersion = "",
  wsUrl = "ws://localhost:8080/ws",
  storage = defaultStorage,
  cookieAuth,
  onLogin,
  onLogout,
  identity,
  locale,
  resources,
  localeAdapter,
}: CoreProviderProps) {
  // Initialize singletons on first render only. initCore's guard makes later
  // calls no-ops if a host recreates one of these boot-time values.
  useMemo(
    () =>
      initCore(
        apiBaseUrl,
        appUrl,
        environment,
        computerVersion,
        storage,
        onLogin,
        onLogout,
        cookieAuth,
        identity,
      ),
    [
      apiBaseUrl,
      appUrl,
      environment,
      computerVersion,
      storage,
      onLogin,
      onLogout,
      cookieAuth,
      identity,
    ],
  );

  // I18nProvider wraps everything else: server and client must use the same
  // (locale, resources) to avoid hydration mismatch. Language switching goes
  // through window.location.reload(), never client-side changeLanguage.
  const tree = (
    <QueryProvider>
      <ComputerReleasePrefetch environment={environment} />
      <AuthInitializer
        onLogin={onLogin}
        onLogout={onLogout}
        storage={storage}
        cookieAuth={cookieAuth}
        identity={identity}
      >
        <WSProvider
          wsUrl={wsUrl}
          authStore={authStore}
          storage={storage}
          cookieAuth={cookieAuth}
          identity={identity}
        >
          <HonorPresenceHeartbeat />
          {children}
        </WSProvider>
      </AuthInitializer>
    </QueryProvider>
  );

  // UserLocaleSync requires a LocaleAdapter to persist; only mount it when
  // the host app provides one (web layout + desktop App both do).
  const withAdapter = localeAdapter ? (
    <LocaleAdapterProvider adapter={localeAdapter}>
      <UserLocaleSync />
      {tree}
    </LocaleAdapterProvider>
  ) : (
    tree
  );

  return (
    <I18nProvider locale={locale} resources={resources}>
      {withAdapter}
    </I18nProvider>
  );
}
