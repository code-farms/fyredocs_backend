-- Backfill owner and plan on job.completed / job.failed rows from their
-- job.created row. Workers did not echo the job owner on terminal events, so
-- these rows landed with user_id NULL, is_guest false (even for guest jobs)
-- and an empty plan_name. The subscriber now fills them at write time
-- (subscriber.applyJobOrigin); this repairs rows written before that.
--
-- Only rows still missing an owner or plan are touched, so a re-run is a no-op.

-- +goose Up
UPDATE public.analytics_events AS c
SET user_id   = COALESCE(c.user_id, j.user_id),
    is_guest  = CASE WHEN c.user_id IS NULL THEN j.is_guest ELSE c.is_guest END,
    plan_name = CASE WHEN COALESCE(c.plan_name, '') = '' THEN j.plan_name ELSE c.plan_name END
FROM public.analytics_events AS j
WHERE c.event_type IN ('job.completed', 'job.failed')
  AND j.event_type = 'job.created'
  AND j.job_id = c.job_id
  AND (c.user_id IS NULL OR COALESCE(c.plan_name, '') = '');

-- +goose Down
-- Data backfill: the pre-repair values were wrong, so there is nothing to restore.
