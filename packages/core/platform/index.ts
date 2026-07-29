export { CoreProvider } from "./core-provider";
export type { CoreProviderProps, ClientIdentity } from "./types";
export { AuthInitializer } from "./auth-initializer";
export { defaultStorage } from "./storage";
export { createPersistStorage } from "./persist-storage";
export { createWorkspaceAwareStorage, setCurrentWorkspace, getCurrentSlug, getCurrentWsId, subscribeToCurrentSlug, registerForWorkspaceRehydration } from "./workspace-storage";
export { clearWorkspaceStorage } from "./storage-cleanup";
export { isMac, modKey, enterKey, formatShortcut } from "./keyboard";
export {
  buildNotificationRoute,
  registerSystemNotificationClickHandler,
  isWebNotificationSupported,
  getWebNotificationPermission,
  requestWebNotificationPermission,
  showWebNotification,
  type SystemNotificationPayload,
  type WebNotificationPermission,
} from "./system-notification";
export {
  getBrowserNotificationCapability,
  isIOSBrowser,
  isStandaloneDisplayMode,
  isPushNotificationSupported,
  type BrowserNotificationCapability,
} from "./browser-notification-capability";
export {
  shouldShowBrowserNotificationPrompt,
  dismissBrowserNotificationPrompt,
  snoozeBrowserNotificationPrompt,
  clearBrowserNotificationPromptDecision,
  readBrowserNotificationPromptDecision,
  BROWSER_NOTIFICATION_PROMPT_STORAGE_KEY,
  type BrowserNotificationPromptDecision,
} from "./browser-notification-prompt";
