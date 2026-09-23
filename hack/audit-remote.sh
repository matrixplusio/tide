#!/usr/bin/env sh
# Scans everything a stranger can fetch from the public repository, not just
# the branches and tags you meant to publish.
#
#   hack/audit-remote.sh https://github.com/<org>/<repo>.git
#
# Includes refs/pull/*, which a plain clone leaves out.
set -u

url=${1:-}
[ -n "$url" ] || { echo "usage: $0 <clone-url>" >&2; exit 2; }

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "克隆（匿名，不带任何凭据）…"
git -c credential.helper= -c http.extraheader= \
    clone --quiet --no-checkout "$url" "$tmp/repo" || {
  echo "克隆失败：仓库不存在，或者不是公开的" >&2; exit 1
}
cd "$tmp/repo"

# Branches and tags come with the clone; pull request heads have to be asked
# for by name, which is exactly why they get forgotten.
git fetch --quiet origin '+refs/pull/*:refs/remotes/pull/*' 2>/dev/null || true

refs=$(git for-each-ref --format='%(refname)' refs/remotes refs/tags | sort -u)
echo "可达的 ref：$(printf '%s\n' "$refs" | wc -l | tr -d ' ') 个"
printf '%s\n' "$refs" | sed 's/^/  /'
echo

patterns="$here/internal-data-patterns.txt"
local_patterns="$here/internal-data-patterns.local.txt"
cat "$patterns" "$local_patterns" 2>/dev/null > "$tmp/patterns"

found=0
for ref in $refs; do
  while IFS= read -r line; do
    case "$line" in ''|\#*) continue ;; esac
    name=${line%%|*}
    regex=${line#*|}
    hits=$(git grep -nIE "$regex" "$ref" -- 2>/dev/null | head -5)
    if [ -n "$hits" ]; then
      found=1
      printf '\033[31m✗ %s  在 %s\033[0m\n' "$name" "$ref"
      printf '%s\n' "$hits" | sed 's/^/    /'
    fi
  done < "$tmp/patterns"
done

if [ "$found" -ne 0 ]; then
  cat >&2 <<'MSG'

检查未通过，见上面命中的位置。
MSG
  exit 1
fi
echo "所有 ref 都干净。"
