import { createStore } from "zustand/vanilla";
import { useStore } from "zustand";

export type ServiceEnvironment = "production" | "test";

interface ConfigState {
  cdnDomain: string;
  environment: ServiceEnvironment;
  allowSignup: boolean;
  googleClientId: string;
  daemonServerUrl: string;
  daemonAppUrl: string;
  agentProfileDevAccessEnabled: boolean;
  // Self-host gate (#3433): when true, every "Create workspace" affordance
  // must be hidden. Defaults to false so unknown / older servers behave like
  // the managed-cloud case.
  workspaceCreationDisabled: boolean;
  setCdnDomain: (domain: string) => void;
  setAuthConfig: (config: {
    allowSignup: boolean;
    googleClientId?: string;
    workspaceCreationDisabled?: boolean;
  }) => void;
  setDaemonConfig: (config: {
    environment?: ServiceEnvironment;
    daemonServerUrl?: string;
    daemonAppUrl?: string;
  }) => void;
  setAgentProfileConfig: (config: { devAccessEnabled?: boolean }) => void;
}

export const configStore = createStore<ConfigState>((set) => ({
  cdnDomain: "",
  environment: "production",
  allowSignup: true,
  googleClientId: "",
  daemonServerUrl: "",
  daemonAppUrl: "",
  agentProfileDevAccessEnabled: false,
  workspaceCreationDisabled: false,
  setCdnDomain: (domain) => set({ cdnDomain: domain }),
  setAuthConfig: ({ allowSignup, googleClientId = "", workspaceCreationDisabled = false }) =>
    set({ allowSignup, googleClientId, workspaceCreationDisabled }),
  setDaemonConfig: ({
    environment = "production",
    daemonServerUrl = "",
    daemonAppUrl = "",
  }) => set({ environment, daemonServerUrl, daemonAppUrl }),
  setAgentProfileConfig: ({ devAccessEnabled = false }) =>
    set({ agentProfileDevAccessEnabled: devAccessEnabled }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}
