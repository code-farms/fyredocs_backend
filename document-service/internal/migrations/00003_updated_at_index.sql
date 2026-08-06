-- Delta-sync support (Phase 9): the updatedSince filter and sort=updated
-- ordering walk (user_id, updated_at) — without this index every sync poll
-- is a per-user sequential scan.

-- +goose Up
CREATE INDEX IF NOT EXISTS idx_documents_user_updated ON public.documents USING btree (user_id, updated_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_documents_user_updated;
