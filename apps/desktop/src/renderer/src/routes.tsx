import {
  createMemoryRouter,
  Navigate,
  useParams,
} from "react-router-dom";
import type { RouteObject } from "react-router-dom";
import { IssueDetailPage } from "./pages/issue-detail-page";
import { ProjectDetailPage } from "./pages/project-detail-page";
import { SkillDetailPage } from "./pages/skill-detail-page";
import { RuntimeDetailPage } from "./pages/runtime-detail-page";
import { AttachmentPreviewRoute } from "./pages/attachment-preview-page";
import { DesktopResearchSessionPage } from "./pages/research-session-page";
import { DesktopSettingsRoute } from "./pages/desktop-settings-route";
import { IssuesPage } from "@multica/views/issues/components";
import { ProjectsPage } from "@multica/views/projects/components";
import { DashboardPage } from "@multica/views/dashboard";
import { OverviewPage } from "@multica/views/overview";
import { PlanBillingPage } from "@multica/views/plan-billing";
import { ResearchListPage } from "@multica/views/research";
import { MyIssuesPage } from "@multica/views/my-issues";
import { SkillsPage } from "@multica/views/skills";
import { KnowledgeListPage } from "@multica/views/knowledge";
import { NotesPage } from "@multica/views/notes";
import { DesktopRuntimesPage } from "./components/desktop-runtimes-page";
import { DesktopMembersPage } from "./components/desktop-members-page";
import { SandboxesPage } from "@multica/views/sandboxes";
import { SandboxDetailPage } from "./pages/sandbox-detail-page";
import { SandboxNodeSetupPage } from "./pages/sandbox-node-setup-page";
import { WikiDetailPage } from "./pages/wiki-detail-page";
import { InboxPage } from "@multica/views/inbox";
import { WorkspaceRouteLayout } from "./components/workspace-route-layout";
import { DesktopRouteErrorPage } from "./components/route-error-page";
import { PageShell } from "./components/page-shell";

export function NotesPageRoute() {
  const { id } = useParams();
  return <NotesPage pageId={id} />;
}

function AgentsIdToMembersRedirect() {
  const { id } = useParams();
  return (
    <Navigate
      to={`../members?member=agent%3A${encodeURIComponent(id ?? "")}`}
      replace
    />
  );
}

function MembersIdToQueryRedirect() {
  const { id } = useParams();
  return (
    <Navigate
      to={`../members?member=user%3A${encodeURIComponent(id ?? "")}`}
      replace
    />
  );
}

/**
 * Route definitions shared by all tabs.
 *
 * Every tab path is workspace-scoped: `/{slug}/{route}/...`. Pre-workspace
 * flows (create workspace, accept invite) are NOT routes — they render as a
 * window-level overlay via `WindowOverlay`, dispatched by the navigation
 * adapter's transition-path interception. The `activeWorkspaceSlug` in the
 * tab store decides which workspace's tabs are visible in the TabBar;
 * workspace-less state (zero-workspace user) shows the overlay instead.
 *
 * The root index route stays as a harmless safety net. With per-workspace
 * tabs, nothing should construct a tab at `/` — but if one ever slips
 * through (malformed persisted state that dodges the migration, direct
 * router.navigate from unforeseen code), the index falls back to null
 * rather than 404; App.tsx's bootstrap repoints activeWorkspaceSlug on the
 * next render pass.
 */
// react-doctor-disable-next-line react-doctor/only-export-components -- route config module intentionally exports React Router data plus component wrappers.
export const appRoutes: RouteObject[] = [
  {
    element: <PageShell />,
    errorElement: <DesktopRouteErrorPage />,
    children: [
      { index: true, element: null },
      {
        path: ":workspaceSlug",
        element: <WorkspaceRouteLayout />,
        children: [
          { index: true, element: <Navigate to="issues" replace /> },
          {
            path: "overview",
            element: <OverviewPage />,
            handle: { title: "Overview" },
          },
          {
            path: "issues",
            element: <IssuesPage />,
            handle: { title: "Issues" },
          },
          {
            path: "issues/:id",
            element: <IssueDetailPage />,
            handle: { title: "Issue" },
          },
          {
            path: "projects",
            element: <ProjectsPage />,
            handle: { title: "Projects" },
          },
          {
            path: "projects/:id",
            element: <ProjectDetailPage />,
            handle: { title: "Project" },
          },
          {
            path: "research",
            element: <ResearchListPage />,
            handle: { title: "Research" },
          },
          {
            path: "research/:id",
            element: <DesktopResearchSessionPage />,
            handle: { title: "Research" },
          },
          {
            path: "my-issues",
            element: <MyIssuesPage />,
            handle: { title: "My Issues" },
          },
          {
            path: "runtimes",
            element: <DesktopRuntimesPage />,
            handle: { title: "Runtimes" },
          },
          {
            path: "runtimes/:id",
            element: <RuntimeDetailPage />,
            handle: { title: "Runtime" },
          },
          { path: "sandboxes", element: <SandboxesPage />, handle: { title: "Sandboxes" } },
          {
            path: "sandboxes/nodes/:nodeId",
            element: <SandboxNodeSetupPage />,
            handle: { title: "Sandbox setup" },
          },
          {
            path: "sandboxes/:instanceId",
            element: <SandboxDetailPage />,
            handle: { title: "Sandbox" },
          },
          { path: "skills", element: <SkillsPage />, handle: { title: "Skills" } },
          {
            path: "skills/:id",
            element: <SkillDetailPage />,
            handle: { title: "Skill" },
          },
          { path: "notes", element: <NotesPage />, handle: { title: "Notes" } },
          { path: "notes/:id", element: <NotesPageRoute />, handle: { title: "Notes" } },
          { path: "wiki", element: <KnowledgeListPage />, handle: { title: "Knowledge" } },
          {
            path: "wiki/:id",
            element: <WikiDetailPage />,
            handle: { title: "Knowledge" },
          },
          {
            path: "members",
            element: <DesktopMembersPage />,
            handle: { title: "Members" },
          },
          {
            path: "agents",
            element: <Navigate to="../members" replace />,
            handle: { title: "Members" },
          },
          {
            path: "agents/:id",
            element: <AgentsIdToMembersRedirect />,
            handle: { title: "Members" },
          },
          {
            path: "members/:id",
            element: <MembersIdToQueryRedirect />,
            handle: { title: "Members" },
          },
          { path: "inbox", element: <InboxPage />, handle: { title: "Activity" } },
          {
            path: "attachments/:id/preview",
            element: <AttachmentPreviewRoute />,
            handle: { title: "Attachment" },
          },
          {
            path: "usage",
            element: <DashboardPage />,
            handle: { title: "Usage" },
          },
          {
            path: "plan-billing",
            element: <PlanBillingPage />,
            handle: { title: "Plan & Billing" },
          },
          {
            path: "settings",
            element: <DesktopSettingsRoute />,
            handle: { title: "Settings" },
          },
        ],
      },
    ],
  },
];

/** Create an independent memory router for a tab. */
// react-doctor-disable-next-line react-doctor/only-export-components -- router factory must stay beside the shared route config.
export function createTabRouter(initialPath: string) {
  return createMemoryRouter(appRoutes, {
    initialEntries: [initialPath],
  });
}
