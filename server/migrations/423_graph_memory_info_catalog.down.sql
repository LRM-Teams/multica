-- Drops the necessary-information catalog (reverse of 423 up).
DROP TRIGGER IF EXISTS graph_memory_recall_info_item_identity ON graph_memory_recall_info_item;
DROP FUNCTION IF EXISTS graph_memory_recall_info_item_validate_identity();
DROP TRIGGER IF EXISTS graph_memory_info_item_identity ON graph_memory_info_item;
DROP FUNCTION IF EXISTS graph_memory_info_item_validate_identity();
DROP TABLE IF EXISTS graph_memory_recall_info_item;
DROP TABLE IF EXISTS graph_memory_info_item_node;
DROP TABLE IF EXISTS graph_memory_info_item;
