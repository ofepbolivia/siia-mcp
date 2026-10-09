-- SIIASQL integration fixture (SDD §20.3).
-- Reproduces the schema and data the postgres integration suite expects:
-- tables authors/books with a PK/FK (CASCADE) and a security-off view.
-- The view author_books intentionally resolves to pg_catalog-free relations so
-- the EXPLAIN-over-view rejection path is exercised against a real material.

CREATE TABLE IF NOT EXISTS public.authors (
    id         integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL,
    birth_year integer
);

CREATE TABLE IF NOT EXISTS public.books (
    id        integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    author_id integer NOT NULL REFERENCES public.authors(id) ON DELETE CASCADE,
    title     text
);

CREATE INDEX IF NOT EXISTS idx_books_author ON public.books(author_id);

CREATE SCHEMA IF NOT EXISTS hidden;

CREATE TABLE IF NOT EXISTS hidden.secrets (
    id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS hidden.private_links (
    id        integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    author_id integer NOT NULL REFERENCES public.authors(id)
);

CREATE OR REPLACE VIEW hidden.safe_authors AS
    SELECT id, name FROM public.authors;

CREATE TABLE IF NOT EXISTS public.hidden_links (
    id        integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    secret_id integer NOT NULL REFERENCES hidden.secrets(id)
);

-- A table with a same-named, same-typed column as authors.name but NO foreign
-- key, so db_suggest_relationships can infer the logical join.
CREATE TABLE IF NOT EXISTS public.tags (
    id   integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text
);

CREATE OR REPLACE VIEW public.author_books AS
    SELECT b.author_id, b.title
    FROM public.books b;

-- Seed data (idempotent: the identity columns pin ids 1..2 for the suite).
TRUNCATE public.books, public.authors, public.hidden_links,
         hidden.secrets, hidden.private_links RESTART IDENTITY CASCADE;

INSERT INTO public.authors (name, birth_year) VALUES
    ('Ada',   NULL),
    ('Grace', NULL);

INSERT INTO public.books (author_id, title) VALUES
    (1, 'Notes G'),
    (2, 'Compiler');
