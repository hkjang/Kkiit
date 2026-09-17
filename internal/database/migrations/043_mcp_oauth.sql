-- MCP over SSO: /mcp accepts a Keycloak access token next to the personal key.
-- Off by default, so a fresh installation and an upgraded one behave exactly as
-- before until an administrator turns it on. The authorization server is one
-- of the OIDC rows in auth_providers (the web sign-in), named by slug; empty
-- means the only enabled OIDC provider. audience and scopes are arrays like
-- the other policy rows. scopes are key permissions, not Keycloak scopes: the
-- ceiling an SSO caller gets, intersected with the account's own roles.
INSERT INTO system_settings(key, value, description) VALUES
('mcp.oauth','{"enabled":false,"provider":"","resource":"","audience":[],"scopes":["mcp.use"]}'::jsonb,'MCP SSO(OAuth) 토큰 인증')
ON CONFLICT (key) DO NOTHING;
