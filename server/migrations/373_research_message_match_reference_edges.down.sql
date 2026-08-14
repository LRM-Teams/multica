DELETE FROM research_artifact_input_reference
WHERE relation IN ('match_utterance','match_primary_anchor','match_candidate','match_decision')
  AND purpose='match_decision_migration';
DROP FUNCTION IF EXISTS research_artifact_materialize_message_match_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_message_match_reference(UUID,UUID,UUID,UUID,TEXT,TEXT,TEXT,TEXT,INTEGER);
