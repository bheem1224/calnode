-- +goose Up
CREATE TABLE identity_providers (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('google','microsoft','oidc')),
 purpose TEXT NOT NULL CHECK(purpose IN ('sso','both','integrations')),
 enabled INTEGER NOT NULL DEFAULT 1,
 allow_members INTEGER NOT NULL DEFAULT 1,
 client_id TEXT NOT NULL,
 client_secret_enc TEXT NOT NULL DEFAULT '',
 microsoft_tenant_id TEXT NOT NULL DEFAULT '',
 issuer_url TEXT NOT NULL DEFAULT '',
 pkce INTEGER NOT NULL DEFAULT 1,
 auto_provision INTEGER NOT NULL DEFAULT 0,
 domains_json TEXT NOT NULL DEFAULT '[]',
 group_ids_json TEXT NOT NULL DEFAULT '[]',
 group_match TEXT NOT NULL DEFAULT 'any' CHECK(group_match IN ('any','all')),
 groups_claim TEXT NOT NULL DEFAULT 'groups',
 legacy_key TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX identity_providers_legacy ON identity_providers(legacy_key) WHERE legacy_key<>'';
CREATE TABLE user_identities (
 provider_id TEXT NOT NULL REFERENCES identity_providers(id),
 issuer TEXT NOT NULL,
 subject TEXT NOT NULL,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 PRIMARY KEY(provider_id,issuer,subject),
 UNIQUE(provider_id,user_id)
);
ALTER TABLE users ADD COLUMN enrollment_provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN legacy_identity_eligible INTEGER NOT NULL DEFAULT 0;
UPDATE users SET legacy_identity_eligible=1;
ALTER TABLE calendar_connections ADD COLUMN oauth_provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN identity_providers_initialized INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN identity_providers_initialized;
ALTER TABLE calendar_connections DROP COLUMN oauth_provider_id;
ALTER TABLE users DROP COLUMN enrollment_provider_id;
ALTER TABLE users DROP COLUMN legacy_identity_eligible;
DROP TABLE user_identities;
DROP INDEX identity_providers_legacy;
DROP TABLE identity_providers;
