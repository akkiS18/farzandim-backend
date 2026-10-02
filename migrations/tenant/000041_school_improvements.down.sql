DROP TABLE IF EXISTS announcement_uploads;
ALTER TABLE announcements DROP COLUMN IF EXISTS image_urls;
ALTER TABLE users DROP COLUMN IF EXISTS primary_subject_id;
