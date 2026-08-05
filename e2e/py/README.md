# Env-Dispatch Issue E2E Test (pytest harness)

On-demand E2E suite proving the full non-training env-dispatch loop: an issue
is submitted to a single agent, a coding agent completes the fixture
code task inside forked Cube sandboxes across a `scratch -> branch -> resume`
chain, and this harness independently verifies lineage and acceptance checks
via the Cube `/execute` endpoint. Agent self-reported results are never
accepted as verdicts.

Full provisioning/validation guide:
[`specs/002-env-dispatch-issue-e2e/quickstart.md`](../../../specs/002-env-dispatch-issue-e2e/quickstart.md)
(outer repo).

## Setup

```bash
cp .env.example .env          # fill in values for the shared deployment
python provision_fixture.py   # one-time: prints MULTICA_BASE_ENV_ID=<uuid>, add it to .env
```

Required env vars (the e2e tests **skip** when any is absent; `.env` is
gitignored and loaded via python-dotenv when installed):

| Variable | Purpose |
|---|---|
| `MULTICA_BASE_URL` | multica server base URL |
| `MULTICA_WORKSPACE_ID` or `MULTICA_WORKSPACE_SLUG` | workspace scoping (query param) |
| `MULTICA_API_KEY` (or `MULTICA_CREDENTIALS_FILE`) | PAT auth (Bearer); file is JSON `{"api_key": ...}` |
| `MULTICA_AGENT_ID` | target agent UUID (squad product retired) |
| `MULTICA_BASE_ENV_ID` | fixture base env from `provision_fixture.py` |
| `CUBE_PROXY_URL` | Cube HTTP proxy for `/execute` |

Optional: `CUBE_DOMAIN` (default `cube.app`), `E2E_STAGE_TIMEOUT_SEC`
(default `1200`), `E2E_DAG_POLL_INTERVAL_SEC` (default `10`),
`E2E_NEGATIVE_CONTROL` (default `0`). See `.env.example` for details.

## Run

```bash
pytest tests/unit -v          # hermetic unit tests — run anywhere, no env needed
pytest -m e2e -v              # full chained e2e (scratch -> branch -> resume)
E2E_NEGATIVE_CONTROL=1 pytest -m e2e -v tests/test_negative_control.py
```

Marker usage: the `e2e` marker is registered in `pytest.ini`, and the default
run excludes e2e tests (`addopts = -m "not e2e"`) — pass `-m e2e` explicitly
to opt in. All e2e tests additionally skip via `requires_env` (conftest.py)
when the required env vars above are missing.

Expected outcomes:

- Chained e2e green: each stage's DAG assembles (>= 1 segment), lineage
  markers (`SOLUTION.md`, implemented `fixture_calc.calculator.add`) are
  present at branch/resume before crediting the agent, and the acceptance
  checks (`tests/test_calculator.py` + `tests/test_sanity.py` in
  `/workspace/repo`) exit 0 at every stage. All dispatch projects are
  DELETEd at the end (guaranteed cleanup in `run_chain`).
- Negative control red **by design**: the run fails at stage `scratch` /
  phase `verification`, and the stdout FailureReport names that stage+phase —
  proving the suite is not vacuously green.
- On any failure, stdout contains a FailureReport (stage, phase, dispatch /
  DAG / task snapshots, per-stage cleanup results, triage hints).

## Layout

```text
e2e_harness/          # config, auth, multica_client, sandbox_exec, chain, diagnostics
fixture_repo/         # fixture-calc package baked into the Cube template
provision_fixture.py  # one-time base-env provisioning (research R4)
tests/unit/           # mocked unit tests (config, exec parser, chain, diagnostics)
tests/test_env_dispatch_issue_chain.py   # scratch-only + full-chain e2e
tests/test_negative_control.py           # unsatisfiable-check e2e
```
