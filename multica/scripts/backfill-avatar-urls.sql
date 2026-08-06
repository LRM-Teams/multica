-- One-off / re-runnable backfill: rewrite stale LocalStorage-style avatar URLs
-- ("/uploads/workspaces/...") to the public OSS base URL after migrate-uploads-to-s3.
-- Prefer `go run ./cmd/backfill-avatar-urls` (respects S3_PUBLIC_BASE_URL) or
-- scripts/run-backfill-avatar-urls.sh on the Aliyun deploy host.
--
-- Safe to re-run: only rows still starting with "/uploads/" are touched.

BEGIN;

-- 1) Prefer already-migrated attachment.url for uploaded agent avatars.
UPDATE agent a
SET avatar_url = att.url
FROM attachment att
WHERE a.avatar_attachment_id = att.id
  AND a.avatar_source = 'uploaded'
  AND a.avatar_url LIKE '/uploads/%'
  AND att.url NOT LIKE '/uploads/%';

-- 2) Rewrite remaining site-relative workspace avatar paths.
UPDATE agent
SET avatar_url = 'https://leagent.s3.oss-cn-beijing.aliyuncs.com/' || substring(avatar_url from '/uploads/(.*)')
WHERE avatar_url LIKE '/uploads/workspaces/%';

UPDATE "user"
SET avatar_url = 'https://leagent.s3.oss-cn-beijing.aliyuncs.com/' || substring(avatar_url from '/uploads/(.*)')
WHERE avatar_url LIKE '/uploads/workspaces/%';

UPDATE workspace
SET avatar_url = 'https://leagent.s3.oss-cn-beijing.aliyuncs.com/' || substring(avatar_url from '/uploads/(.*)')
WHERE avatar_url LIKE '/uploads/workspaces/%';

UPDATE channel
SET avatar_url = 'https://leagent.s3.oss-cn-beijing.aliyuncs.com/' || substring(avatar_url from '/uploads/(.*)')
WHERE avatar_url LIKE '/uploads/workspaces/%';

-- channel_message does not persist author_avatar_url; list/WS handlers join
-- agent/user.avatar_url at read time, so no message-row backfill is needed.

COMMIT;
