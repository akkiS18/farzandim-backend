DROP TABLE IF EXISTS charge_logs;
DROP TABLE IF EXISTS charge_plan_students;
DROP TABLE IF EXISTS charge_plan_classes;
DROP TABLE IF EXISTS charge_plan_levels;
DROP TABLE IF EXISTS charge_plans;

ALTER TABLE classes DROP COLUMN IF EXISTS level;
