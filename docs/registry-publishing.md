# Forgejo container publishing failures

The image workflow separates **Build Docker image**, registry login, and **Publish Docker image to Forgejo**. Publication reuses the same Buildx layer cache and preserves provenance. A successful build followed by a rejected registry upload is a publishing/infrastructure failure, not a compilation failure. Application changes cannot establish which upstream rule denied a request.

## HTML 403 while completing a blob upload

For a failure such as `PUT https://forgejo.jwala.net/v2/bheem/calnode/blobs/uploads/...?...digest=sha256...`:

1. Match the request time, method, path, runner source IP and any `cf-ray` value against Cloudflare **Security Events** and the origin proxy's access/error logs. The Cloudflare analytics beacon in an HTML error page alone does **not** prove a WAF rule blocked it; an origin-generated error may also pass through Cloudflare. Consult [Cloudflare's 403 guidance](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/4xx-client-error/error-403/).
2. Check Forgejo's logs and the registry token. `REGISTRY_TOKEN` must belong to the username used by the login step (`github.actor`) and have package-write access to the image owner's namespace. A successful login proves credentials were accepted, not that every repository operation is authorized. See [Forgejo's container registry documentation](https://forgejo.org/docs/latest/user/packages/container/).
3. Ensure the registry proxy handles OCI upload methods (`POST`, `PATCH`, `PUT`, `DELETE`, `GET`, `HEAD`) as intended. Preserve the upload URL's path, query parameters, `Location` header and authorization header. A method allowlist, external authentication gateway, or WAF can reject the final `PUT` even after the earlier upload succeeds. Compare the registry configuration with [Forgejo's reverse proxy guidance](https://forgejo.org/docs/latest/admin/setup/reverse-proxy/).
4. Correct the specific identified permission or proxy rule, then rerun publishing. If a security-rule exception is necessary, scope it to the registry route and trusted CI source; keep registry authentication and TLS enabled. Do not disable security controls across the site or mask the job failure.

Avoid pasting tokens, Docker authorization headers, or complete authenticated upload URLs into public issue reports. Retain the HTTP status, method, sanitized path, timestamp and request correlation ID.

Neither this repository nor local build checks can verify a fix to a remote registry/proxy without access to its logs and configuration. A real successful push is the final publication check.
