-- Up Migration: Add polls to announcements
ALTER TABLE announcements ADD COLUMN IF NOT EXISTS is_poll BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS announcement_poll_options (
    id SERIAL PRIMARY KEY,
    announcement_id INT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    option_text VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS announcement_poll_votes (
    id SERIAL PRIMARY KEY,
    announcement_id INT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    option_id INT NOT NULL REFERENCES announcement_poll_options(id) ON DELETE CASCADE,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (announcement_id, user_id)
);
