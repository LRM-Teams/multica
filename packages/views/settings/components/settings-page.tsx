"use client";

import React, { useEffect } from "react";
import {
  User,
  SlidersHorizontal,
  Key,
  Settings,
  Users,
  FolderGit2,
  FlaskConical,
  Bell,
  Plug,
  Medal,
  Box,
} from "lucide-react";
import { GitHubMark } from "./github-mark";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "../../navigation";
import { AccountTab } from "./account-tab";
import { HonorTab } from "./honor-tab";
import { PreferencesTab } from "./preferences-tab";
import { TokensTab } from "./tokens-tab";
import { WorkspaceTab } from "./workspace-tab";
import { MembersTab } from "./members-tab";
import { RepositoriesTab } from "./repositories-tab";
import { GitHubTab } from "./github-tab";
import { IntegrationsTab } from "./integrations-tab";
import { LabsTab } from "./labs-tab";
import { NotificationsTab } from "./notifications-tab";
import { SandboxesPage } from "../../sandboxes";
import { useT } from "../../i18n";

const ACCOUNT_TAB_KEYS = [
  "profile",
  "honor",
  "preferences",
  "notifications",
  "tokens",
  "sandboxes",
] as const;
const ACCOUNT_TAB_ICONS = {
  profile: User,
  honor: Medal,
  preferences: SlidersHorizontal,
  notifications: Bell,
  tokens: Key,
  sandboxes: Box,
} as const;

const WORKSPACE_TAB_KEYS = [
  "general",
  "repositories",
  "github",
  "integrations",
  "labs",
  "members",
] as const;
const WORKSPACE_TAB_VALUES = {
  general: "workspace",
  repositories: "repositories",
  github: "github",
  integrations: "integrations",
  labs: "labs",
  members: "members",
} as const;
const WORKSPACE_TAB_ICONS = {
  general: Settings,
  repositories: FolderGit2,
  github: GitHubMark,
  integrations: Plug,
  labs: FlaskConical,
  members: Users,
} as const;

const DEFAULT_TAB = "profile";
const TAB_QUERY_KEY = "tab";

// Legacy `?tab=…` values that have been collapsed into another tab. Old
// bookmarks still land on the correct surface without us preserving a
// dead TabsContent entry. Lark used to be its own top-level workspace
// tab; it now lives inside Integrations.
const LEGACY_WORKSPACE_TAB_REDIRECTS: Record<string, string> = {
  lark: "integrations",
};

// Evolution review moved from Settings to Skills; old bookmarks still use these tab values.
const EVOLUTION_REVIEW_LEGACY_TABS = new Set(["evolution-review", "evolution_review"]);

export interface ExtraSettingsTab {
  value: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  content: React.ReactNode;
}

interface SettingsPageProps {
  /** Additional tabs injected by platform (e.g. desktop daemon settings) */
  extraAccountTabs?: ExtraSettingsTab[];
}

export function SettingsPage({ extraAccountTabs }: SettingsPageProps = {}) {
  const { t } = useT("settings");
  const workspaceName = useCurrentWorkspace()?.name;
  const navigation = useNavigation();
  const paths = useWorkspacePaths();

  // Whitelist of valid tab values; unknown ?tab=… values silently fall back to
  // the default. Whitelisting also blocks junk like ?tab=<script> from
  // surfacing in the DOM via Radix Tabs internals.
  const validTabs = React.useMemo(
    () =>
      new Set<string>([
        ...ACCOUNT_TAB_KEYS,
        ...Object.values(WORKSPACE_TAB_VALUES),
        ...(extraAccountTabs?.map((tab) => tab.value) ?? []),
      ]),
    [extraAccountTabs],
  );

  const tabFromUrl = navigation.searchParams.get(TAB_QUERY_KEY);

  useEffect(() => {
    if (tabFromUrl && EVOLUTION_REVIEW_LEGACY_TABS.has(tabFromUrl)) {
      navigation.replace(`${paths.skills()}?section=review`);
    }
  }, [navigation, paths, tabFromUrl]);

  const candidateTab = tabFromUrl
    ? LEGACY_WORKSPACE_TAB_REDIRECTS[tabFromUrl] ?? tabFromUrl
    : null;
  const activeTab =
    candidateTab && validTabs.has(candidateTab) ? candidateTab : DEFAULT_TAB;

  // replace (not push) so settings tab switches don't pollute browser history.
  // Preserve any other query params the page may carry.
  const handleTabChange = (next: string) => {
    const params = new URLSearchParams(navigation.searchParams);
    params.set(TAB_QUERY_KEY, next);
    navigation.replace(`${navigation.pathname}?${params.toString()}`);
  };

  return (
    <Tabs
      value={activeTab}
      onValueChange={handleTabChange}
      orientation="vertical"
      className="flex-1 min-h-0 gap-0 flex flex-col md:flex-row md:overflow-hidden overflow-y-auto"
    >
      {/* Left nav (stacks on top on mobile, sidebar on md+) */}
      <div className="shrink-0 border-b border-border p-3 md:w-52 md:overflow-y-auto md:border-b-0 md:border-r md:p-4">
        <h1 className="mb-4 px-2 text-base font-bold text-ink">{t(($) => $.page.title)}</h1>
        <TabsList variant="line" className="w-full flex-col items-stretch">
          {/* My Account group */}
          <span className="px-2 pb-1 pt-2 text-xs font-bold text-ink-2">
            {t(($) => $.page.my_account)}
          </span>
          {ACCOUNT_TAB_KEYS.map((key) => {
            const Icon = ACCOUNT_TAB_ICONS[key];
            return (
              <TabsTrigger key={key} value={key} className="text-xs font-bold">
                <Icon className="h-4 w-4" />
                {t(($) => $.page.tabs[key])}
              </TabsTrigger>
            );
          })}
          {extraAccountTabs?.map((tab) => (
            <TabsTrigger key={tab.value} value={tab.value} className="text-xs font-bold">
              <tab.icon className="h-4 w-4" />
              {tab.label}
            </TabsTrigger>
          ))}

          {/* Workspace group */}
          <span className="truncate px-2 pb-1 pt-4 text-xs font-bold text-ink-2">
            {workspaceName ?? t(($) => $.page.workspace_fallback)}
          </span>
          {WORKSPACE_TAB_KEYS.map((key) => {
            const Icon = WORKSPACE_TAB_ICONS[key];
            return (
              <TabsTrigger
                key={key}
                value={WORKSPACE_TAB_VALUES[key]}
                className="text-xs font-bold"
              >
                <Icon className="h-4 w-4" />
                {t(($) => $.page.tabs[key])}
              </TabsTrigger>
            );
          })}
        </TabsList>
      </div>

      {/* Right content */}
      <div className="flex-1 min-w-0 md:overflow-y-auto">
        <div
          className={cn(
            "mx-auto w-full p-4 md:p-6",
            activeTab === "honor" || activeTab === "sandboxes"
              ? "max-w-none xl:px-8 2xl:px-10"
              : "max-w-3xl",
            activeTab === "sandboxes" && "flex min-h-[min(70vh,720px)] flex-col p-0 md:p-0",
          )}
        >
          <TabsContent value="profile"><AccountTab /></TabsContent>
          <TabsContent value="honor"><HonorTab /></TabsContent>
          <TabsContent value="preferences"><PreferencesTab /></TabsContent>
          <TabsContent value="notifications"><NotificationsTab /></TabsContent>
          <TabsContent value="tokens"><TokensTab /></TabsContent>
          <TabsContent value="sandboxes" className="mt-0 flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <SandboxesPage />
          </TabsContent>
          <TabsContent value="workspace"><WorkspaceTab /></TabsContent>
          <TabsContent value="repositories"><RepositoriesTab /></TabsContent>
          <TabsContent value="github"><GitHubTab /></TabsContent>
          <TabsContent value="integrations"><IntegrationsTab /></TabsContent>
          <TabsContent value="labs"><LabsTab /></TabsContent>
          <TabsContent value="members"><MembersTab /></TabsContent>
          {extraAccountTabs?.map((tab) => (
            <TabsContent key={tab.value} value={tab.value}>{tab.content}</TabsContent>
          ))}
        </div>
      </div>
    </Tabs>
  );
}
