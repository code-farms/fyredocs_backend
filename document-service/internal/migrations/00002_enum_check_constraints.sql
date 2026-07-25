-- Enforce the status enums in the database, not just in Go.
--
-- documents.status and exports.status are free-text columns whose values come
-- from a fixed set in the models (internal/models/document.go, export.go). Other
-- services already CHECK their equivalents (users.role, processing_jobs.status);
-- this closes the inconsistency so a bad write fails loudly at the DB instead of
-- silently producing a row no code path can handle.
--
-- NOT VALID skips the scan of existing rows, then VALIDATE runs it without an
-- ACCESS EXCLUSIVE lock — safe on a live table. If validation fails, an existing
-- row holds an unexpected status: fix the data, then re-run.

-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.documents
        ADD CONSTRAINT chk_document_status
        CHECK (status IN ('uploaded','processing','ready','failed')) NOT VALID;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.documents VALIDATE CONSTRAINT chk_document_status;
EXCEPTION WHEN undefined_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.exports
        ADD CONSTRAINT chk_export_status
        CHECK (status IN ('queued','processing','ready','failed')) NOT VALID;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.exports VALIDATE CONSTRAINT chk_export_status;
EXCEPTION WHEN undefined_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.documents DROP CONSTRAINT IF EXISTS chk_document_status;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.exports DROP CONSTRAINT IF EXISTS chk_export_status;
-- +goose StatementEnd
