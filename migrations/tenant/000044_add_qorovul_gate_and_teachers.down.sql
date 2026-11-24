DROP INDEX IF EXISTS idx_gate_logs_user_date;
DROP INDEX IF EXISTS idx_gate_logs_student_date;
DROP INDEX IF EXISTS idx_gate_logs_date_role;
DROP TABLE IF EXISTS gate_attendance_logs;
DROP INDEX IF EXISTS idx_users_qorovul_person_id;
ALTER TABLE users DROP COLUMN IF EXISTS qorovul_photo_url;
ALTER TABLE users DROP COLUMN IF EXISTS qorovul_person_id;
ALTER TABLE qorovul_settings DROP COLUMN IF EXISTS last_synced_at;
ALTER TABLE qorovul_settings DROP COLUMN IF EXISTS teacher_type_id;
