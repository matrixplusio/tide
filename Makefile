# Local settings live in local.mk, which is not in the repository: the
# hostname, the CA secret and the passwords below are whatever your own
# cluster happens to use. Copy local.mk.example to local.mk and edit it.
-include local.mk

KCTX ?= docker-desktop
K := kubectl --context $(KCTX)
NS ?= tide-dev
STAGE_NS ?= tide-stage

# The wildcard domain the local ingress serves, and the cert-manager secret
# holding its CA. Nothing outside a laptop resolves these.
DEV_DOMAIN ?= example.test
CA_SECRET  ?= local-ca

# Local-only database and cache credentials, created by deploy/dev.
DEV_DB_OWNER_PASSWORD ?= tide-dev-owner
DEV_DB_APP_PASSWORD   ?= tide-dev-app
# The shared Redis the integration tests use; empty skips the Redis tests.
TEST_REDIS_URL ?=

# The local rehearsal of the production manifests (`make stage-up`).
STAGE_PG_HOST     ?= pgsql.infra.svc:5432
STAGE_REDIS_URL   ?=
# Throwaway: 32 bytes, base64. Never reuse outside a laptop.
STAGE_SECRETS_KEY ?= c3RhZ2Utb25seS1lbmNyeXB0aW9uLWtleS0zMmJ5dGU=

.PHONY: dev-up dev-sso dev-sso-down dev-down dev-logs dev-web-logs test test-db lint web-check check build web stage-up stage-down stage-logs

## Bring up the dev environment (hostPath source, air + vite hot reload).
dev-up:
	$(K) get ns $(NS) >/dev/null 2>&1 || $(K) create ns $(NS)
	$(K) -n infra get secret infra-secrets -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d | \
		$(K) -n $(NS) create secret generic pg-admin --from-file=POSTGRES_PASSWORD=/dev/stdin --dry-run=client -o yaml | $(K) apply -f -
	$(K) -n cert-manager get secret $(CA_SECRET) -o jsonpath='{.data.ca\.crt}' | base64 -d | \
		$(K) -n $(NS) create secret generic local-ca --from-file=ca.crt=/dev/stdin --dry-run=client -o yaml | $(K) apply -f -
	$(K) -n $(NS) delete job db-provision --ignore-not-found
	sed -e "s/INGRESS_IP/$$($(K) -n ingress-nginx get svc ingress-nginx-controller -o jsonpath='{.spec.clusterIP}')/" \
	    -e "s/DEV_DOMAIN/$(DEV_DOMAIN)/g" -e "s#SOURCE_PATH#$(CURDIR)#g" \
	    -e "s/OWNER_PASSWORD/$(DEV_DB_OWNER_PASSWORD)/g" -e "s/APP_PASSWORD/$(DEV_DB_APP_PASSWORD)/g" \
	    deploy/dev/tide.yaml | $(K) apply -f -
	$(K) -n $(NS) wait --for=condition=complete job/db-provision --timeout=180s
	@echo "https://tide.$(DEV_DOMAIN)"

## Optional local OIDC provider (Dex), only needed when working on SSO.
dev-sso:
	sed "s/DEV_DOMAIN/$(DEV_DOMAIN)/g" deploy/dev/dex.yaml | $(K) apply -f -
	$(K) -n $(NS) rollout status deploy/dex --timeout=120s
	@echo "https://dex.$(DEV_DOMAIN)/dex"

dev-sso-down:
	sed "s/DEV_DOMAIN/$(DEV_DOMAIN)/g" deploy/dev/dex.yaml | $(K) delete -f - --ignore-not-found

dev-down:
	$(K) delete ns $(NS)

dev-logs:
	$(K) -n $(NS) logs -f deploy/backend

dev-web-logs:
	$(K) -n $(NS) logs -f deploy/web

# kubectl exec is broken on this docker-desktop, so tests run via docker exec
# in the backend container, where the infra database is reachable.
BACKEND = $$(docker ps -q --filter 'name=^k8s_backend_backend-.*_$(NS)_' | head -1)

## Unit tests with the race detector (database tests skip without TIDE_TEST_*).
test:
	go test -race ./...

## Integration tests: real PostgreSQL and real Redis, inside the dev
## container where both are reachable. Database 9 on Redis is scratch space.
test-db:
	docker exec -w /workspace \
		-e TIDE_TEST_OWNER_URL=postgres://tide_owner:$(DEV_DB_OWNER_PASSWORD)@pgsql.infra.svc:5432/tide_test?sslmode=disable \
		-e TIDE_TEST_APP_URL=postgres://tide_app:$(DEV_DB_APP_PASSWORD)@pgsql.infra.svc:5432/tide_test?sslmode=disable \
		-e TIDE_TEST_REDIS_URL=$(TEST_REDIS_URL) \
		$(BACKEND) sh -c 'go test -count=1 -p 1 ./...'

lint:
	go vet ./...
	golangci-lint run

web-check:
	cd web && pnpm typecheck && pnpm lint && pnpm test && pnpm build
	touch internal/web/dist/.keep

## Rehearse the production manifests on this cluster: same image, same
## migrate init container, same two replicas, its own namespace and database.
## It does not touch tide-dev or the demo data in the `tide` database.
stage-up:
	docker build -t localhost:5001/tide:stage .
	docker push localhost:5001/tide:stage
	$(K) get ns $(STAGE_NS) >/dev/null 2>&1 || $(K) create ns $(STAGE_NS)
	$(K) -n infra get secret infra-secrets -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d | \
		$(K) -n $(STAGE_NS) create secret generic pg-admin --from-file=POSTGRES_PASSWORD=/dev/stdin --dry-run=client -o yaml | $(K) apply -f -
	$(K) -n $(STAGE_NS) create secret generic tide --dry-run=client -o yaml \
		--from-literal=TIDE_DATABASE_URL="postgres://tide_app:$(DEV_DB_APP_PASSWORD)@$(STAGE_PG_HOST)/tide_stage?sslmode=disable" \
		--from-literal=TIDE_REDIS_URL="$(STAGE_REDIS_URL)" \
		--from-literal=TIDE_SECRETS_KEY="$(STAGE_SECRETS_KEY)" \
		--from-literal=TIDE_SERVER_METRICS_TOKEN=stage-only | $(K) apply -f -
	$(K) -n $(STAGE_NS) create secret generic tide-migrate --dry-run=client -o yaml \
		--from-literal=TIDE_DATABASE_MIGRATE_URL="postgres://tide_owner:$(DEV_DB_OWNER_PASSWORD)@$(STAGE_PG_HOST)/tide_stage?sslmode=disable" | $(K) apply -f -
	$(K) -n $(STAGE_NS) delete job db-provision --ignore-not-found
	sed -e "s/OWNER_PASSWORD/$(DEV_DB_OWNER_PASSWORD)/g" -e "s/APP_PASSWORD/$(DEV_DB_APP_PASSWORD)/g" \
	    deploy/stage/db-provision.yaml > /tmp/tide-stage-db.yaml
	$(K) apply -k deploy/stage
	$(K) apply -f /tmp/tide-stage-db.yaml
	$(K) -n $(STAGE_NS) wait --for=condition=complete job/db-provision --timeout=180s
	$(K) -n $(STAGE_NS) rollout status deploy/tide --timeout=300s
	@echo "https://tide-stage.$(DEV_DOMAIN)"

stage-down:
	$(K) delete ns $(STAGE_NS) --ignore-not-found
	@echo "the tide_stage database is left behind; drop it by hand if you want it gone"

stage-logs:
	$(K) -n $(STAGE_NS) logs -f deploy/tide

## Quality gates (CONVENTIONS.md §9).
check: lint test test-db web-check

web:
	cd web && pnpm build
# vite empties dist, .keep included; it is the only tracked file in there and
# `go build` needs it, or //go:embed all:dist finds nothing in a fresh clone.
	touch internal/web/dist/.keep

build: web
	CGO_ENABLED=0 go build -o bin/tide ./cmd/tide
