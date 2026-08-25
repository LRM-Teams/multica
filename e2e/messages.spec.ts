import "./env";
import { expect, test } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

interface Workspace {
  id: string;
  name: string;
  slug: string;
}

interface Channel {
  id: string;
  name: string;
}

interface Message {
  id: string;
  seq: number;
  content: string;
}

async function authedFetch(
  api: TestApiClient,
  workspace: Workspace,
  path: string,
  init?: RequestInit,
): Promise<Response> {
  const token = api.getToken();
  if (!token) throw new Error("test api client not logged in");
  return fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      "X-Workspace-ID": workspace.id,
      "X-Workspace-Slug": workspace.slug,
      ...((init?.headers as Record<string, string>) ?? {}),
    },
  });
}

async function jsonRequest<T>(
  api: TestApiClient,
  workspace: Workspace,
  path: string,
  init?: RequestInit,
): Promise<T> {
  const response = await authedFetch(api, workspace, path, init);
  if (!response.ok) {
    throw new Error(`${init?.method ?? "GET"} ${path}: ${response.status} ${await response.text()}`);
  }
  return response.json() as Promise<T>;
}

test.describe("Messages", () => {
  let owner: TestApiClient;
  let reader: TestApiClient;
  let database: pg.Client;
  let workspace: Workspace;
  let channel: Channel | null;

  test.beforeEach(async () => {
    const run = `${process.pid}-${Date.now()}`;
    owner = new TestApiClient();
    reader = new TestApiClient();
    database = new pg.Client(DATABASE_URL);
    await database.connect();

    await owner.login(`messages-owner-${run}@multica.ai`, "Messages Owner");
    workspace = await owner.ensureWorkspace(`Messages ${run}`, `messages-${run}`);
    await owner.ensureWorkspaceReady(workspace);

    await reader.login(`messages-reader-${run}@multica.ai`, "Messages Reader");
    const readerId = reader.getUserId();
    if (!readerId) throw new Error("reader user missing");
    await database.query(
      `INSERT INTO member (workspace_id, user_id, role)
       VALUES ($1, $2, 'member')
       ON CONFLICT (workspace_id, user_id) DO NOTHING`,
      [workspace.id, readerId],
    );
    reader.setWorkspaceId(workspace.id);
    reader.setWorkspaceSlug(workspace.slug);
    await reader.ensureWorkspaceReady(workspace);

    channel = await jsonRequest<Channel>(owner, workspace, "/api/channels", {
      method: "POST",
      body: JSON.stringify({ name: `message-e2e-${run}` }),
    });
    await authedFetch(owner, workspace, `/api/channels/${channel.id}/members`, {
      method: "POST",
      body: JSON.stringify({ member_type: "user", member_id: readerId }),
    });
  });

  test.afterEach(async () => {
    try {
      if (channel) {
        await authedFetch(owner, workspace, `/api/channels/${channel.id}`, {
          method: "DELETE",
        });
      }
    } finally {
      await database.end();
      channel = null;
    }
  });

  test("send, read state, unread divider, and message deep-link stay coherent", async ({ page }) => {
    if (!channel) throw new Error("channel missing");
    const created: Message[] = [];
    for (let index = 1; index <= 24; index += 1) {
      created.push(
        await jsonRequest<Message>(owner, workspace, `/api/channels/${channel.id}/messages`, {
          method: "POST",
          body: JSON.stringify({ content: `history message ${String(index).padStart(2, "0")}` }),
        }),
      );
    }

    await authedFetch(reader, workspace, `/api/channels/${channel.id}/read`, { method: "POST" });

    const unread: Message[] = [];
    for (let index = 1; index <= 5; index += 1) {
      unread.push(
        await jsonRequest<Message>(owner, workspace, `/api/channels/${channel.id}/messages`, {
          method: "POST",
          body: JSON.stringify({ content: `unread message ${index}` }),
        }),
      );
    }

    const token = reader.getToken();
    if (!token) throw new Error("reader token missing");
    await page.addInitScript((value) => localStorage.setItem("multica_token", value), token);
    await page.goto(`/${workspace.slug}/channels/${channel.id}`);

    const scroller = page.getByTestId("message-scroller");
    await expect(scroller).toBeVisible({ timeout: 30_000 });
    await expect(page.getByText("unread message 5", { exact: true })).toBeVisible();
    await expect(page.getByTestId("unread-divider")).toContainText("5 new messages");

    await expect
      .poll(async () => {
        const result = await database.query<{ last_read_seq: string }>(
          `SELECT last_read_seq::text
           FROM conversation_member
           WHERE conversation_id = $1 AND member_type = 'user' AND member_id = $2`,
          [channel!.id, reader.getUserId()],
        );
        return Number(result.rows[0]?.last_read_seq ?? 0);
      })
      .toBe(unread.at(-1)!.seq);

    const targetComposer = page.getByRole("textbox", { name: `Message #${channel.name}` });
    await expect(targetComposer).toBeVisible();
    await targetComposer.click();
    const editor = page.locator('[data-composer-surface="channel"] .ProseMirror');
    await expect(editor).toBeVisible();
    const sentText = `reader sent ${Date.now()}`;
    await editor.fill(sentText);
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.getByText(sentText, { exact: true })).toBeVisible();
    await expect(page.getByText(sentText, { exact: true })).toHaveCount(1);
    await expect
      .poll(async () =>
        database
          .query<{ count: string }>(
            `SELECT count(*)::text FROM channel_message WHERE channel_id = $1 AND content = $2`,
            [channel!.id, sentText],
          )
          .then((result) => Number(result.rows[0]?.count ?? 0)),
      )
      .toBe(1);

    await editor.fill("#");
    const channelSuggestion = page
      .locator('div[style*="position: fixed"][style*="z-index: 50"] button')
      .filter({ hasText: channel.name });
    await expect(channelSuggestion).toBeVisible();
    await editor.press("Enter");
    await expect(editor.locator(".channel-mention")).toContainText(channel.name);
    await expect(page.getByText(sentText, { exact: true })).toHaveCount(1);
    await expect
      .poll(async () =>
        database
          .query<{ count: string }>(
            `SELECT count(*)::text FROM channel_message WHERE channel_id = $1 AND content = '#'`,
            [channel!.id],
          )
          .then((result) => Number(result.rows[0]?.count ?? 0)),
      )
      .toBe(0);

    const target = created[3]!;
    await page.goto(`/${workspace.slug}/channels/${channel.id}?message=${target.id}`);
    const targetBubble = page.locator(`[data-testid="message-bubble"][data-message-id="${target.id}"]`);
    await expect(targetBubble).toBeVisible();
    await expect(targetBubble).toHaveClass(/ring-1/);
    await expect
      .poll(async () =>
        targetBubble.evaluate((element) => {
          const targetRect = element.getBoundingClientRect();
          const scroller = element.closest('[data-testid="message-scroller"]');
          if (!scroller) return Number.POSITIVE_INFINITY;
          const scrollerRect = scroller.getBoundingClientRect();
          return Math.abs(
            targetRect.top + targetRect.height / 2 -
              (scrollerRect.top + scrollerRect.height / 2),
          );
        }),
      )
      .toBeLessThan(140);
  });
});
