ALTER TABLE qorovul_settings ADD COLUMN IF NOT EXISTS teacher_type_id INTEGER;
ALTER TABLE qorovul_settings ADD COLUMN IF NOT EXISTS last_synced_at TIMESTAMP;

ALTER TABLE users ADD COLUMN IF NOT EXISTS qorovul_person_id INTEGER;
ALTER TABLE users ADD COLUMN IF NOT EXISTS qorovul_photo_url TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_qorovul_person_id
    ON users (qorovul_person_id)
    WHERE is_deleted = false AND qorovul_person_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS gate_attendance_logs (
    id SERIAL PRIMARY KEY,
    student_id INTEGER REFERENCES students(id) ON DELETE CASCADE,
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    qorovul_person_id INTEGER NOT NULL,
    person_role VARCHAR(20) NOT NULL DEFAULT 'STUDENT',
    log_date DATE NOT NULL,
    incoming_time TIMESTAMP,
    outgoing_time TIMESTAMP,
    late_minutes INTEGER NOT NULL DEFAULT 0,
    early_minutes INTEGER NOT NULL DEFAULT 0,
    is_inside BOOLEAN NOT NULL DEFAULT false,
    qorovul_action_id BIGINT,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (qorovul_person_id, log_date)
);

CREATE INDEX IF NOT EXISTS idx_gate_logs_date_role ON gate_attendance_logs (log_date, person_role);
CREATE INDEX IF NOT EXISTS idx_gate_logs_student_date ON gate_attendance_logs (student_id, log_date);
CREATE INDEX IF NOT EXISTS idx_gate_logs_user_date ON gate_attendance_logs (user_id, log_date);
