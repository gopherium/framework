-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE SCHEMA IF NOT EXISTS gonsole;

CREATE TABLE gonsole.records (
    id uuid PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now(),
    actor text NOT NULL,
    account_id uuid,
    command text NOT NULL,
    args jsonb NOT NULL,
    flags jsonb NOT NULL
);

CREATE INDEX records_applied_at ON gonsole.records (applied_at DESC);

-- +goose Down
DROP TABLE gonsole.records;
