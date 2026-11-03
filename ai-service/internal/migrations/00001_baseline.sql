-- Baseline schema for ai-service.
--
-- ai_usage is the per-subject monthly token ledger the quota gate reads and
-- writes. subject is "user:<id>" today; the column is text, not a uuid, so
-- per-org quota ("org:<id>") needs no schema change once the gateway carries
-- an org identity. period is the UTC month as YYYYMM.

-- +goose Up
CREATE TABLE IF NOT EXISTS public.ai_usage (
    subject text NOT NULL,
    period integer NOT NULL,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    cached_tokens bigint NOT NULL DEFAULT 0,
    reserved_tokens bigint NOT NULL DEFAULT 0,
    updated_at timestamp with time zone NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ai_usage_pkey PRIMARY KEY (subject, period)
);

-- +goose Down
DROP TABLE IF EXISTS public.ai_usage;
