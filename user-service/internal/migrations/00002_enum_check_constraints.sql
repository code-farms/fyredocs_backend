-- Enforce the membership-role and plan-name enums in the database.
--
-- memberships.role drives every authorization decision in document-service
-- (RoleAtLeast); a value outside the known set silently ranks 0 and denies
-- everything, which is a confusing failure mode to debug from the app side.
-- organizations.plan_name mirrors auth-service's plan names.
--
-- NOT VALID + VALIDATE avoids an ACCESS EXCLUSIVE table scan on a live table.

-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.memberships
        ADD CONSTRAINT chk_membership_role
        CHECK (role IN ('owner','admin','editor','viewer')) NOT VALID;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.memberships VALIDATE CONSTRAINT chk_membership_role;
EXCEPTION WHEN undefined_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.organizations
        ADD CONSTRAINT chk_org_plan_name
        CHECK (plan_name IN ('guest','free','pro')) NOT VALID;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.organizations VALIDATE CONSTRAINT chk_org_plan_name;
EXCEPTION WHEN undefined_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.memberships DROP CONSTRAINT IF EXISTS chk_membership_role;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.organizations DROP CONSTRAINT IF EXISTS chk_org_plan_name;
-- +goose StatementEnd
