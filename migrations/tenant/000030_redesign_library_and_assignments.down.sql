DROP TABLE IF EXISTS student_reading_progress CASCADE;
DROP TABLE IF EXISTS reading_assignment_books CASCADE;
DROP TABLE IF EXISTS reading_assignments CASCADE;
ALTER TABLE books DROP COLUMN IF EXISTS category_id;
ALTER TABLE books DROP COLUMN IF EXISTS download_link;
ALTER TABLE books DROP COLUMN IF EXISTS created_by;
DROP TABLE IF EXISTS book_categories CASCADE;
