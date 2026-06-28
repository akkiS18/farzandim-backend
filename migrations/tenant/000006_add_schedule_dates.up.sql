ALTER TABLE class_schedules ADD COLUMN start_date DATE;
ALTER TABLE class_schedules ADD COLUMN end_date DATE;

-- Update existing records to have a default school year range
UPDATE class_schedules SET start_date = '2026-09-01', end_date = '2027-05-31' WHERE start_date IS NULL;

-- Make columns NOT NULL after seeding default dates
ALTER TABLE class_schedules ALTER COLUMN start_date SET NOT NULL;
ALTER TABLE class_schedules ALTER COLUMN end_date SET NOT NULL;

-- Drop old unique constraint
ALTER TABLE class_schedules DROP CONSTRAINT IF EXISTS class_schedules_class_id_day_of_week_lesson_number_key;

-- Add new unique constraint scoped with start_date
ALTER TABLE class_schedules ADD CONSTRAINT class_schedules_class_id_day_of_week_lesson_number_start_date_key UNIQUE (class_id, day_of_week, lesson_number, start_date);
