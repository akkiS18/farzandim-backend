-- Down Migration: Remove telegram_poll_id column from announcements
ALTER TABLE announcements DROP COLUMN IF EXISTS telegram_poll_id;
