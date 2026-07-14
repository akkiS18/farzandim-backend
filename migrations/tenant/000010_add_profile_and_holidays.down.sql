-- Down Migration: Remove profile expansions and drop school_holidays table
DROP TABLE IF EXISTS school_holidays;

ALTER TABLE students DROP COLUMN IF EXISTS ina;
ALTER TABLE students DROP COLUMN IF EXISTS birthdate;
ALTER TABLE students DROP COLUMN IF EXISTS address;

ALTER TABLE users DROP COLUMN IF EXISTS passport;
