CREATE TABLE IF NOT EXISTS qorovul_settings (
    id SERIAL PRIMARY KEY,
    username VARCHAR(150) NOT NULL DEFAULT '',
    password VARCHAR(255) NOT NULL DEFAULT '',
    access_token TEXT,
    refresh_token TEXT,
    token_expires_at TIMESTAMP,
    organization_id INTEGER,
    organization_name VARCHAR(255),
    student_type_id INTEGER,
    is_enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

ALTER TABLE students ADD COLUMN IF NOT EXISTS qorovul_person_id INTEGER;
ALTER TABLE students ADD COLUMN IF NOT EXISTS qorovul_photo_url TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_students_qorovul_person_id
    ON students (qorovul_person_id)
    WHERE is_deleted = false AND qorovul_person_id IS NOT NULL;
