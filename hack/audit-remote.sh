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

# Same rules, real-value list and allow list as the commit and push hooks.
. "$here/lib-internal-data.sh"
leak_setup "$here" "$tmp"

found=0
for ref in $refs; do
  # git grep prints "<ref>:<path>:<line>:<match>"; drop the ref so the
  # allow list sees "path:line:match" like everywhere else.
  scan_ref() { git grep -nI "$@" "$ref" -- . ':(exclude)hack/internal-data-*' ':(exclude)hack/lib-internal-data.sh' ':(exclude)hack/check-no-internal-data.sh' ':(exclude)internal/web/dist' | sed "s#^$ref:##"; }
  leak_scan "$tmp" "  在 $ref" scan_ref || found=1
done
# Messages are published with every ref; read them all once.
git log --all --format='%h %s%n%b' > "$tmp/messages"
scan_msgs() { grep -n "$@" "$tmp/messages"; }
leak_scan "$tmp" "  在提交信息里" scan_msgs || found=1

if [ "$found" -ne 0 ]; then
  cat >&2 <<'MSG'

检查未通过，见上面命中的位置。
MSG
  exit 1
fi
echo "所有 ref 都干净。"
