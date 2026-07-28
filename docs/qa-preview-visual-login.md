# QA preview visual login

This is the QA-owned path for agents to get a browser login state on preview or a target deployment and attach Playwright screenshots to in-review visual issues without asking Frank to hard-refresh manually.

## Selected QA account

Use this shared human QA member for preview visual checks:

- Email: `qa-preview@multica.ai`
- Display name: `QA Preview`
- Workspace slug: `multica-qa`

The account must be a normal workspace member in the target environment. Do not use Frank's personal account and do not post passwords, verification codes, JWTs, PATs, or database URLs in issue comments or group chat.

## Supported auth paths

`e2e/preview-auth.ts` intentionally mirrors `e2e/helpers.ts` `loginAsDefault`.

### Preferred: secret-injected token

Operations or the backend owner provisions a short-lived human browser token for `qa-preview@multica.ai` in the agent secret store:

```bash
QA_PREVIEW_TOKEN=...                 # secret, never printed
QA_PREVIEW_WORKSPACE_SLUG=multica-qa
```

The helper opens `/login`, writes `QA_PREVIEW_TOKEN` to `localStorage.multica_token`, and navigates to `/<workspace>/issues`. This matches the legacy token mode that the web app still accepts for automated browser sessions.

### Fallback: DB OTP read

When a preview database tunnel is available, use the same flow as `loginAsDefault`:

1. POST `/auth/send-code` for `QA_PREVIEW_EMAIL`.
2. Read the newest unused OTP from `verification_code` in the target database.
3. POST `/auth/verify-code` to receive the JWT.
4. Inject the JWT into `localStorage.multica_token` and open the target page.

Required env:

```bash
PLAYWRIGHT_BASE_URL=https://preview.example.com
NEXT_PUBLIC_API_URL=https://preview.example.com/api-or-backend-origin
DATABASE_URL=postgres://...          # secret, never printed
QA_PREVIEW_EMAIL=qa-preview@multica.ai
QA_PREVIEW_NAME="QA Preview"
QA_PREVIEW_WORKSPACE_SLUG=multica-qa
```

## Desktop Playwright capture

Run a single authenticated visual smoke against an in-review issue:

```bash
QA_VISUAL_ISSUE_ID=<issue-uuid> \
PLAYWRIGHT_BASE_URL=https://preview.example.com \
QA_PREVIEW_WORKSPACE_SLUG=multica-qa \
QA_PREVIEW_TOKEN=$QA_PREVIEW_TOKEN \
pnpm exec playwright test e2e/preview-visual.spec.ts --project=chromium
```

If using the DB OTP fallback, replace `QA_PREVIEW_TOKEN` with `NEXT_PUBLIC_API_URL` and `DATABASE_URL`.

After the run, upload the screenshot and attach it to the visual issue:

```bash
multica attachment upload --path test-results/<run>/preview-visual-<issue>.png
cat <<'COMMENT' | multica issue comment add <issue-id> --attachment-id <attachment-id> --content-stdin
QA preview visual pass via qa-preview@multica.ai.

Evidence: attached Playwright screenshot.
COMMENT
```

## Operational checklist

- Confirm `qa-preview@multica.ai` exists and is a member of the target workspace.
- Store `QA_PREVIEW_TOKEN` or the DB tunnel `DATABASE_URL` as an agent secret; never paste it in comments.
- Set `QA_VISUAL_ISSUE_ID` to the in-review issue being checked.
- Run the Playwright smoke and attach the screenshot to that issue.
- If login fails at `/login`, verify account membership and token freshness before asking for a product review.
