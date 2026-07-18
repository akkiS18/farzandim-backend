-- Down Migration: Revert menu_intervals changes
ALTER TABLE menu_cycles DROP CONSTRAINT IF EXISTS menu_cycles_interval_week_day_unique;
ALTER TABLE menu_cycles DROP COLUMN IF EXISTS interval_id;
ALTER TABLE menu_cycles ADD CONSTRAINT menu_cycles_week_number_day_of_week_key UNIQUE (week_number, day_of_week);
DROP TABLE IF EXISTS menu_intervals CASCADE;
