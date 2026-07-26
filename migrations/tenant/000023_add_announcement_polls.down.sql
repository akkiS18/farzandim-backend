-- Down Migration: Remove polls from announcements
DROP TABLE IF EXISTS announcement_poll_votes;
DROP TABLE IF EXISTS announcement_poll_options;
ALTER TABLE announcements DROP COLUMN IF EXISTS is_poll;
