#!/usr/bin/env python3
"""Unit tests for split_baseline.py.

Run:  python3 -m unittest discover -s scripts -p 'test_*.py'

The interesting behaviour is `idempotent()` on CREATE TABLE: the baseline must
converge a table that already exists but is missing columns the models added
later. Getting that wrong is not a cosmetic bug — it crash-loops a service at
boot with `column "..." does not exist` (SQLSTATE 42703).
"""
import unittest

from split_baseline import add_column_stmts, idempotent, split_top_level, table_body

USER_SESSIONS = """CREATE TABLE public.user_sessions (
    id uuid NOT NULL,
    refresh_token_hash text,
    prev_refresh_token_hash text,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
)"""

USERS_WITH_CHECK = """CREATE TABLE public.users (
    id uuid NOT NULL,
    plan_name text DEFAULT 'free'::text NOT NULL,
    role text DEFAULT 'user'::text NOT NULL,
    CONSTRAINT chk_user_role CHECK ((role = ANY (ARRAY['user'::text, 'admin'::text, 'super-admin'::text])))
)"""


class TestCreateTable(unittest.TestCase):
    def test_create_table_is_guarded(self):
        out = idempotent(USER_SESSIONS)
        self.assertIn("CREATE TABLE IF NOT EXISTS public.user_sessions", out)
        self.assertNotIn("CREATE TABLE public.user_sessions", out)

    def test_emits_one_add_column_per_column_in_order(self):
        out = idempotent(USER_SESSIONS)
        adds = [ln for ln in out.splitlines() if ln.startswith("ALTER TABLE")]
        self.assertEqual(len(adds), 4)
        names = [ln.split("ADD COLUMN IF NOT EXISTS ")[1].split(" ")[0] for ln in adds]
        self.assertEqual(
            names, ["id", "refresh_token_hash", "prev_refresh_token_hash", "created_at"]
        )

    def test_the_regression_that_broke_auth_service(self):
        # user_sessions gained prev_refresh_token_hash after the live table was
        # created; without this ALTER the later CREATE INDEX on it fails.
        self.assertIn(
            "ALTER TABLE public.user_sessions "
            "ADD COLUMN IF NOT EXISTS prev_refresh_token_hash text;",
            idempotent(USER_SESSIONS),
        )


class TestNotNullHandling(unittest.TestCase):
    def test_not_null_without_default_is_dropped(self):
        # Adding a NOT NULL column with no default to a populated table fails.
        stmts = add_column_stmts("t", "id uuid NOT NULL")
        self.assertEqual(
            stmts, ["ALTER TABLE public.t ADD COLUMN IF NOT EXISTS id uuid;"]
        )

    def test_not_null_with_default_is_kept(self):
        # Postgres backfills from the default, so NOT NULL is safe here.
        stmts = add_column_stmts("t", "plan_name text DEFAULT 'free'::text NOT NULL")
        self.assertEqual(
            stmts,
            [
                "ALTER TABLE public.t ADD COLUMN IF NOT EXISTS "
                "plan_name text DEFAULT 'free'::text NOT NULL;"
            ],
        )

    def test_nullable_column_is_unchanged(self):
        stmts = add_column_stmts("t", "note text")
        self.assertEqual(
            stmts, ["ALTER TABLE public.t ADD COLUMN IF NOT EXISTS note text;"]
        )


class TestTableConstraintsAreSkipped(unittest.TestCase):
    def test_inline_constraint_produces_no_add_column(self):
        out = idempotent(USERS_WITH_CHECK)
        adds = [ln for ln in out.splitlines() if ln.startswith("ALTER TABLE")]
        self.assertEqual(len(adds), 3)  # id, plan_name, role — not the CONSTRAINT
        self.assertNotIn("chk_user_role", "\n".join(adds))

    def test_check_body_commas_do_not_split_columns(self):
        # ARRAY['user'::text, 'admin'::text] contains commas that are NOT
        # column separators.
        parts = split_top_level(table_body(USERS_WITH_CHECK))
        self.assertEqual(len(parts), 4)
        self.assertTrue(parts[-1].startswith("CONSTRAINT chk_user_role"))


class TestOtherBranchesUnchanged(unittest.TestCase):
    """Guard the paths this change was not meant to touch."""

    def test_index(self):
        self.assertEqual(
            idempotent("CREATE INDEX idx_a ON public.t USING btree (x)"),
            "CREATE INDEX IF NOT EXISTS idx_a ON public.t USING btree (x);",
        )

    def test_unique_index(self):
        self.assertEqual(
            idempotent("CREATE UNIQUE INDEX idx_b ON public.t USING btree (y)"),
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_b ON public.t USING btree (y);",
        )

    def test_sequence(self):
        self.assertEqual(
            idempotent("CREATE SEQUENCE public.t_id_seq"),
            "CREATE SEQUENCE IF NOT EXISTS public.t_id_seq;",
        )

    def test_add_constraint_is_wrapped_in_a_guarded_do_block(self):
        out = idempotent(
            "ALTER TABLE ONLY public.t ADD CONSTRAINT t_pkey PRIMARY KEY (id)"
        )
        self.assertTrue(out.startswith("-- +goose StatementBegin"))
        self.assertIn("conname = 't_pkey'", out)
        self.assertIn("EXCEPTION WHEN duplicate_object", out)
        self.assertNotIn("ADD COLUMN", out)


if __name__ == "__main__":
    unittest.main()
