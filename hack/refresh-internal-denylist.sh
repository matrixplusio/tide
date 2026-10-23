#!/usr/bin/env sh
# Writes hack/internal-data-denylist.local.txt: the real names and addresses
# of one environment, taken from where they actually live rather than from
# anyone's memory, so the checks know them word for word.
#
#   hack/refresh-internal-denylist.sh
#
# Read-only everywhere. Where it reads comes from local.mk (never committed):
#
#   LEAK_KCTX   kube context of the cluster running the production database
#   LEAK_PG_NS  its namespace            LEAK_PG_POD  its pod
#   LEAK_PG_DB  the Tide database name
#
# and, always, the kube contexts on this machine and DEV_DOMAIN. Words too
# common to forbid that would reveal something if listed here go in
# hack/internal-data-common.local.txt. Anything
# else worth hiding (a company name, an IdP client id) goes, one per line, in
# hack/internal-data-denylist.extra.local.txt, which is merged in.
#
# The output is itself sensitive — it lists what is being hidden — and is
# ignored by git like every *.local.* file.
set -eu

cd "$(dirname "$0")/.."
out=hack/internal-data-denylist.local.txt
extra=hack/internal-data-denylist.extra.local.txt
allow=hack/internal-data-allow.txt

mk() { grep -E "^$1 *[:?]?=" local.mk 2>/dev/null | head -1 | sed -E 's/^[^=]*= *//'; }
kctx=$(mk LEAK_KCTX); ns=$(mk LEAK_PG_NS); pod=$(mk LEAK_PG_POD); db=$(mk LEAK_PG_DB)

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# 1. What the production Tide knows: services, applications, projects,
#    stages, image repositories, people, and every host in its settings.
if [ -n "$kctx" ] && [ -n "$pod" ]; then
  kubectl --context "$kctx" -n "${ns:-default}" exec "$pod" -c postgres -- \
    env PGOPTIONS='-c default_transaction_read_only=on' psql -d "${db:-tide}" -At -c "
    SELECT v FROM (
      SELECT payload->>'service' AS v FROM release_items
      UNION SELECT payload->>'app' FROM release_items
      UNION SELECT payload->>'project' FROM release_items
      UNION SELECT payload->>'stage' FROM release_items
      UNION SELECT payload->>'image' FROM release_items
      UNION SELECT service FROM ci_intake
      UNION SELECT ci_actor FROM ci_intake
      UNION SELECT image FROM ci_intake
      UNION SELECT name FROM users WHERE method = 'oidc'
      UNION SELECT created_by_name FROM releases
      UNION SELECT env FROM releases
      UNION SELECT u->>'kargoUrl' FROM settings, jsonb_array_elements(value->'items') u WHERE section = 'upstreams'
      UNION SELECT u->>'argocdUrl' FROM settings, jsonb_array_elements(value->'items') u WHERE section = 'upstreams'
      UNION SELECT u->>'registryUrl' FROM settings, jsonb_array_elements(value->'items') u WHERE section = 'upstreams'
      UNION SELECT u->>'name' FROM settings, jsonb_array_elements(value->'items') u WHERE section = 'upstreams'
      UNION SELECT value->>'issuer' FROM settings WHERE section = 'oidc'
      UNION SELECT value->>'clientId' FROM settings WHERE section = 'oidc'
      UNION SELECT value->>'baseUrl' FROM settings WHERE section = 'system'
      UNION SELECT c->>'url' FROM settings, jsonb_array_elements(value->'channels') c WHERE section = 'notify'
    ) x WHERE v IS NOT NULL AND v <> '';" > "$tmp/raw"
else
  echo "local.mk 里没有 LEAK_KCTX / LEAK_PG_POD，跳过生产数据，只用本机信息" >&2
  : > "$tmp/raw"
fi

# 2. This machine: kube contexts and the dev domain.
kubectl config get-contexts -o name 2>/dev/null >> "$tmp/raw" || :
mk DEV_DOMAIN >> "$tmp/raw"
[ -f "$extra" ] && grep -vE '^[[:space:]]*(#|$)' "$extra" >> "$tmp/raw"

# Turn raw values into words to forbid: a URL gives its host and the domain
# under it, an image reference its registry host and each path segment, a
# GKE context its project and cluster. Then drop what is too common to be
# anyone's secret, and what the allow list vouches for.
awk '
  function add(w) { w = tolower(w); gsub(/^[[:space:]]+|[[:space:]]+$/, "", w); if (w != "") print w }
  function host(h,   n, p) {
    add(h); n = split(h, p, "."); if (n >= 3) add(p[n-1] "." p[n])
  }
  {
    v = $0
    if (v ~ /^https?:\/\//) { sub(/^https?:\/\//, "", v); sub(/[\/?#].*/, "", v); sub(/:[0-9]+$/, "", v); host(v); next }
    if (v ~ /^gke_/) { add(v); n = split(v, p, "_"); add(p[2]); add(p[n]); next }
    if (v ~ /\//) {                              # registry/path/name[:tag]
      sub(/[:@][^\/]*$/, "", v); n = split(v, p, "/")
      if (p[1] ~ /\./) host(p[1]); for (i = 2; i <= n; i++) add(p[i]); next
    }
    add(v)
  }' "$tmp/raw" | sort -u > "$tmp/words"

# Words that turn up in ordinary prose and code everywhere: forbidding them
# would make every commit fail and teach people to ignore the check.
cat > "$tmp/common" <<'W'
admin
api
app
docker-desktop
gcp
gitlab-ci
idc
backend
default
dev
frontend
local
localhost
main
prod
qa
test
uat
web
github.com
ghcr.io
docker.io
open.feishu.cn
open.larksuite.com
larksuite.com
feishu.cn
office.com
webhook.office.com
logic.azure.com
azure.com
powerplatform.com
microsoft.com
W
grep -vE '^[[:space:]]*(#|$)' "$allow" 2>/dev/null | tr 'A-Z' 'a-z' >> "$tmp/common" || :
# Common words that would still say something about the business if this
# public script listed them go in a local file of their own.
grep -vE '^[[:space:]]*(#|$)' hack/internal-data-common.local.txt 2>/dev/null | tr 'A-Z' 'a-z' >> "$tmp/common" || :

# Shorter than three characters, or only digits: too likely to be a word.
grep -vxFf "$tmp/common" "$tmp/words" | awk 'length($0) >= 3 && $0 !~ /^[0-9.]+$/' > "$tmp/final"

{
  echo "# Generated by hack/refresh-internal-denylist.sh on $(date -u +%Y-%m-%dT%H:%MZ). Local only; do not commit."
  echo "# One real value per line; the checks forbid each as a whole word, any case."
  cat "$tmp/final"
} > "$out"
echo "写入 ${out}：$(wc -l < "$tmp/final" | tr -d ' ') 个真实值"
