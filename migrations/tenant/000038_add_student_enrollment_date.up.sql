-- Up Migration: Add enrollment_date column to students table
ALTER TABLE students ADD COLUMN IF NOT EXISTS enrollment_date DATE DEFAULT CURRENT_DATE;

-- Backfill existing students with their user created_at date or today's date
UPDATE students s
SET enrollment_date = COALESCE(u.created_at::date, CURRENT_DATE)
FROM users u
WHERE s.user_id = u.id AND s.enrollment_date IS NULL;
