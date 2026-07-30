-- Share links (Phase 6): expiring public download links for user files.
--
-- A share owns its object in the outputs bucket (shares/{id}/... keys) —
-- unlike job outputs, the bytes are copied out of the upload lifecycle at
-- creation and swept by the cleanup pass when the share expires or is
-- revoked. Only the SHA-256 hash of the redeem token is stored; the token
-- itself is returned once at creation.

-- +goose Up
CREATE TABLE IF NOT EXISTS public.shares (
    id uuid NOT NULL,
    token_hash text NOT NULL,
    object_key text NOT NULL,
    file_name text NOT NULL,
    content_type text,
    file_size bigint NOT NULL DEFAULT 0,
    user_id uuid,
    expires_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    download_count bigint NOT NULL DEFAULT 0,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE ONLY public.shares DROP CONSTRAINT IF EXISTS shares_pkey;
ALTER TABLE ONLY public.shares ADD CONSTRAINT shares_pkey PRIMARY KEY (id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_shares_token_hash ON public.shares USING btree (token_hash);
CREATE INDEX IF NOT EXISTS idx_shares_expires_at ON public.shares USING btree (expires_at);
CREATE INDEX IF NOT EXISTS idx_shares_user_id ON public.shares USING btree (user_id);

-- +goose Down
DROP TABLE IF EXISTS public.shares;
