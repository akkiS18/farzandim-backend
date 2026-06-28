CREATE TABLE IF NOT EXISTS class_schedules (
    id SERIAL PRIMARY KEY,
    class_id INTEGER NOT NULL REFERENCES classes(id),
    day_of_week INTEGER NOT NULL CHECK (day_of_week BETWEEN 1 AND 6), -- 1 = Monday, 6 = Saturday
    lesson_number INTEGER NOT NULL CHECK (lesson_number BETWEEN 1 AND 10), -- 1-10th hour
    subject_id INTEGER NOT NULL REFERENCES subjects(id),
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (class_id, day_of_week, lesson_number)
);
