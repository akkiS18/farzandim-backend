DROP TABLE IF EXISTS student_transfer_requests CASCADE;
DROP INDEX IF EXISTS idx_parents_unique_passport;
DROP INDEX IF EXISTS idx_staff_unique_phone;

-- Restore users_phone_key if needed
-- ALTER TABLE users ADD CONSTRAINT users_phone_key UNIQUE (phone);
