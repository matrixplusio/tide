# Tide

[![CI](https://github.com/matrixplusio/tide/actions/workflows/ci.yml/badge.svg)](https://github.com/matrixplusio/tide/actions/workflows/ci.yml)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](go.mod)
[![React](https://img.shields.io/badge/React-19-61DAFB.svg)](web/package.json)

**A release console for Kargo and Argo CD: choose an artifact, confirm what it changes, get it approved, and keep a record no one can edit afterwards.**

[简体中文](README.zh-CN.md)

Tide deploys nothing itself. Argo CD applies manifests, Kargo writes the deploy
repository. Tide owns the step before that: which artifact goes to which
environment, who approved it, what it actually changed, and whether the new
pods are really running.


## Why

Writing the image tag from two places is the whole problem. If a release system
edits `kustomization.yaml` while Kargo does too, Kargo's Freight state and the
repository drift apart: Kargo thinks qa runs A while it runs B. Every promotion
decision after that is guesswork.

So in Tide, **only Kargo writes the deploy repository**; Tide asks it to, by
creating a Promotion. Tide never clones the repo, never holds Git credentials,
never parses kustomize. It also never talks to a Kubernetes API — everything
goes through the Kargo, Argo CD and registry APIs.

## What it does

- **Upgrade** — pick an artifact Kargo offers for the stage, pinned by digest.
- **Config sync** — apply what a merge request changed in the deploy repo
  besides the image, after showing the exact Argo CD diff; restarts the
  workloads when only a fixed-name ConfigMap or Secret changed.
- **Restart** — roll the workloads at the current version, for config a service
  reads at startup.
- **Batch** — several services of one project in one release; same sequence runs
  in parallel, a higher sequence waits for the lower ones. Paste a list of
  `service tag` lines and Tide resolves them.
- **A ten-second confirmation** — the checklist shows current → target with
  digests and flags rollbacks, version jumps, artifacts that soaked too briefly
  upstream, and configuration that would go out with the image. Environments can
  turn any of these into a hard block.
- **Approval inside Tide** — rules per environment, project and service type;
  any one / at least N / all approvers, with a timeout. The creator cannot
  approve their own release.
- **Cross-site gate** — when qa and uat live in different Kargo instances, Tide
  only offers (and only accepts) images whose digest was verified in the source
  environment, and re-checks that before execution.
- **Done means running** — a release succeeds when every new pod is ready and
  the old ones are gone, not when the request was accepted.
- **Audit** — every action, in an append-only table enforced by database grants.
- **Notifications** — Lark / Teams / generic webhook: one card when a release is
  submitted, one when it ends.


## Get it

```bash
# Image (linux/amd64 · linux/arm64)
docker pull ghcr.io/matrixplusio/tide:v0.0.1-beta.1

# Or a binary — the frontend is embedded in it
curl -fsSLO https://github.com/matrixplusio/tide/releases/download/v0.0.1-beta.1/tide_v0.0.1-beta.1_linux_amd64
chmod +x tide_v0.0.1-beta.1_linux_amd64 && ./tide_v0.0.1-beta.1_linux_amd64 version
```

Every artifact carries a signed provenance attestation, verifiable with nothing
configured:

```bash
gh attestation verify --repo matrixplusio/tide oci://ghcr.io/matrixplusio/tide:v0.0.1-beta.1
gh attestation verify --repo matrixplusio/tide tide_v0.0.1-beta.1_linux_amd64
```

The current `main` is `ghcr.io/matrixplusio/tide:main`. It never moves `latest`,
which belongs to releases.

## Quick start (local)

Needs Docker Desktop with Kubernetes, `kubectl`, Go 1.26, Node 22 and pnpm,
and something for Tide to talk to: an Argo CD, a Kargo and an image registry.
[docs/development.md](docs/development.md) walks through a throwaway set of
them in the same cluster.

```bash
cp local.mk.example local.mk    # your cluster's hostname, CA and dev passwords
make dev-up                     # Tide in the local cluster, live reload (air / Vite HMR)
open https://tide.example.test  # whatever DEV_DOMAIN you set
```

The first visit runs the setup wizard: it prints a token to the pod log, you
create the first administrator, then connect the upstreams in the settings.

## Production

```bash
docker build -t tide .
# Put the passwords you want into the two Secrets in deploy/prod/tide.yaml,
# then let Tide print the SQL that creates the database and roles with them:
kubectl apply -f deploy/prod/bootstrap-sql.yaml
kubectl -n tide logs job/tide-bootstrap-sql    # run it as a superuser, once
kubectl apply -k deploy/prod                   # replace every CHANGE_ME first
```

One database, two roles: the server connects as `tide_app`, which the
migrations grant INSERT and SELECT — and nothing else — on `audit_log`. A
table's owner can grant itself UPDATE whenever it likes, so a single role
would make "a record no one can edit afterwards" a promise in code rather
than something PostgreSQL enforces.

[deploy/prod/README.md](deploy/prod/README.md) covers the database bootstrap,
secrets, the Argo CD and Kargo accounts (with RBAC), monitoring, backups,
upgrades and a go-live checklist.

## Documentation

| | |
|---|---|
| [Product](docs/product.md) | Boundaries, flow, the two safety nets, release model |
| [Architecture](docs/architecture.md) | Data sources, data model, executor, observability, permissions, Kargo pitfalls |
| [API](docs/api.md) | Envelope, error codes, every endpoint |
| [Deployment](deploy/prod/README.md) | Production runbook |
| [Designs](docs/designs/) | Multi-site GitOps, admin console, Lark ChatOps |

## Security

- Sessions are server-side, cookies are HttpOnly and SameSite; passwords are
  bcrypt with a strength policy, throttling and audited failures.
- Every endpoint declares the permission it needs; permissions — including the
  three view permissions — are scoped by environment, project and service type.
  Nothing is granted by default: a new account sees no service, no release and
  no audit record until an administrator grants a role. Denials are audited.
- Non-GET requests require a JSON content type and a same-origin `Origin`.
- Upstream tokens and SSO secrets are encrypted with `TIDE_SECRETS_KEY`, masked
  in responses, and never logged.
- The audit table is append-only at the database level: the runtime role has
  only INSERT and SELECT.
- Tide holds no Kubernetes credentials and mounts no service account token.

Reporting a vulnerability: [SECURITY.md](SECURITY.md).

## Tech stack

Go (gin, gorm with hand-written SQL, zap, viper) · React 19 (TypeScript,
TanStack Query, react-hook-form, zod, no Tailwind) · PostgreSQL · single
binary with the web build embedded.

Engineering rules — logging, API envelope, validation, permissions, tests — are
in [CONVENTIONS.md](CONVENTIONS.md); they are part of the review checklist.

## License

Copyright (c) 2026 MatrixPlus.

[GNU AGPL v3.0](LICENSE). Running a modified version as a network service means
offering its source to users of that service.
