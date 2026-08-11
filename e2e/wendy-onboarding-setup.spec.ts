import { expect, test, type Page } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";

const databaseUrl = process.env.DATABASE_URL!;

async function db(sql: string, params: unknown[] = []) {
  const client = new pg.Client(databaseUrl);
  await client.connect();
  try {
    return await client.query(sql, params);
  } finally {
    await client.end();
  }
}

async function authenticate(page: Page, token: string) {
  await page.addInitScript((value) => {
    localStorage.setItem("multica_token", value);
  }, token);
}

test("workspace creation is gated through explicit Wendy setup and seeded #general welcome", async ({ page }) => {
  test.setTimeout(90_000);
  const stamp = Date.now();
  const email = `wendy-setup-${stamp}@multica.ai`;
  const api = new TestApiClient();
  await api.login(email, "Wendy Setup Owner");
  const workspace = await api.ensureWorkspace("Wendy Setup", `wendy-setup-${stamp}`);
  await db(
    `UPDATE "user"
     SET onboarded_at = now(),
         onboarding_questionnaire = '{"source":["other"],"source_skipped":false}'::jsonb
     WHERE email = $1`,
    [email],
  );
  const runtime = await db(
    `INSERT INTO agent_runtime (
       workspace_id, daemon_id, name, runtime_mode, provider, status,
       visibility, device_info, metadata, last_seen_at
     ) VALUES ($1, NULL, $2, 'cloud', 'e2e_wendy', 'online',
               'public', 'Wendy setup acceptance', '{}'::jsonb, now())
     RETURNING id`,
    [workspace.id, `Wendy Computer ${stamp}`],
  );
  const runtimeId = runtime.rows[0].id as string;

  await page.route(`**/api/runtimes/${runtimeId}/models`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        id: `catalog-${stamp}`,
        runtime_id: runtimeId,
        status: "completed",
        supported: true,
        custom_model_id_supported: false,
        models: [{ id: "wendy-e2e-model", label: "Wendy E2E Model", provider: "e2e" }],
      }),
    });
  });

  try {
    await authenticate(page, api.getToken()!);
    await page.goto(`/${workspace.slug}/issues`);

    await expect(page.getByRole("heading", { name: "Meet Wendy" })).toBeVisible();
    await page.getByTestId("runtime-picker-trigger").click();
    await page.getByTestId(`runtime-picker-option-${runtimeId}`).click();
    const modelTrigger = page.getByTestId("model-dropdown-trigger");
    await modelTrigger.click();
    await page.getByText("Wendy E2E Model", { exact: true }).last().click();
    await expect(modelTrigger).toContainText("Wendy E2E Model");
    await expect(modelTrigger).not.toContainText("e2e");
    await page.getByRole("button", { name: "Create Wendy" }).click();
    await expect(page.getByRole("heading", { name: "Meet Wendy" })).toBeHidden();

    const setup = await db(
      `SELECT workspace.onboarding_agent_id, agent.model, channel.id AS general_id,
              array_agg(message.content ORDER BY message.created_at, message.id) AS welcome
       FROM workspace
       JOIN agent ON agent.id = workspace.onboarding_agent_id
       JOIN channel ON channel.workspace_id = workspace.id AND channel.system_key = 'general'
       JOIN channel_message message
         ON message.channel_id = channel.id
        AND message.author_type = 'agent'
        AND message.author_id = agent.id
       WHERE workspace.id = $1
       GROUP BY workspace.onboarding_agent_id, agent.model, channel.id`,
      [workspace.id],
    );
    expect(setup.rowCount).toBe(1);
    expect(setup.rows[0].model).toBe("wendy-e2e-model");
    expect(setup.rows[0].welcome).toEqual([
      "Hi — I’m your Workspace Onboarding Agent. I can help turn the work you describe into a clear team of Agents.",
      "Tell me what you’re working on. I’ll discuss the role with you and prepare a Hiring Proposal for an Owner or Admin to review.",
    ]);

    await page.goto(`/${workspace.slug}/channels/${setup.rows[0].general_id}`);
    await expect(page.getByText("Hi — I’m your Workspace Onboarding Agent.", { exact: false })).toBeVisible();
    await expect(page.getByText("Tell me what you’re working on.", { exact: false })).toBeVisible();
  } finally {
    await db(`DELETE FROM workspace WHERE id = $1`, [workspace.id]);
    await db(`DELETE FROM "user" WHERE email = $1`, [email]);
  }
});
