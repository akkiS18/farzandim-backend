CREATE TABLE IF NOT EXISTS books (
    id SERIAL PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    author VARCHAR(255) DEFAULT '',
    description TEXT DEFAULT '',
    cover_url VARCHAR(500) DEFAULT '',
    file_url VARCHAR(500) NOT NULL,
    file_size VARCHAR(50) DEFAULT '',
    target_levels INT[] DEFAULT '{}',
    class_ids INT[] DEFAULT '{}',
    is_deleted BOOLEAN DEFAULT FALSE,
    deleted_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_books_target_levels ON books USING GIN (target_levels);
CREATE INDEX IF NOT EXISTS idx_books_is_deleted ON books (is_deleted);
