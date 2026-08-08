-- Up Migration: Add paid_amount and bonus_amount columns to payment_transactions table
ALTER TABLE payment_transactions ADD COLUMN IF NOT EXISTS paid_amount NUMERIC(12, 2) NOT NULL DEFAULT 0.00;
ALTER TABLE payment_transactions ADD COLUMN IF NOT EXISTS bonus_amount NUMERIC(12, 2) NOT NULL DEFAULT 0.00;

-- Backfill existing PAYMENT records so paid_amount equals amount if paid_amount was 0.00
UPDATE payment_transactions SET paid_amount = amount WHERE type = 'PAYMENT' AND paid_amount = 0.00;
