-- +goose Up
ALTER TABLE users ADD COLUMN mcp_review_required INTEGER NOT NULL DEFAULT 1;
CREATE TABLE mcp_change_requests (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 credential_id TEXT NOT NULL,
 permissions_json TEXT NOT NULL,
 operations_json TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending',
 results_json TEXT NOT NULL DEFAULT '[]',
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL
);
CREATE INDEX mcp_changes_user ON mcp_change_requests(user_id, created_at);
CREATE TABLE mcp_integration_drafts (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 configuration_json TEXT NOT NULL,
 created_at TEXT NOT NULL
);
-- Freeze the newly separated custom-times grant at each existing credential's ceiling.
UPDATE roles SET permissions_json=json_set(permissions_json,'$."event_types.custom_times"',json_extract(permissions_json,'$."event_types.links"')) WHERE json_valid(permissions_json) AND json_extract(permissions_json,'$."event_types.links"') IS NOT NULL;
UPDATE api_keys SET permissions_json=json_set(permissions_json,'$."event_types.custom_times"',json_extract(permissions_json,'$."event_types.links"')) WHERE json_valid(permissions_json) AND json_extract(permissions_json,'$."event_types.links"') IS NOT NULL;
UPDATE oauth_access_tokens SET permissions_json=json_set(permissions_json,'$."event_types.custom_times"',json_extract(permissions_json,'$."event_types.links"')) WHERE json_valid(permissions_json) AND json_extract(permissions_json,'$."event_types.links"') IS NOT NULL;
-- +goose Down
UPDATE roles SET permissions_json=json_remove(permissions_json,'$."event_types.custom_times"','$."team_event_types.read"','$."team_event_types.create"','$."team_event_types.update"','$."team_event_types.delete"','$."team_event_types.links"','$."team_event_types.custom_times"','$."logs.read"') WHERE json_valid(permissions_json);
UPDATE api_keys SET permissions_json=json_remove(permissions_json,'$."event_types.custom_times"','$."team_event_types.read"','$."team_event_types.create"','$."team_event_types.update"','$."team_event_types.delete"','$."team_event_types.links"','$."team_event_types.custom_times"','$."logs.read"') WHERE json_valid(permissions_json);
UPDATE oauth_access_tokens SET permissions_json=json_remove(permissions_json,'$."event_types.custom_times"','$."team_event_types.read"','$."team_event_types.create"','$."team_event_types.update"','$."team_event_types.delete"','$."team_event_types.links"','$."team_event_types.custom_times"','$."logs.read"') WHERE json_valid(permissions_json);
DROP TABLE mcp_integration_drafts;
DROP INDEX mcp_changes_user;
DROP TABLE mcp_change_requests;
ALTER TABLE users DROP COLUMN mcp_review_required;
