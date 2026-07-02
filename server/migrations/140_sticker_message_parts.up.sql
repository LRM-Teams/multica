ALTER TABLE chat_message
  ADD COLUMN parts jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD CONSTRAINT chat_message_parts_array CHECK (jsonb_typeof(parts) = 'array');

ALTER TABLE channel_message
  ADD COLUMN parts jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD CONSTRAINT channel_message_parts_array CHECK (jsonb_typeof(parts) = 'array');

CREATE TABLE sticker_pack (
  id text PRIMARY KEY,
  workspace_id uuid REFERENCES workspace(id) ON DELETE CASCADE,
  name text NOT NULL,
  source text NOT NULL DEFAULT 'builtin',
  license text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT sticker_pack_id_format CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  CONSTRAINT sticker_pack_source_check CHECK (source IN ('builtin', 'local', 's3', 'cos'))
);

CREATE TABLE sticker_asset (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  pack_id text NOT NULL REFERENCES sticker_pack(id) ON DELETE CASCADE,
  sticker_id text NOT NULL,
  storage_key text NOT NULL,
  asset_url text NOT NULL DEFAULT '',
  mime_type text NOT NULL DEFAULT 'image/png',
  width integer,
  height integer,
  alt text NOT NULL DEFAULT '',
  tags jsonb NOT NULL DEFAULT '[]'::jsonb,
  animated boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT sticker_asset_id_format CHECK (sticker_id ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  CONSTRAINT sticker_asset_width_check CHECK (width IS NULL OR width > 0),
  CONSTRAINT sticker_asset_height_check CHECK (height IS NULL OR height > 0),
  CONSTRAINT sticker_asset_tags_array CHECK (jsonb_typeof(tags) = 'array'),
  UNIQUE (pack_id, sticker_id)
);

CREATE INDEX idx_sticker_pack_workspace ON sticker_pack (workspace_id);

INSERT INTO sticker_pack (id, workspace_id, name, source, license)
VALUES ('builtin', NULL, 'Built-in stickers', 'builtin', 'Apache-2.0')
ON CONFLICT (id) DO NOTHING;
