# Avatar URL backfill after OSS LocalStorage fallback

## When to run

After any window where uploads fell back to LocalStorage (`/uploads/workspaces/...`
URLs) and attachments were later moved to S3 with `migrate-uploads-to-s3`:

1. `attachment.url` may already be OSS direct links.
2. Denormalized `agent` / `user` / `workspace` / `channel`.`avatar_url` can still
   be `/uploads/...`. Message APIs join those fields as `author_avatar_url`, so
   history looks broken until the cascade runs.

`migrate-uploads-to-s3` now runs this cascade automatically at the end of a
migration. Use the standalone path below to re-run avatar-only (safe, idempotent).

## One-click on Aliyun deploy host

```bash
./scripts/run-backfill-avatar-urls.sh /data/multica
```

## Go tool (respects `S3_PUBLIC_BASE_URL`)

```bash
# dry-run: print candidate counts only
DATABASE_URL=... go run ./cmd/backfill-avatar-urls

# apply
DATABASE_URL=... S3_PUBLIC_BASE_URL=https://leagent.s3.oss-cn-beijing.aliyuncs.com \
  go run ./cmd/backfill-avatar-urls --dry-run=false
```

Default public base when unset: `https://leagent.s3.oss-cn-beijing.aliyuncs.com`.
