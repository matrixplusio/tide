# Contributing

Thanks for looking. A few things are worth knowing before you spend time on a
change.

## What this project is careful about

Tide decides what goes to production and keeps the record of who decided it.
Two properties are not negotiable, and a change that weakens either will be
turned down however good the rest of it is:

- **The audit trail is append-only.** It is a PostgreSQL grant, not a
  convention: the runtime role has `INSERT` and `SELECT` on `audit_log` and
  nothing else. This is why Tide needs two database roles.
- **Nothing is released without somebody saying so**, except where an
  administrator has explicitly turned that on for one environment. An
  automatic release is recorded as automatic and never attributed to a person.

`CONVENTIONS.md` is the engineering standard: envelope, error codes, validation,
logging, permissions, the frontend rules. It is short, and most review
comments are already answered there.

## Before you open a pull request

```bash
make check          # go vet, golangci-lint, tests, frontend typecheck/lint/test/build
```

`make check` includes `make test-db`, which needs a real PostgreSQL and Redis;
see [docs/development.md](docs/development.md) for a local one. CI runs the
same gates, so a pull request that has not run them will simply fail there.

- **A bug fix comes with a regression test.** Write it first and watch it fail
  — a test that passes before the fix is not testing the fix.
- **A new rule that a machine can check should be checked by a machine.** Some
  are lint rules (`forbidigo`), some are tests that read the source. See the
  table in `CONVENTIONS.md` §9.
- Commit subjects follow [Conventional Commits](https://www.conventionalcommits.org/);
  the body says **why**, not what — the diff already says what.

## Branches

`main` only takes merges from `dev`. Work on `feat/*`, `fix/*`, `refactor/*`,
`chore/*` or `docs/*` and open the pull request against `dev`.

## Reporting a security issue

Not here — see [SECURITY.md](SECURITY.md).

