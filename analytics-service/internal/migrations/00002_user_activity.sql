-- User activity audit trail: one row per meaningful user action, written by
-- the client batch-ingest endpoint (web/mobile) and by the NATS subscriber for
-- server-origin security events clients cannot observe.
--
-- Kept separate from analytics_events on purpose: that table is lossy
-- fire-and-forget product metrics, this one carries idempotent client writes
-- (client_event_id) and its own retention. Rows are evidence, not state — no
-- UPDATE/DELETE code path exists outside the retention sweep.

-- +goose Up
CREATE TABLE IF NOT EXISTS public.user_activity (
    id uuid NOT NULL,
    client_event_id text,
    user_id uuid,
    is_guest boolean NOT NULL DEFAULT false,
    session_id text,
    event_type text NOT NULL,
    feature_id text,
    tool_id text,
    action text,
    entity_type text,
    entity_id text,
    document_id text,
    status text NOT NULL DEFAULT 'success',
    failure_reason text,
    error_code text,
    old_value jsonb,
    new_value jsonb,
    metadata jsonb,
    platform text,
    device_type text,
    app_version text,
    correlation_id text,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE ONLY public.user_activity DROP CONSTRAINT IF EXISTS user_activity_pkey;
ALTER TABLE ONLY public.user_activity ADD CONSTRAINT user_activity_pkey PRIMARY KEY (id);

-- Partial unique index (not a UNIQUE constraint): server-origin rows have no
-- client_event_id, and a plain unique column would let only one NULL-free
-- dedupe domain exist. ON CONFLICT in the ingest handler targets this index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_ua_client_event ON public.user_activity USING btree (client_event_id) WHERE (client_event_id IS NOT NULL);

CREATE INDEX IF NOT EXISTS idx_ua_user_created ON public.user_activity USING btree (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ua_document_created ON public.user_activity USING btree (document_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ua_event_created ON public.user_activity USING btree (event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ua_session ON public.user_activity USING btree (session_id);
CREATE INDEX IF NOT EXISTS idx_ua_correlation ON public.user_activity USING btree (correlation_id);

-- +goose Down
DROP TABLE IF EXISTS public.user_activity;
