-- env-dispatch message dispatch (handler/env_dispatch.go CreateChannelMessage)
-- writes channel_message.source = 'env_dispatch' to label messages originated by
-- an env-dispatch rollout. Migration 112_channels only allows ('multica', 'lark'),
-- so those inserts violate the auto-named channel_message_source_check (SQLSTATE
-- 23514) and the whole dispatch returns 500 "all rollouts failed". Extend the
-- check to admit 'env_dispatch'. The constraint name is the Postgres default for
-- an unnamed column CHECK (<table>_<column>_check); confirmed by the live error.
ALTER TABLE channel_message
    DROP CONSTRAINT IF EXISTS channel_message_source_check,
    ADD CONSTRAINT channel_message_source_check
        CHECK (source IN ('multica', 'lark', 'env_dispatch'));
