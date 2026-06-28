CREATE TABLE IF NOT EXISTS class_schedule_exceptions (
    id SERIAL PRIMARY KEY,
    class_id INTEGER NOT NULL REFERENCES classes(id),
    date DATE NOT NULL,
    lesson_number INTEGER NOT NULL CHECK (lesson_number BETWEEN 1 AND 10),
    subject_id INTEGER REFERENCES subjects(id), -- NULL means cancelled
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Partial unique index ensures only one active exception can exist per class/date/lesson slot
CREATE UNIQUE INDEX IF NOT EXISTS class_schedule_exceptions_class_id_date_lesson_number_key 
ON class_schedule_exceptions (class_id, date, lesson_number) 
WHERE is_deleted = false;
