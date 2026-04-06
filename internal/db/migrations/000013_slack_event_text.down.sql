ALTER TABLE companies DROP COLUMN IF EXISTS slack_bot_user_id;
ALTER TABLE slack_events DROP COLUMN IF EXISTS text;
