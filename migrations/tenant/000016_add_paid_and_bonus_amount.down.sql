-- Down Migration: Drop paid_amount and bonus_amount columns from payment_transactions table
ALTER TABLE payment_transactions DROP COLUMN IF EXISTS paid_amount;
ALTER TABLE payment_transactions DROP COLUMN IF EXISTS bonus_amount;
