# Security Policy / 安全策略

## Reporting a vulnerability / 报告漏洞

Do not open a public issue. Use GitHub's
[private advisory form](../../security/advisories/new), or write to the
maintainers, with:

- what you can do with it (impact), and how you found it;
- the affected version or commit;
- steps to reproduce, ideally against a local environment (`make dev-up`).

We confirm receipt, agree on a fix window with you, and credit you in the
release notes unless you prefer otherwise. Please keep the details private
until a fix is released.

请不要提公开 issue。用 GitHub 的私密安全公告，或直接联系维护者，说明影响、受影响的版本或提交、复现步骤
（尽量在本地环境复现）。修复发布之前请勿公开细节。

## Supported versions / 支持的版本

Only the latest release of `main` gets fixes. There are no long-term support
branches. 只有 `main` 的最新版本会收到修复。

## What Tide already assumes / 已有的安全设计

Knowing these helps you judge whether something is a bug:

| | |
|---|---|
| Authentication | Server-side sessions; HttpOnly, SameSite=Lax cookies (Secure over HTTPS); a new session id on login. Local passwords: bcrypt cost 12, ≥ 12 bytes, no username inside, no common or repeated patterns. Failures are rate-limited per user and per IP, answered with one constant-time message, and audited. |
| Authorization | Deny by default, including visibility: services, releases and audit records are only listed within the grants a person holds, and nothing is granted to new accounts. Every endpoint declares its permission; environment permissions are additionally scoped by project and service type. The service's project and type come from the catalog — unknown ones match only unscoped grants (fail closed). Denials are audited. |
| CSRF | Non-GET requests must send `Content-Type: application/json` and an `Origin` / `Referer` matching the host. |
| Input | Whitelist validation on every field, parameterised SQL, escaped `LIKE`, unknown JSON fields rejected, 1 MiB body limit. |
| Output | CSP, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, HSTS over HTTPS; no `dangerouslySetInnerHTML`; only `http(s)` external links. |
| Secrets | Upstream tokens and SSO secrets are encrypted with `TIDE_SECRETS_KEY`, masked in API responses, never logged. Only administrators can configure or test upstreams (SSRF: `http(s)` only). |
| Audit | Append-only, enforced by database grants: the runtime role has INSERT and SELECT on `audit_log` and nothing else. |
| Cluster access | None. Tide holds no kubeconfig and mounts no service account token; it only calls the Kargo, Argo CD and registry APIs with the tokens an administrator configured. |
| Metrics | `/metrics` is opt-in and can require a bearer token. |

## Out of scope / 不属于漏洞

- Anything an administrator can already do through the admin console
  (configuring upstreams, granting permissions, disabling approval).
- The permissions of the Argo CD and Kargo accounts you give Tide: scope them
  per project, see `deploy/prod/README.md`.
- Losing `TIDE_SECRETS_KEY` — stored credentials then become unreadable by
  design.
