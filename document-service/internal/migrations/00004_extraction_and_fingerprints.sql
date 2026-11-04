-- AI document understanding + duplicate detection (web roadmap D1).
--
-- doc_type / extraction hold the ai-service extract result a client saved for
-- the document (receipt totals, invoice fields, tables…), so the web can export
-- every receipt as CSV. ID documents keep doc_type only: their extraction is
-- refused at the handler (ai-service's PII posture — ID values are never
-- persisted server-side).
--
-- content_sha256 is computed BY THIS SERVICE from the stored object (never
-- taken from a client) for exact duplicates; simhash is the client-computed
-- 64-bit text fingerprint (fyredocs_app/src/lib/dedupe/simhash.ts) for
-- near-duplicates — re-scans never share bytes, but their words do.
--
-- Constraints are added NOT VALID then validated, like 00002, so the migration
-- never holds an ACCESS EXCLUSIVE lock while scanning a live table.

-- +goose Up
ALTER TABLE public.documents
    ADD COLUMN IF NOT EXISTS doc_type text,
    ADD COLUMN IF NOT EXISTS extraction jsonb,
    ADD COLUMN IF NOT EXISTS content_sha256 text,
    ADD COLUMN IF NOT EXISTS simhash bigint;

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.documents
        ADD CONSTRAINT chk_document_doc_type
        CHECK (doc_type IS NULL OR doc_type IN
            ('invoice','receipt','id_document','business_card','form','letter','contract','statement','other')) NOT VALID;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.documents
        ADD CONSTRAINT chk_document_content_sha256
        CHECK (content_sha256 IS NULL OR content_sha256 ~ '^[0-9a-f]{64}$') NOT VALID;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.documents VALIDATE CONSTRAINT chk_document_doc_type;
    ALTER TABLE public.documents VALIDATE CONSTRAINT chk_document_content_sha256;
EXCEPTION WHEN undefined_object THEN NULL;
END $$;
-- +goose StatementEnd

-- The receipts list (docType filter) walks (user_id, doc_type) for live rows.
CREATE INDEX IF NOT EXISTS idx_documents_user_doc_type
    ON public.documents USING btree (user_id, doc_type)
    WHERE doc_type IS NOT NULL AND deleted_at IS NULL;

-- Exact-duplicate lookup is an equality probe on the hash.
CREATE INDEX IF NOT EXISTS idx_documents_content_sha256
    ON public.documents USING btree (content_sha256)
    WHERE content_sha256 IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_documents_content_sha256;
DROP INDEX IF EXISTS idx_documents_user_doc_type;
ALTER TABLE public.documents DROP CONSTRAINT IF EXISTS chk_document_content_sha256;
ALTER TABLE public.documents DROP CONSTRAINT IF EXISTS chk_document_doc_type;
ALTER TABLE public.documents
    DROP COLUMN IF EXISTS simhash,
    DROP COLUMN IF EXISTS content_sha256,
    DROP COLUMN IF EXISTS extraction,
    DROP COLUMN IF EXISTS doc_type;
