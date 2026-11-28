-- One row per job lifecycle transition. Job events are delivered at least
-- once, and the subscriber used a plain INSERT, so a redelivered event became
-- a second row and dashboards counted it twice (seen: one failed OCR job shown
-- as two). Remove existing duplicates (keeping the first one persisted), then
-- let a unique index reject future ones; the subscriber inserts with
-- ON CONFLICT DO NOTHING.

-- +goose Up
DELETE FROM public.analytics_events AS d
USING public.analytics_events AS k
WHERE d.job_id = k.job_id
  AND d.event_type = k.event_type
  AND d.event_type IN ('job.created', 'job.completed', 'job.failed')
  AND (k.persisted_at, k.id) < (d.persisted_at, d.id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_event_job_lifecycle
    ON public.analytics_events (job_id, event_type)
    WHERE job_id IS NOT NULL
      AND event_type IN ('job.created', 'job.completed', 'job.failed');

-- +goose Down
DROP INDEX IF EXISTS public.uq_event_job_lifecycle;
