import { create } from "zustand";
import type { User, StorageAdapter } from "../types";
import { identify as identifyAnalytics, resetAnalytics } from "../analytics";
import { ApiError, type ApiClient } from "../api/client";
import { setCurrentWorkspace } from "../platform/workspace-storage";

export interface AuthStoreOptions {
  api: ApiClient;
  storage: StorageAdapter;
  onLogin?: () => void;
  onLogout?: () => void;
  /** When true, rely on HttpOnly cookies instead of localStorage for auth tokens. */
  cookieAuth?: boolean;
}

export interface AuthState {
  user: User | null;
  isLoading: boolean;

  initialize: () => Promise<void>;
  sendCode: (email: string) => Promise<void>;
  verifyCode: (email: string, code: string) => Promise<User>;
  loginWithGoogle: (code: string, redirectUri: string) => Promise<User>;
  loginWithToken: (token: string) => Promise<User>;
  logout: () => void;
  setUser: (user: User) => void;
  refreshMe: () => Promise<void>;
}

export function createAuthStore(options: AuthStoreOptions) {
  const { api, storage, onLogin, onLogout, cookieAuth } = options;

  return create<AuthState>((set) => ({
    user: null,
    isLoading: true,

    initialize: async () => {
      if (cookieAuth) {
        // In cookie mode, the HttpOnly cookie is sent automatically.
        // Try to fetch the current user — if the cookie exists the server will accept it.
        try {
          const user = await api.getMe();
          set({ user, isLoading: false });
        } catch {
          set({ user: null, isLoading: false });
        }
        return;
      }

      // Token mode: read from localStorage (Electron / legacy).
      const token = storage.getItem("multica_token");
      if (!token) {
        set({ isLoading: false });
        return;
      }

      api.setToken(token);

      try {
        const user = await api.getMe();
        set({ user, isLoading: false });
      } catch (err) {
        // Only clear the stored token on a genuine auth failure (401). For
        // transient errors — network blips, backend rolling restarts, 5xx,
        // aborted fetches — keep the token so the next initialize() (next
        // page load or focus-refresh) can retry. The 401 path's token
        // cleanup is handled upstream by ApiClient.handleUnauthorized via
        // the onUnauthorized callback; we only need to reset the in-memory
        // user + workspace state here.
        if (err instanceof ApiError && err.status === 401) {
          setCurrentWorkspace(null, null);
        }
        set({ user: null, isLoading: false });
      }
    },

    sendCode: async (email: string) => {
      await api.sendCode(email);
    },

    verifyCode: async (email: string, code: string) => {
      const { token, user } = await api.verifyCode(email, code);
      // Keep the freshly issued token in memory so immediate post-login API
      // calls do not race cookie persistence through the browser/proxy stack.
      api.setToken(token);
      if (!cookieAuth) {
        // Token mode: persist for Electron / legacy.
        storage.setItem("multica_token", token);
      }
      onLogin?.();
      identifyAnalytics(user.id, { email: user.email, name: user.display_name || user.name });
      set({ user });
      return user;
    },

    loginWithGoogle: async (code: string, redirectUri: string) => {
      const { token, user } = await api.googleLogin(code, redirectUri);
      // Keep the freshly issued token in memory so immediate post-login API
      // calls do not race cookie persistence through the browser/proxy stack.
      api.setToken(token);
      if (!cookieAuth) {
        storage.setItem("multica_token", token);
      }
      onLogin?.();
      identifyAnalytics(user.id, { email: user.email, name: user.display_name || user.name });
      set({ user });
      return user;
    },

    loginWithToken: async (token: string) => {
      storage.setItem("multica_token", token);
      api.setToken(token);
      const user = await api.getMe();
      onLogin?.();
      identifyAnalytics(user.id, { email: user.email, name: user.display_name || user.name });
      set({ user, isLoading: false });
      return user;
    },

    logout: () => {
      // Best-effort: remove this browser/device binding before clearing auth so
      // background Push cannot keep delivering to a logged-out browser profile.
      void (async () => {
        await unbindBrowserPushSubscription(api).catch(() => undefined);
        if (cookieAuth) {
          // Clear server-side HttpOnly cookie.
          await api.logout().catch(() => undefined);
        }
        api.setToken(null);
      })();
      storage.removeItem("multica_token");
      setCurrentWorkspace(null, null);
      resetAnalytics();
      onLogout?.();
      set({ user: null });
    },

    setUser: (user: User) => {
      set({ user });
    },

    refreshMe: async () => {
      const user = await api.getMe();
      set({ user });
    },
  }));
}

async function unbindBrowserPushSubscription(api: ApiClient): Promise<void> {
  if (typeof window === "undefined" || !("serviceWorker" in window.navigator)) return;
  const registrations = await window.navigator.serviceWorker.getRegistrations();
  await Promise.all(
    registrations.map(async (registration) => {
      const subscription = await registration.pushManager.getSubscription();
      if (!subscription) return;
      await api.unbindWebPushSubscription(subscription.endpoint).catch(() => undefined);
      await subscription.unsubscribe().catch(() => false);
    }),
  );
}
