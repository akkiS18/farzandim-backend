CREATE TABLE IF NOT EXISTS lesson_plans (
    id SERIAL PRIMARY KEY,
    teacher_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    class_id INT NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
    subject_id INT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    day_of_week INT NOT NULL CHECK (day_of_week BETWEEN 1 AND 7),
    lesson_number INT NOT NULL CHECK (lesson_number >= 1),
    start_date DATE NOT NULL,
    topic_name TEXT NOT NULL,
    notes TEXT DEFAULT '',
    is_deleted BOOLEAN DEFAULT FALSE,
    deleted_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_lesson_plans_teacher_id ON lesson_plans(teacher_id);
CREATE INDEX IF NOT EXISTS idx_lesson_plans_class_id ON lesson_plans(class_id);
CREATE INDEX IF NOT EXISTS idx_lesson_plans_subject_id ON lesson_plans(subject_id);
CREATE INDEX IF NOT EXISTS idx_lesson_plans_start_date ON lesson_plans(start_date);
CREATE INDEX IF NOT EXISTS idx_lesson_plans_is_deleted ON lesson_plans(is_deleted);
