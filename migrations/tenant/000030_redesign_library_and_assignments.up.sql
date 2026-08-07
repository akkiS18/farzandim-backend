CREATE TABLE IF NOT EXISTS book_categories (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT DEFAULT '',
    created_by INT REFERENCES users(id) ON DELETE SET NULL,
    is_deleted BOOLEAN DEFAULT FALSE,
    deleted_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_book_categories_is_deleted ON book_categories(is_deleted);

ALTER TABLE books ADD COLUMN IF NOT EXISTS category_id INT REFERENCES book_categories(id) ON DELETE SET NULL;
ALTER TABLE books ADD COLUMN IF NOT EXISTS download_link VARCHAR(1000) DEFAULT '';
ALTER TABLE books ADD COLUMN IF NOT EXISTS created_by INT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE books ADD COLUMN IF NOT EXISTS location_in_school VARCHAR(255) DEFAULT '';
ALTER TABLE books ALTER COLUMN file_url DROP NOT NULL;

CREATE TABLE IF NOT EXISTS reading_assignments (
    id SERIAL PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    teacher_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    description TEXT DEFAULT '',
    is_deleted BOOLEAN DEFAULT FALSE,
    deleted_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_reading_assignments_teacher_id ON reading_assignments(teacher_id);
CREATE INDEX IF NOT EXISTS idx_reading_assignments_dates ON reading_assignments(start_date, end_date);
CREATE INDEX IF NOT EXISTS idx_reading_assignments_is_deleted ON reading_assignments(is_deleted);

CREATE TABLE IF NOT EXISTS reading_assignment_books (
    assignment_id INT REFERENCES reading_assignments(id) ON DELETE CASCADE,
    book_id INT REFERENCES books(id) ON DELETE CASCADE,
    PRIMARY KEY (assignment_id, book_id)
);

CREATE TABLE IF NOT EXISTS student_reading_progress (
    id SERIAL PRIMARY KEY,
    assignment_id INT NOT NULL REFERENCES reading_assignments(id) ON DELETE CASCADE,
    book_id INT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    student_id INT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'assigned',
    grade_value VARCHAR(50) DEFAULT '',
    numeric_value NUMERIC(5, 2) DEFAULT NULL,
    grading_system_id INT REFERENCES grading_systems(id) ON DELETE SET NULL,
    teacher_feedback TEXT DEFAULT '',
    graded_by INT REFERENCES users(id) ON DELETE SET NULL,
    graded_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (assignment_id, book_id, student_id)
);

CREATE INDEX IF NOT EXISTS idx_student_reading_progress_student_id ON student_reading_progress(student_id);
CREATE INDEX IF NOT EXISTS idx_student_reading_progress_assignment_id ON student_reading_progress(assignment_id);
