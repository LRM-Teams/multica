import type { LocaleResources, SupportedLocale } from "@multica/core/i18n";
import enCommon from "./en/common.json";
import enAuth from "./en/auth.json";
import enSettings from "./en/settings.json";
import enIssues from "./en/issues.json";
import enAgents from "./en/agents.json";
import enEditor from "./en/editor.json";
import enOnboarding from "./en/onboarding.json";
import enInvite from "./en/invite.json";
import enDevice from "./en/device.json";
import enLabels from "./en/labels.json";
import enMembers from "./en/members.json";
import enMyIssues from "./en/my-issues.json";
import enSearch from "./en/search.json";
import enInbox from "./en/inbox.json";
import enWorkspace from "./en/workspace.json";
import enProjects from "./en/projects.json";
import enSkills from "./en/skills.json";
import enChat from "./en/chat.json";
import enModals from "./en/modals.json";
import enRuntimes from "./en/runtimes.json";
import enLayout from "./en/layout.json";
import enOverview from "./en/overview.json";
import enUsage from "./en/usage.json";
import enUi from "./en/ui.json";
import enBilling from "./en/billing.json";
import enChannels from "./en/channels.json";
import enEvolution from "./en/evolution.json";
import enKnowledge from "./en/knowledge.json";
import enResearch from "./en/research.json";
import enProblemEvolution from "./en/problem-evolution.json";
import enPlanBilling from "./en/plan-billing.json";
import zhHansCommon from "./zh-Hans/common.json";
import zhHansAuth from "./zh-Hans/auth.json";
import zhHansSettings from "./zh-Hans/settings.json";
import zhHansIssues from "./zh-Hans/issues.json";
import zhHansAgents from "./zh-Hans/agents.json";
import zhHansEditor from "./zh-Hans/editor.json";
import zhHansOnboarding from "./zh-Hans/onboarding.json";
import zhHansInvite from "./zh-Hans/invite.json";
import zhHansDevice from "./zh-Hans/device.json";
import zhHansLabels from "./zh-Hans/labels.json";
import zhHansMembers from "./zh-Hans/members.json";
import zhHansMyIssues from "./zh-Hans/my-issues.json";
import zhHansSearch from "./zh-Hans/search.json";
import zhHansInbox from "./zh-Hans/inbox.json";
import zhHansWorkspace from "./zh-Hans/workspace.json";
import zhHansProjects from "./zh-Hans/projects.json";
import zhHansSkills from "./zh-Hans/skills.json";
import zhHansChat from "./zh-Hans/chat.json";
import zhHansModals from "./zh-Hans/modals.json";
import zhHansRuntimes from "./zh-Hans/runtimes.json";
import zhHansLayout from "./zh-Hans/layout.json";
import zhHansOverview from "./zh-Hans/overview.json";
import zhHansUsage from "./zh-Hans/usage.json";
import zhHansUi from "./zh-Hans/ui.json";
import zhHansBilling from "./zh-Hans/billing.json";
import zhHansChannels from "./zh-Hans/channels.json";
import zhHansEvolution from "./zh-Hans/evolution.json";
import zhHansKnowledge from "./zh-Hans/knowledge.json";
import zhHansResearch from "./zh-Hans/research.json";
import zhHansProblemEvolution from "./zh-Hans/problem-evolution.json";
import zhHansPlanBilling from "./zh-Hans/plan-billing.json";

// Single source of truth for the resource bundle. Both apps (web layout +
// desktop App.tsx) import from here so adding a locale or namespace happens
// in exactly one place.
export const RESOURCES: Record<SupportedLocale, LocaleResources> = {
  en: {
    common: enCommon,
    auth: enAuth,
    settings: enSettings,
    issues: enIssues,
    agents: enAgents,
    editor: enEditor,
    onboarding: enOnboarding,
    invite: enInvite,
    device: enDevice,
    labels: enLabels,
    members: enMembers,
    "my-issues": enMyIssues,
    search: enSearch,
    inbox: enInbox,
    workspace: enWorkspace,
    projects: enProjects,
    skills: enSkills,
    chat: enChat,
    modals: enModals,
    runtimes: enRuntimes,
    layout: enLayout,
    "overview": enOverview,
    usage: enUsage,
    ui: enUi,
    billing: enBilling,
    channels: enChannels,
    evolution: enEvolution,
    knowledge: enKnowledge,
    research: enResearch,
    "problem-evolution": enProblemEvolution,
    "plan-billing": enPlanBilling,
  },
  "zh-Hans": {
    common: zhHansCommon,
    auth: zhHansAuth,
    settings: zhHansSettings,
    issues: zhHansIssues,
    agents: zhHansAgents,
    editor: zhHansEditor,
    onboarding: zhHansOnboarding,
    invite: zhHansInvite,
    device: zhHansDevice,
    labels: zhHansLabels,
    members: zhHansMembers,
    "my-issues": zhHansMyIssues,
    search: zhHansSearch,
    inbox: zhHansInbox,
    workspace: zhHansWorkspace,
    projects: zhHansProjects,
    skills: zhHansSkills,
    chat: zhHansChat,
    modals: zhHansModals,
    runtimes: zhHansRuntimes,
    layout: zhHansLayout,
    "overview": zhHansOverview,
    usage: zhHansUsage,
    ui: zhHansUi,
    billing: zhHansBilling,
    channels: zhHansChannels,
    evolution: zhHansEvolution,
    knowledge: zhHansKnowledge,
    research: zhHansResearch,
    "problem-evolution": zhHansProblemEvolution,
    "plan-billing": zhHansPlanBilling,
  },
};
