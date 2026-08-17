-- Database Index Optimization for High Concurrency & Latency Reduction
CREATE INDEX IF NOT EXISTS idx_users_phone ON users(phone) WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_students_user_id ON students(user_id) WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_students_class_id ON students(class_id) WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_student_parents_student ON student_parents(student_id);
CREATE INDEX IF NOT EXISTS idx_student_parents_parent ON student_parents(parent_id);
CREATE INDEX IF NOT EXISTS idx_grades_student_id ON grades(student_id) WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_grades_created_at ON grades(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_grade_comments_grade ON grade_comments(grade_id);
CREATE INDEX IF NOT EXISTS idx_grade_comments_parent ON grade_comments(parent_id);
CREATE INDEX IF NOT EXISTS idx_announcements_created ON announcements(created_at DESC) WHERE is_deleted = false;
CREATE INDEX IF NOT EXISTS idx_class_teachers_class_teacher ON class_teachers(class_id, teacher_id) WHERE is_deleted = false;

