-- Up Migration: Create telegram_polls table to map individual Telegram poll IDs to announcements
CREATE TABLE IF NOT EXISTS telegram_polls (
    id SERIAL PRIMARY KEY,
    announcement_id INT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    telegram_poll_id VARCHAR(255) NOT NULL UNIQUE,
    chat_id BIGINT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
