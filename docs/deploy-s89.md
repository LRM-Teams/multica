# s89 CI runner / host operations

> **Authoritative roles (Frank, 2026-07-28):**
>
> | Role | Host |
> | --- | --- |
> | **Shared Multica `dev` deploy target** | **Aliyun** `101.200.210.144` (**leagent.me**); workflow `.github/workflows/deploy.yml` (`runs-on: [self-hosted, aliyun]`, Environment `aliyun-dev`, stack `/data/multica`). See `docker-compose.aliyun.yml`, `docker-compose.oss.yml`, and `deploy/aliyun/`. |
> | **s89** | **CI runner host**, not the deploy target. It may still run jobs / hold residual services; that does **not** make it the product dev environment. |
>
> **Do not write that s89 is decommissioned** — it can remain as a runner.  
> **Do not use** `https://82.157.184.89` / s89 as “the dev stack” for product verification; use **leagent.me** / Aliyun. Confirm CD with `deploy.yml` if unsure.

This document is the host-side runbook for the **s89** self-hosted runner and
related TLS/proxy triage — **not** the continuous-deploy contract. The s89
runner reaches GitHub through the host-local `sing-box` proxy at
`http://127.0.0.1:7893` when that path is in use. Do not put credentials, proxy
configuration, or database passwords in workflow logs or this repository.

## Services and paths

| Purpose | Value |
| --- | --- |
| Runner service | `actions.runner.LRM-Teams-multica.s89.service` |
| Runner user | `gha` |
| Runner directory | `/home/gha/actions-runner` |
| Runner diagnostics | `/home/gha/actions-runner/_diag` |
| Outbound proxy service | `sing-box.service` |
| Outbound proxy address | `http://127.0.0.1:7893` |
| Historical browser entry (not shared dev CD) | `https://82.157.184.89` |
| Historical daemon/API HTTP entry | `http://82.157.184.89:8090` (HTML browser navigations redirect to HTTPS) |
| Caddy config source (s89 host) | `deploy/s89/Caddyfile` |
| Shared dev product URL | **https://leagent.me** (Aliyun CD) |

The **Aliyun** deploy workflow runs only after `dev` changes (or an explicit
manual dispatch) on runners labeled `aliyun`. If a job fails during
`Set up job` on **this** s89 runner, that is runner/proxy/TLS triage for s89 —
not evidence about the Aliyun Multica Compose stack, images, or migrations.

## TLS/action-download incident signature

Task #459 investigated Deploy run `29378712773` on 2026-07-15. Two deploy-job
attempts failed during job initialization, then the third attempt completed the
same workflow end to end.

The runner diagnostics pin the immediate cause to the outbound proxy/TLS path:

- runner `2.335.1` was already running continuously; there was no runner-service
  restart or workflow change between the attempts;
- both failed workers reached job dispatch, then failed while resolving actions;
- requests to both `launch.actions.githubusercontent.com` and
  `results-receiver.actions.githubusercontent.com` ended during the TLS
  handshake with `unexpected EOF or 0 bytes from the transport stream`;
- the terminal error was `FailedToResolveActionDownloadInfoException`;
- the third attempt started at `00:40:32Z` and succeeded at `00:41:20Z`.

This is not a certificate-validation error (`unknown authority`, expired
certificate, or hostname mismatch). It is a transient failure in the s89
runner's proxied outbound connection. The retained logs do not identify which
remote tunnel/provider component closed the TLS stream, so do not claim a more
specific upstream cause without new proxy-side evidence.

## Read-only triage

Run these commands on s89. They do not restart services or modify the deployed
stack.

### 1. Confirm both services are healthy

```bash
systemctl status actions.runner.LRM-Teams-multica.s89.service --no-pager -l
systemctl status sing-box.service --no-pager -l
sudo ss -ltnp '( sport = :7893 )'
```

The runner must be listening for jobs, `sing-box` must be active, and port 7893
must have a listener.

### 2. Probe the exact outbound path as the runner user

```bash
sudo -u gha curl --proxy http://127.0.0.1:7893 \
  --connect-timeout 10 --max-time 20 -sS -o /dev/null \
  -w 'api.github.com %{http_code} %{time_appconnect}\n' \
  https://api.github.com

sudo -u gha curl --proxy http://127.0.0.1:7893 \
  --connect-timeout 10 --max-time 20 -sS -o /dev/null \
  -w 'launch.actions.githubusercontent.com %{http_code} %{time_appconnect}\n' \
  https://launch.actions.githubusercontent.com

sudo -u gha curl --proxy http://127.0.0.1:7893 \
  --connect-timeout 10 --max-time 20 -sS -o /dev/null \
  -w 'results-receiver.actions.githubusercontent.com %{http_code} %{time_appconnect}\n' \
  https://results-receiver.actions.githubusercontent.com
```

`api.github.com` should return 200. The two Actions service roots may return
404; that still proves DNS, proxy CONNECT, and TLS completed. A curl TLS error,
timeout, or HTTP code `000` means the host path is still unhealthy.

### 3. Read the runner diagnostics for the failed job

```bash
sudo grep -Eni \
  'FailedToResolveActionDownloadInfo|SSL connection|unexpected EOF|runnerresolve|results-receiver' \
  /home/gha/actions-runner/_diag/Worker_*-utc.log
```

Use the timestamps from the GitHub job when narrowing the file. The listener
log records job dispatch and worker exit; the worker log contains the actual
action-resolution/TLS exception.

### 4. Decide whether a rerun is safe

- If the proxy probes are healthy and the failure happened before checkout,
  rerun the failed deploy job once. Do not change application code or manually
  restart the Multica stack for this signature.
- If probes still fail, keep the deploy blocked and inspect `sing-box`/network
  health. Repeated reruns only hide the outage and add queue noise.
- If checkout started, classify the later failure from its actual step logs;
  this runbook no longer applies.

## Prevention and monitoring

Action resolution happens before the first workflow step, so a workflow-level
preflight cannot catch this failure. Monitor it on the host instead.

Recommended host monitor (systemd timer or existing host monitoring):

1. verify `actions.runner.LRM-Teams-multica.s89.service` and
   `sing-box.service` are active;
2. verify port 7893 is listening;
3. run the three runner-user proxy probes above;
4. alert after two consecutive probe failures, and include the failing endpoint
   plus curl/TLS error;
5. retain runner and proxy logs long enough to cover deploy-incident review.

When changing host service configuration, ensure the runner starts only after
`network-online.target` and `sing-box.service`. Do not log proxy credentials or
the contents of runner credential files.

## Normal deploy verification

After a successful job, verify the deployed state separately from the runner
transport:

```bash
docker compose -p multica ps
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8090/api/config
curl --connect-to 82.157.184.89:443:127.0.0.1:443 \
  -fsS https://82.157.184.89/healthz
```

The HTTPS probe validates the public-IP certificate while connecting over
loopback, so provider NAT hairpin behavior cannot create a false failure. Caddy
uses Let's Encrypt's `shortlived` profile; the certificate lasts about six days
and is renewed automatically from the persistent `multica_caddy_data` volume.
Ports 80 and 443 must remain publicly reachable for HTTP-01 validation and
browser traffic. Do not replace the public certificate with Caddy's internal
CA: browsers that do not trust that private root would still reject microphone
capture.

Old `:8090` browser bookmarks receive a permanent redirect to the HTTPS origin,
while non-HTML daemon and API requests remain available on HTTP. Google Web
OAuth cannot use a non-localhost raw IP callback and is disabled on this IP-only
entrypoint. Enabling it requires a DNS browser origin, a matching Caddy site and
certificate, and a registered callback for that same origin.

For a migration-sensitive change, also read the exact `schema_migrations` row;
`/readyz` proves the current migration gate is healthy but is not a substitute
for an immutable version/timestamp ledger when that exact evidence is required.
