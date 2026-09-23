#!/usr/bin/env sh
# Refuses to let a specific environment's data into a public repository.
#
#   hack/check-no-internal-data.sh              # everything git tracks
#   hack/check-no-internal-data.sh --staged     # what is about to be committed
#
# The repository is public. Examples use generic names (acme, example.com);
# anything naming one real environment stays out.
#
# Patterns live in hack/internal-data-patterns.txt, one extended regex per
# line, '#' starts a comment. Keep them specific: a pattern that fires on
# ordinary English makes the check useless by making it noisy.
set -u

cd "$(dirname "$0")/.."
patterns=hack/internal-data-patterns.txt
local_patterns=hack/internal-data-patterns.local.txt

messages=""
case "${1:-}" in
  --staged) files=$(git diff --cached --name-only --diff-filter=ACMR) ;;
  # Commit messages are as public as the code. Checked separately because
  # they are not files.
  --messages) files=""; messages=$(git log --format='%h %s%n%b' -50) ;;
  "")       files=$(git ls-files) ;;
  *)        echo "usage: $0 [--staged|--messages]" >&2; exit 2 ;;
esac

if [ -n "$messages" ]; then
  bad=0
  while IFS= read -r line; do
    case "$line" in ''|\#*) continue ;; esac
    name=${line%%|*}
    regex=${line#*|}
    hits=$(printf '%s\n' "$messages" | grep -nE "$regex" 2>/dev/null | head -5)
    if [ -n "$hits" ]; then
      bad=1
      printf '\033[31m✗ %s（提交信息里）\033[0m\n' "$name"
      printf '%s\n' "$hits" | sed 's/^/    /'
    fi
  done < "$(cat "$patterns" "$local_patterns" 2>/dev/null > /tmp/tide-msg-pat.$$ && echo /tmp/tide-msg-pat.$$)"
  rm -f /tmp/tide-msg-pat.$$
  [ "$bad" -eq 0 ] || { echo "\n提交信息里不能有这些内容。" >&2; exit 1; }
  echo "最近 50 条提交信息干净。"
  exit 0
fi

[ -n "$files" ] || exit 0

# Built artifacts and the pattern file itself would match themselves.
files=$(printf '%s\n' "$files" | grep -vE '^(internal/web/dist/|hack/internal-data-patterns[^ ]*$|hack/check-no-internal-data\.sh$)' || true)
[ -n "$files" ] || exit 0

found=0
while IFS= read -r line; do
  case "$line" in ''|\#*) continue ;; esac
  name=${line%%|*}
  regex=${line#*|}
  # A rule whose regex does not compile would otherwise pass silently, which
  # is the one failure mode a check like this cannot afford. grep exits 1 for
  # "compiled, no match" and 2 for "bad pattern"; only the second is a problem.
  printf '' | grep -qE "$regex" 2>/dev/null
  if [ $? -gt 1 ]; then
    printf '\033[31m✗ 规则 %s 的正则无法编译\033[0m\n  %s\n' "$name" "$regex" >&2
    exit 2
  fi
  # -I skips binaries; without it a match inside a PNG prints the whole file.
  hits=$(printf '%s\n' "$files" | tr '\n' '\0' \
    | xargs -0 grep -nIE "$regex" 2>/dev/null | head -20 || true)
  if [ -n "$hits" ]; then
    found=1
    printf '\n\033[31m✗ %s\033[0m\n' "$name"
    printf '%s\n' "$hits" | sed 's/^/    /'
  fi
done < "$(cat "$patterns" "$local_patterns" 2>/dev/null > /tmp/tide-patterns.$$ && echo /tmp/tide-patterns.$$)"
rm -f /tmp/tide-patterns.$$

if [ "$found" -ne 0 ]; then
  cat >&2 <<'MSG'

这些内容不能进公开仓库。

改掉它们，或者——确实是误报的话——把文件排除在 hack/check-no-internal-data.sh
里，或者收窄 hack/internal-data-patterns.txt 中对应的正则。

不要用 --no-verify 绕过。
MSG
  exit 1
fi
