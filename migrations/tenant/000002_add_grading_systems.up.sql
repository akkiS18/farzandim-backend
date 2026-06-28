CREATE TABLE IF NOT EXISTS grading_systems (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    type VARCHAR(50) NOT NULL,
    min_value NUMERIC(5,2),
    max_value NUMERIC(5,2),
    options JSONB,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Seed default grading systems
INSERT INTO grading_systems (name, type, min_value, max_value, options, is_active) VALUES
('5 ballik sistema', 'NUMERIC', 1.00, 5.00, NULL, TRUE),
('100 ballik sistema', 'NUMERIC', 0.00, 100.00, NULL, FALSE)
ON CONFLICT (name) DO NOTHING;

-- Modify grades table
ALTER TABLE grades ALTER COLUMN value TYPE VARCHAR(50) USING value::VARCHAR;
ALTER TABLE grades ADD COLUMN numeric_value NUMERIC(5,2);
