/**
 * TestApiClient — lightweight API helper for E2E test data setup/teardown.
 *
 * Uses raw fetch so E2E tests have zero build-time coupling to the web app.
 */

import "./env";
import pg from "pg";

// `||` (not `??`) so an empty `NEXT_PUBLIC_API_URL=` in .env still falls
// back to localhost. dotenv sets unset-vs-empty both as "" — treating them
// the same matches user intent.
const API_BASE = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL = process.env.DATABASE_URL ?? "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

interface TestWorkspace {
  id: string;
  name: string;
  slug: string;
  onboarding_agent_id?: string | null;
}

export class TestApiClient {
  private token: string | null = null;
  private workspaceSlug: string | null = null;
  private workspaceId: string | null = null;
  private userId: string | null = null;
  private createdIssueIds: string[] = [];

  async login(email: string, name: string) {
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      // Keep each E2E login isolated so previous test runs do not trip the
      // per-email send-code rate limit.
      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      // Step 1: Send verification code
      const sendRes = await fetch(`${API_BASE}/auth/send-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      });
      if (!sendRes.ok) {
        throw new Error(`send-code failed: ${sendRes.status}`);
      }

      // Step 2: Read code from database
      const result = await client.query(
        "SELECT code FROM verification_code WHERE email = $1 AND used = FALSE AND expires_at > now() ORDER BY created_at DESC LIMIT 1",
        [email],
      );
      if (result.rows.length === 0) {
        throw new Error(`No verification code found for ${email}`);
      }

      // Step 3: Verify code to get JWT
      const verifyRes = await fetch(`${API_BASE}/auth/verify-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, code: result.rows[0].code }),
      });
      if (!verifyRes.ok) {
        throw new Error(`verify-code failed: ${verifyRes.status}`);
      }
      const data = await verifyRes.json();

      this.token = data.token;
      this.userId = data.user?.id ?? null;

      // Update user name if needed
      if (name && data.user?.name !== name) {
        await this.authedFetch("/api/me", {
          method: "PATCH",
          body: JSON.stringify({ name }),
        });
      }

      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      return data;
    } finally {
      await client.end();
    }
  }

  async getWorkspaces(): Promise<TestWorkspace[]> {
    const res = await this.authedFetch("/api/workspaces");
    return res.json();
  }

  setWorkspaceId(id: string) {
    this.workspaceId = id;
  }

  setWorkspaceSlug(slug: string) {
    this.workspaceSlug = slug;
  }

  async ensureWorkspace(name = "E2E Workspace", slug = "e2e-workspace") {
    const workspaces = await this.getWorkspaces();
    const workspace = workspaces.find((item) => item.slug === slug) ?? workspaces[0];
    if (workspace) {
      this.workspaceId = workspace.id;
      this.workspaceSlug = workspace.slug;
      return workspace;
    }

    const res = await this.authedFetch("/api/workspaces", {
      method: "POST",
      body: JSON.stringify({ name, slug }),
    });
    if (res.ok) {
      const created = (await res.json()) as TestWorkspace;
      this.workspaceId = created.id;
      return created;
    }

    const refreshed = await this.getWorkspaces();
    const created = refreshed.find((item) => item.slug === slug) ?? refreshed[0];
    if (created) {
      this.workspaceId = created.id;
      return created;
    }

    throw new Error(`Failed to ensure workspace ${slug}: ${res.status} ${res.statusText}`);
  }

  /**
   * Put a generic E2E workspace past product onboarding gates.
   *
   * Tests for the onboarding/setup flows deliberately do not call this.
   * Other browser tests need a real completed-onboarding response and a
   * server-provisioned onboarding Agent so they exercise the workspace UI
   * instead of stopping at unrelated gates.
   */
  async ensureWorkspaceReady(workspace: TestWorkspace) {
    if (!this.token || !this.userId) {
      throw new Error("login is required before preparing an E2E workspace");
    }

    this.workspaceId = workspace.id;
    this.workspaceSlug = workspace.slug;

    const questionnaireRes = await this.authedFetch("/api/me/onboarding", {
      method: "PATCH",
      body: JSON.stringify({
        questionnaire: {
          source: [],
          source_skipped: true,
          role: "",
          role_skipped: true,
          use_case: [],
          use_case_skipped: true,
          version: 2,
        },
      }),
    });
    if (!questionnaireRes.ok) {
      throw new Error(
        `prepare onboarding questionnaire failed: ${questionnaireRes.status} ${await questionnaireRes.text()}`,
      );
    }

    const onboardingRes = await this.authedFetch("/api/me/onboarding/complete", {
      method: "POST",
      body: JSON.stringify({
        completion_path: "skip_existing",
        workspace_id: workspace.id,
      }),
    });
    if (!onboardingRes.ok) {
      throw new Error(
        `complete onboarding failed: ${onboardingRes.status} ${await onboardingRes.text()}`,
      );
    }

    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    let runtimeId: string | null = null;
    try {
      const binding = await client.query(
        "SELECT onboarding_agent_id FROM workspace WHERE id = $1",
        [workspace.id],
      );
      if (binding.rows[0]?.onboarding_agent_id) return;

      const runtime = await client.query(
        `INSERT INTO agent_runtime (
           workspace_id, daemon_id, name, runtime_mode, provider, status,
           device_info, metadata, last_seen_at, visibility
         ) VALUES ($1, NULL, $2, 'cloud', 'e2e_workspace_setup', 'online',
                   'E2E workspace setup runtime', '{}'::jsonb, now(), 'private')
         RETURNING id`,
        [workspace.id, `E2E Workspace Setup ${workspace.id}`],
      );
      runtimeId = runtime.rows[0].id as string;
    } finally {
      await client.end();
    }

    const windyRes = await this.authedFetch("/api/members/agents/windy", {
      method: "POST",
      body: JSON.stringify({ runtime_id: runtimeId, model: "e2e-model" }),
    });
    if (!windyRes.ok) {
      throw new Error(
        `prepare onboarding agent failed: ${windyRes.status} ${await windyRes.text()}`,
      );
    }
  }

  async createIssue(title: string, opts?: Record<string, unknown>) {
    const res = await this.authedFetch("/api/issues", {
      method: "POST",
      body: JSON.stringify({ title, ...opts }),
    });
    const issue = await res.json();
    this.createdIssueIds.push(issue.id);
    return issue;
  }

  async deleteIssue(id: string) {
    await this.authedFetch(`/api/issues/${id}`, { method: "DELETE" });
  }

  /** Clean up all issues created during this test. */
  async cleanup() {
    for (const id of this.createdIssueIds) {
      try {
        await this.deleteIssue(id);
      } catch {
        /* ignore — may already be deleted */
      }
    }
    this.createdIssueIds = [];
  }

  getToken() {
    return this.token;
  }

  getUserId() {
    return this.userId;
  }

  private async authedFetch(path: string, init?: RequestInit) {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...((init?.headers as Record<string, string>) ?? {}),
    };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (this.workspaceSlug) headers["X-Workspace-Slug"] = this.workspaceSlug;
    else if (this.workspaceId) headers["X-Workspace-ID"] = this.workspaceId;
    return fetch(`${API_BASE}${path}`, { ...init, headers });
  }
}
