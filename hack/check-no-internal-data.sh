#!/usr/bin/env sh
# Refuses to let a specific environment's data into a public repository.
#
#   hack/check-no-internal-data.sh              # everything git tracks
#   hack/check-no-internal-data.sh --staged     # what is about to be committed
#   hack/check-no-internal-data.sh --messages   # the last 50 commit messages
#   hack/check-no-internal-data.sh --message F  # one message, from a file
#
# The repository is public. Examples use generic names (acme, example.com);
# anything naming one real environment stays out.
#
# Patterns live in hack/internal-data-patterns.txt, one extended regex per
# line, '#' starts a comment. They are deliberately wide — a service name
# nobody remembers to enumerate is exactly the one that leaks — and the
# false positives that follow are answered by name in
# hack/internal-data-allow.txt rather than by narrowing the rule.
set -u

cd "$(dirname "$0")/.."
patterns=hack/internal-data-patterns.txt
local_patterns=hack/internal-data-patterns.local.txt
allow=hack/internal-data-allow.txt

mode=files
messages=""
case "${1:-}" in
  --staged)   files=$(git diff --cached --name-only --diff-filter=ACMR) ;;
  # Commit messages are as public as the code, and are where a real name
  # turns up without any file ever holding it. Checked separately because
  # they are not files.
  --messages) mode=text; messages=$(git log --format='%h %s%n%b' -50) ;;
  --message)  mode=text; messages=$(cat "${2:?usage: --message <file>}") ;;
  "")         files=$(git ls-files) ;;
  *)          echo "usage: $0 [--staged|--messages|--message <file>]" >&2; exit 2 ;;
esac

tmp=${TMPDIR:-/tmp}/tide-gate.$$
trap 'rm -rf "$tmp"' EXIT INT TERM
mkdir -p "$tmp" || exit 2
cat "$patterns" "$local_patterns" 2>/dev/null > "$tmp/patterns"
grep -vE '^[[:space:]]*(#|$)' "$allow" 2>/dev/null | tr 'A-Z' 'a-z' > "$tmp/allow" || : > "$tmp/allow"

# drop_allowed reads "location:matched text" lines and removes the ones whose
# matched text, once stripped of the delimiters the regex had to include, is
# a word the allow list vouches for.
drop_allowed() {
  # LC_ALL=C: the matched text can be any bytes, and awk trying to decode it
  # as characters turns a stray byte into a warning on stderr.
  LC_ALL=C awk -v allowfile="$tmp/allow" '
    BEGIN { n = 0; while ((getline w < allowfile) > 0) if (w != "") allow[++n] = w }
    function bare(c) { return c ~ /[a-z0-9-]/ }
    {
      # grep prints "path:line:match"; the match may contain colons of its
      # own, so strip that prefix rather than splitting on every colon. What
      # is left still carries whatever delimiters the rule had to include,
      # and those can be multibyte — so rather than trimming them, look for
      # an allowed word sitting inside with nothing word-like touching it.
      m = $0
      sub(/^[^:]*:[0-9]+:/, "", m)
      m = tolower(m)
      for (i = 1; i <= n; i++) {
        p = index(m, allow[i])
        if (p == 0) continue
        before = (p == 1) ? "" : substr(m, p - 1, 1)
        after = substr(m, p + length(allow[i]), 1)
        if (!bare(before) && !bare(after)) next
      }
      print
    }'
}

report() { # name, hits
  printf '\n\033[31m✗ %s\033[0m\n' "$1"
  printf '%s\n' "$2" | sed 's/^/    /'
}

found=0
if [ "$mode" = text ]; then
  [ -n "$messages" ] || exit 0
  while IFS= read -r line; do
    case "$line" in ''|\#*) continue ;; esac
    name=${line%%|*}; regex=${line#*|}
    hits=$(printf '%s\n' "$messages" | grep -noE "$regex" 2>/dev/null | drop_allowed | head -5)
    [ -n "$hits" ] || continue
    found=1
    report "${name}（提交信息里）" "$hits"
  done < "$tmp/patterns"
  [ "$found" -eq 0 ] || { printf '\n提交信息里不能有这些内容。\n' >&2; exit 1; }
  echo "提交信息干净。"
  exit 0
fi

[ -n "$files" ] || exit 0

# Built artifacts and the rule files themselves would match themselves.
files=$(printf '%s\n' "$files" | grep -vE '^(internal/web/dist/|hack/internal-data-(patterns|allow)[^ ]*$|hack/check-no-internal-data\.sh$)' || true)
[ -n "$files" ] || exit 0

while IFS= read -r line; do
  case "$line" in ''|\#*) continue ;; esac
  name=${line%%|*}; regex=${line#*|}
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
    | xargs -0 grep -noIE "$regex" 2>/dev/null | drop_allowed | head -20 || true)
  [ -n "$hits" ] || continue
  found=1
  report "$name" "$hits"
done < "$tmp/patterns"

if [ "$found" -ne 0 ]; then
  cat >&2 <<'MSG'

这些内容不能进公开仓库。

改掉它们，或者——确实是误报的话——把那个词加进 hack/internal-data-allow.txt，
只加通用英文和本仓库自己编的假名字，真实环境里的东西一个都不许加。

不要用 --no-verify 绕过。
MSG
  exit 1
fi
