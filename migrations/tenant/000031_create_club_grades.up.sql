-- Migration 000031: Create club_grades table for extracurricular club session attendance and evaluation

CREATE TABLE IF NOT EXISTS club_grades (
    id SERIAL PRIMARY KEY,
    club_id INT NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    student_id INT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    lesson_date DATE NOT NULL,
    attendance VARCHAR(20) NOT NULL DEFAULT 'PRESENT', -- 'PRESENT', 'ABSENT', 'EXCUSED'
    score_value VARCHAR(50) DEFAULT '',                -- '5', '4', '100', etc.
    feedback TEXT DEFAULT '',
    graded_by INT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (club_id, student_id, lesson_date)
);

CREATE INDEX IF NOT EXISTS idx_club_grades_club_date ON club_grades(club_id, lesson_date);
CREATE INDEX IF NOT EXISTS idx_club_grades_student ON club_grades(student_id);
