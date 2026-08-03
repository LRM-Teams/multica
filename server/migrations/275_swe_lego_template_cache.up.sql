CREATE TABLE swe_lego_template_cache (
  node_id uuid NOT NULL REFERENCES sandbox_node(id) ON DELETE CASCADE,
  cache_key text NOT NULL CHECK (length(cache_key) = 64),
  parent_template_id text NOT NULL,
  task_template_id text,
  status text NOT NULL CHECK (status IN ('building', 'ready', 'failed')),
  error text,
  builder_instance_id uuid REFERENCES sandbox_instance(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (node_id, cache_key),
  CHECK ((status = 'ready') = (task_template_id IS NOT NULL))
);
