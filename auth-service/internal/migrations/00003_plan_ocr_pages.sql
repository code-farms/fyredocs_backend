-- Per-plan cap on the pages one OCR job may have (decision 2026-11-27). The
-- 300-page OCR gate on the 6 vCPU / 12 GB VPS (2026-11-26) ran into the worker's
-- fixed 10-minute deadline after ~17 pages and failed with a vague timeout; the
-- optimize-pdf worker now rejects a scan above this limit before it starts, with
-- a message naming the limit. seedPlans (internal/models/database.go) is
-- insert-only, so the UPDATEs are what change an existing database.

-- +goose Up
ALTER TABLE public.subscription_plans
    ADD COLUMN IF NOT EXISTS max_ocr_pages bigint NOT NULL DEFAULT 30;

UPDATE public.subscription_plans SET max_ocr_pages = 10 WHERE name = 'guest';
UPDATE public.subscription_plans SET max_ocr_pages = 200 WHERE name = 'pro';

-- +goose Down
ALTER TABLE public.subscription_plans DROP COLUMN IF EXISTS max_ocr_pages;
