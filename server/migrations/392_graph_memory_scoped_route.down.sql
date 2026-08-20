ALTER TABLE graph_memory_profile
  DROP COLUMN IF EXISTS scoped_writer_ready,
  DROP COLUMN IF EXISTS timezone;
DROP TABLE IF EXISTS graph_memory_consolidation_run;
DROP TABLE IF EXISTS graph_memory_channel_lineage;
DROP TABLE IF EXISTS graph_memory_channel_route;
