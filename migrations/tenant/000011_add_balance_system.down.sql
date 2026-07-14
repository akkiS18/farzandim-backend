-- Down Migration: Remove balance column and drop payment_transactions table
DROP TABLE IF EXISTS payment_transactions;

ALTER TABLE students DROP COLUMN IF EXISTS balance;
