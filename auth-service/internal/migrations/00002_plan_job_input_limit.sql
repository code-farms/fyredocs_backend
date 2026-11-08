-- Plan limits live in this table and nowhere else (decision 2026-11-08, see
-- docs/developer/services/auth-service.md "Plan limits"). This migration adds
-- the per-job total and brings existing rows to the values workers can honour:
-- every PDF worker rejects a job whose total input × 3 exceeds its 900 MB tmpfs
-- budget (*/internal/worker/tmpfs.go), so no plan may promise more than 300 MB
-- per job. seedPlans (internal/models/database.go) is insert-only, so these
-- UPDATEs are what changes an existing database; a fresh one gets the same
-- values from the seed.

-- +goose Up
ALTER TABLE public.subscription_plans
    ADD COLUMN IF NOT EXISTS max_job_input_mb bigint NOT NULL DEFAULT 300;

UPDATE public.subscription_plans SET max_file_size_mb = 300 WHERE name = 'pro';
UPDATE public.subscription_plans SET max_job_input_mb = 100 WHERE name = 'guest';

-- +goose Down
UPDATE public.subscription_plans SET max_file_size_mb = 500 WHERE name = 'pro';

ALTER TABLE public.subscription_plans DROP COLUMN IF EXISTS max_job_input_mb;
