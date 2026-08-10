# Next.js build assets on Aliyun OSS/CDN

Date: 2026-08-10
Scope: `apps/web` in `/private/tmp/multica-agent-avatar-oss`
Resolved framework version: Next.js `16.2.12` (`apps/web/package.json` declares `^16.2.11`)

## Recommendation

Use `assetPrefix` only for Next.js build artifacts under `/_next/static/`. Upload those artifacts from the **exact tagged web image** to the same OSS key prefix before switching the running frontend. Keep `public/` and `.next/static/` in the standalone image so an unset prefix remains a complete self-hosted deployment and so same-origin-only files and the built-in image optimizer continue to work.

Do not treat “all static files” as one Next.js switch:

| Asset class | Browser URL after the proposed change | OSS treatment | Origin copy |
| --- | --- | --- | --- |
| `.next/static/**` JavaScript, CSS, fonts, media | `https://cdn.leagent.me/_next/static/**` | Upload every deploy; immutable one-year cache; never delete during deploy | Keep in standalone image as fallback/self-host support |
| `public/**` referenced as `/foo` | Still `https://www.leagent.me/foo` | Not moved by `assetPrefix`; migrate selected assets explicitly to versioned OSS URLs | Keep, especially `sw.js`, favicons, manifests, and sources consumed by `next/image` |
| `next/image` optimized response | `https://www.leagent.me/_next/image?...` by default | Not a build artifact and not uploadable; it is generated at runtime | Keep on Next.js unless replacing it with a custom image loader/service |
| Application HTML, RSC/navigation responses, API, WebSocket | Main application/API origins | Do not upload to OSS | Keep behind Caddy/Next.js/backend |
| Agent preset avatars | Existing faces under `.../agent-avatars/v1/human-01.jpg`; new pool under `.../v2/agent-01.png` | Upload once under versioned, independently managed prefixes; immutable one-year cache | Retain the old Web copies for legacy clients; do not couple persisted DB URLs to a Next build ID |

## What Next.js officially does

### `assetPrefix`

`assetPrefix: "https://cdn.leagent.me"` causes Next.js to emit that prefix for JavaScript and CSS loaded from `/_next/`, whose files come from `.next/static/`. `cdn.leagent.me` already fronts the production OSS bucket for the versioned Computer release feed, so using that existing origin is lower-risk than introducing an unconfigured `static.leagent.me` hostname. The directory contents must be uploaded as OSS keys under `_next/static/`; the rest of `.next/` must not be published because it contains server code and configuration. `assetPrefix` does **not** rewrite files in `public/`. It is also not the mechanism for mounting an app below a URL subpath; that is `basePath`.

Official source: [Next.js `assetPrefix`](https://nextjs.org/docs/app/api-reference/config/next-config-js/assetPrefix) and the version-matched [v16.2.12 source document](https://github.com/vercel/next.js/blob/v16.2.12/docs/01-app/03-api-reference/05-config/01-next-config-js/assetPrefix.mdx).

Practical consequence: this works as a build-time setting. A container-start environment variable cannot retrofit the URLs already emitted by `next build`. The Docker build therefore needs a default-empty build argument, and only the Aliyun build should set it. A source-built self-host deployment leaves it unset and continues serving assets from itself.

### Files under `public/`

Next.js maps `public/avatars/me.png` to `/avatars/me.png`. These paths remain main-origin paths even when `assetPrefix` is configured. Next.js deliberately gives public files `Cache-Control: public, max-age=0` because stable names may change.

Official source: [Next.js `public` folder](https://nextjs.org/docs/app/api-reference/file-conventions/public-folder) and the [v16.2.12 source document](https://github.com/vercel/next.js/blob/v16.2.12/docs/01-app/03-api-reference/03-file-conventions/public-folder.mdx).

Selected public assets can be moved, but their references must change explicitly. Use content-hashed filenames or a release prefix such as `app-public/sha-abc1234/...` before applying a long immutable TTL. Stable public names should use revalidation or a short TTL. In this repository, `/sw.js` must remain same-origin because it registers with scope `/`; moving it to an OSS origin would break the service-worker security model. The favicon/application metadata are also better kept at their canonical same-origin paths.

### `next/image`

The default `next/image` loader performs optimization at request time, not during `next build`. Its default endpoint is `/_next/image`; `images.path` changes that optimizer API path, while `loaderFile` replaces URL generation with a custom service. An absolute external image source must match `images.remotePatterns`. Setting `assetPrefix` does not turn OSS into an image optimizer and does not upload optimized variants.

Official sources: [self-hosting: Image Optimization](https://nextjs.org/docs/app/guides/self-hosting#image-optimization), [`next/image` loader and configuration](https://nextjs.org/docs/app/api-reference/components/image), and [custom image loader configuration](https://nextjs.org/docs/app/api-reference/config/next-config-js/images).

For this repo, the landing page passes public paths such as `/images/landing-hero.png` to `next/image`. Keep those source files in `public/` and keep `/_next/image` on the Next.js server for the first OSS phase. If the goal later becomes removing image work from Next.js too, choose one explicit design:

1. a real image CDN/custom `loaderFile` that honors width and quality; or
2. OSS-hosted originals with `unoptimized`, accepting that the browser receives the original image.

If a CDN/proxy fronts `/_next/image`, it must forward `Accept`, because Next.js negotiates AVIF/WebP from that header. Optimized outputs have their own runtime cache and cannot be pre-uploaded as a finite build directory.

### Standalone output

`output: "standalone"` creates `.next/standalone` with traced runtime files and a minimal `server.js`, but it intentionally omits `public/` and `.next/static/`. Next.js says those directories can be copied into the standalone tree manually, after which `server.js` serves them.

Official source: [Next.js `output: standalone`](https://nextjs.org/docs/app/api-reference/config/next-config-js/output) and the [v16.2.12 source document](https://github.com/vercel/next.js/blob/v16.2.12/docs/01-app/03-api-reference/05-config/01-next-config-js/output.mdx).

The repository already follows this correctly:

- `apps/web/next.config.ts:35-49` enables standalone when `STANDALONE=true` and currently has no `assetPrefix` or deployment ID.
- `Dockerfile.web:65-70` copies `.next/standalone`, `.next/static`, and `public` into the runtime image.
- The runtime command is `node apps/web/server.js` (`Dockerfile.web:81`).

Those copies should remain even after OSS offload. Removing them would make the Dockerfile's default/unprefixed self-host mode incomplete, remove the local sources used by current `next/image` calls, and make OSS availability a hard startup dependency.

## Caching, versioning, and rollback

Next.js fingerprints immutable assets and serves them with `Cache-Control: public, max-age=31536000, immutable`. When OSS serves the objects directly, set equivalent object metadata because Next.js is no longer in the response path. Do not overwrite an existing object with different bytes at an immutable URL.

Official source: [Next.js self-hosting automatic caching](https://nextjs.org/docs/app/guides/self-hosting#automatic-caching). Alibaba Cloud OSS supports standard HTTP metadata such as `Cache-Control`; `ossutil set-meta` can apply it recursively ([official OSS metadata command](https://www.alibabacloud.com/help/en/oss/developer-reference/set-meta)).

Set `deploymentId` (or `NEXT_DEPLOYMENT_ID`) to the immutable image tag, for example `sha-abc1234`. Next.js then appends `?dpl=<deploymentId>` to static asset URLs and detects navigation-version mismatches. This is cache busting/version-skew protection, **not version-aware routing**: Next.js explicitly does not use `?dpl=` to route an asset request. Therefore:

- upload new assets before serving new HTML;
- retain older hashed objects across deploys;
- never use a deploy-time sync mode that deletes remote keys absent from the new build;
- keep objects for at least the browser/service-worker tail and rollback window (for example, 30–90 days), then prune with a reviewed OSS lifecycle rule rather than in the deployment transaction;
- a manual rollback should extract and upload the selected old image's assets again before starting it, making rollback self-healing if retention was shortened.

Official source: [Next.js `deploymentId`](https://nextjs.org/docs/app/api-reference/config/next-config-js/deploymentId), [version skew guidance](https://nextjs.org/docs/app/guides/self-hosting#version-skew), and the [v16.2.12 source document](https://github.com/vercel/next.js/blob/v16.2.12/docs/01-app/03-api-reference/05-config/01-next-config-js/deploymentId.mdx).

## CORS and OSS/CDN headers

For a dedicated OSS hostname, configure an exact read-only CORS rule for `https://www.leagent.me` (and any other real application origin): methods `GET` and `HEAD`; expose `ETag` and `Content-Length`; do not allow credentials; avoid `*` unless the bucket is intentionally fully public. This covers cross-origin fonts and current/future module fetches. If Alibaba Cloud CDN sits in front of OSS, configure response CORS at the CDN too: Alibaba's documentation says OSS CORS applies only to direct OSS-origin requests.

Official source: [Alibaba Cloud OSS CORS](https://www.alibabacloud.com/help/en/oss/user-guide/cors-12).

Also ensure the uploaded objects have correct `Content-Type` (`text/css`, JavaScript, font, image) and `Content-Encoding` metadata. A successful HTTP 200 with an HTML/error content type is not a valid asset deployment.

## Repository seams and safest deployment sequence

Current state:

- The web build is performed once by `docker/build-push-action` and pushed as both `latest` and immutable `sha-<shortsha>` (`.github/workflows/deploy.yml:145-213`).
- The deploy job pulls the exact frontend/backend tag and immediately starts the frontend (`.github/workflows/deploy.yml:443-484`).
- The Aliyun deploy already has OSS credentials in protected job environment and validates them before runtime mutation.
- Caddy currently sends every web path to the Next.js container (`deploy/aliyun/Caddyfile:10-16`); no Caddy change is necessary when HTML contains an absolute CDN prefix.
- `docker-compose.selfhost.yml:187-195` uses the same standalone image contract, so defaults must remain origin-capable.

Recommended sequence:

1. Add a default-empty build argument such as `NEXT_ASSET_PREFIX` to `Dockerfile.web`. In `next.config.ts`, use it for `assetPrefix` only when non-empty. Set `deploymentId` from the existing immutable application version/tag. Pass the CDN origin only in the Aliyun build lane; leave it unset in generic/source-built self-hosting.
2. Keep building the web image exactly once. Do **not** run a second host-side `next build` to obtain assets: a separate build can have a different build identity and no longer proves that the uploaded files match the deployed server.
3. In the deploy job, `compose pull frontend` as today. Create a stopped temporary container from `${MULTICA_WEB_IMAGE}:${MULTICA_IMAGE_TAG}` and copy `/app/apps/web/.next/static/` to a temporary directory. The current runtime image already contains the exact files at that path.
4. Upload the **contents** of that directory to `oss://<bucket>/_next/static/` without deleting any other object. `ossutil cp <local-static-dir> oss://<bucket>/_next/static/ --recursive --force --meta 'Cache-Control:public,max-age=31536000,immutable'` illustrates the mapping; pin and test the installed ossutil version because option spelling is CLI-version-specific. Alibaba's official `cp` documentation covers recursive directory upload: [ossutil `cp`](https://www.alibabacloud.com/help/en/oss/developer-reference/cp).
5. Before `compose up -d frontend`, select at least one emitted JS file, one CSS file when present, and one media/font file when present. `HEAD` their public CDN URLs and require 200, expected `Content-Type`, and `Cache-Control` containing `immutable`. Also require a non-empty local asset set.
6. Start the frontend only after the upload/probes pass. Then extend the existing public health step to fetch HTML, verify it contains the configured CDN `/_next/static/` origin, and fetch one referenced asset. A failed upload must leave the old frontend running.
7. Keep the existing `.next/static` and `public` copies in the image. The same Dockerfile then remains valid when `NEXT_ASSET_PREFIX` is unset, for local Compose, downstream self-hosting, and emergency origin serving.
8. For rollback dispatches, run the same extraction/upload/probe sequence against the selected rollback image tag before starting it.

The upload step belongs between the existing `compose pull frontend backend` and `compose up -d frontend`, but should ideally be a named script with shell tests and explicit inputs rather than more inline workflow shell.

## Rollout boundary

Ship in two phases:

1. **Build assets:** offload only `.next/static/**` with `assetPrefix`, keep runtime copies, add upload and served-asset proof, and configure `deploymentId`.
2. **Selected public assets:** move only high-volume assets through an explicit asset URL helper or static imports. Keep same-origin operational files (`sw.js`, metadata icons) and do not rewrite persisted agent-avatar URLs as release-scoped public paths.

This boundary captures the bandwidth win without changing runtime image semantics, breaking self-host users, or conflating OSS object storage with the runtime `next/image` optimizer.
