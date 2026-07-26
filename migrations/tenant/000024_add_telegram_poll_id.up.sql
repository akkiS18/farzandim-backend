-- Up Migration: Add telegram_poll_id column to announcements
ALTER TABLE announcements ADD COLUMN IF NOT EXISTS telegram_poll_id VARCHAR(255);
