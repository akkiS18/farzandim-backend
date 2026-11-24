DROP INDEX IF EXISTS idx_students_qorovul_person_id;
ALTER TABLE students DROP COLUMN IF EXISTS qorovul_photo_url;
ALTER TABLE students DROP COLUMN IF EXISTS qorovul_person_id;
DROP TABLE IF EXISTS qorovul_settings;
