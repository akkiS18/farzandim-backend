ALTER TABLE class_schedules DROP CONSTRAINT IF EXISTS class_schedules_class_id_day_of_week_lesson_number_start_date_key;
ALTER TABLE class_schedules ADD CONSTRAINT class_schedules_class_id_day_of_week_lesson_number_key UNIQUE (class_id, day_of_week, lesson_number);
ALTER TABLE class_schedules DROP COLUMN IF EXISTS start_date;
ALTER TABLE class_schedules DROP COLUMN IF EXISTS end_date;
