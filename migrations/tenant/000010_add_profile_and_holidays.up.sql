-- Up Migration: Add profile expansions and create school_holidays table
ALTER TABLE users ADD COLUMN IF NOT EXISTS passport VARCHAR(255);

ALTER TABLE students ADD COLUMN IF NOT EXISTS address TEXT;
ALTER TABLE students ADD COLUMN IF NOT EXISTS birthdate DATE;
ALTER TABLE students ADD COLUMN IF NOT EXISTS ina VARCHAR(255);

CREATE TABLE IF NOT EXISTS school_holidays (
    id SERIAL PRIMARY KEY,
    holiday_date DATE UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
