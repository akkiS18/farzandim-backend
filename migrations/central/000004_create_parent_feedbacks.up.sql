CREATE TABLE IF NOT EXISTS parent_feedbacks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_user_id BIGINT NOT NULL,
    telegram_username VARCHAR(255),
    first_name VARCHAR(255),
    last_name VARCHAR(255),
    message_type VARCHAR(50) NOT NULL, -- text, voice, audio, photo, document, video
    content TEXT,
    telegram_file_id TEXT,
    admin_message_ids JSONB DEFAULT '{}'::jsonb,
    is_replied BOOLEAN DEFAULT FALSE,
    admin_reply TEXT,
    ai_summary TEXT,
    ai_sentiment VARCHAR(50),
    ai_category VARCHAR(100),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_parent_feedbacks_user_id ON parent_feedbacks(telegram_user_id);
CREATE INDEX IF NOT EXISTS idx_parent_feedbacks_created_at ON parent_feedbacks(created_at DESC);
