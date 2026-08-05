CREATE TABLE IF NOT EXISTS target_presets (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    target_levels INT[] DEFAULT '{}',
    target_classes INT[] DEFAULT '{}',
    target_students INT[] DEFAULT '{}',
    created_by INT NULL REFERENCES users(id) ON DELETE SET NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_target_presets_is_deleted ON target_presets(is_deleted) WHERE is_deleted = false;
