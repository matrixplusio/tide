#!/usr/bin/env sh
# Refuses to let a specific environment's data into a public repository.
#
#   hack/check-no-internal-data.sh              # everything git tracks
#   hack/check-no-internal-data.sh --staged     # what is about to be committed
#   hack/check-no-internal-data.sh --messages   # the last 50 commit messages
#   hack/check-no-internal-data.sh --message F  # one message, from a file
#   hack/check-no-internal-data.sh --range R    # what a push sends: every
#                                               # message and added line in R
#                                               # (e.g. origin/main..HEAD)
#
# The repository is public. Examples use generic names (acme, example.com);
# anything naming one real environment stays out. The rules and how they
# match are in hack/lib-internal-data.sh.
set -u

cd "$(dirname "$0")/.."
. hack/lib-internal-data.sh

tmp=${TMPDIR:-/tmp}/tide-gate.$$
trap 'rm -rf "$tmp"' EXIT INT TERM
mkdir -p "$tmp" || exit 2
leak_setup hack "$tmp"

fail_text() {
  printf '\n%s里不能有这些内容。示例和测试用假名（acme、globex、example.com）。\n不要用 --no-verify 绕过。\n' "$1" >&2
  exit 1
}

case "${1:-}" in
  --messages|--message|--range)
    case "$1" in
      --messages) git log --format='%h %s%n%b' -50 > "$tmp/text" ;;
      --message)  cat "${2:?usage: --message <file>}" > "$tmp/text" ;;
      --range)
        r=${2:?usage: --range <rev-range>}
        # Commit by commit, not the net difference: a name added in one
        # commit and removed in the next is gone from the tree but not from
        # the history being published. Each message, then each line that
        # commit adds, tagged with its commit and file.
        git log --no-color -p --unified=0 --format='@@commit %h: %s%n%b' "$r" -- . ':(exclude)internal/web/dist' 2>/dev/null \
          | awk '/^@@commit /{c=substr($2,1,length($2)-1); print substr($0,3); inmsg=1; next}
                 /^diff --git /{inmsg=0; next}
                 inmsg{print; next}
                 /^\+\+\+ /{f=substr($0,7); next}
                 /^\+/{print c " " f ": " substr($0,2)}' > "$tmp/text" ;;
    esac
    [ -s "$tmp/text" ] || exit 0
    : > "$tmp/hits"
    # Options first, the file last: BSD grep stops reading options at the
    # first file name.
    scan_text() { grep -n "$@" "$tmp/text"; }
    leak_scan "$tmp" "" scan_text
    [ $? -eq 2 ] && exit 2
    if [ -s "$tmp/hits" ]; then
      if [ "$1" = --range ]; then
        printf '\n出处：\n' >&2
        cut -d: -f1 "$tmp/hits" | sort -un | while read -r n; do
          printf '    %s\n' "$(sed -n "${n}p" "$tmp/text" | cut -c1-200)" >&2
        done
        fail_text "要推送的提交"
      fi
      fail_text "提交信息"
    fi
    echo "提交信息干净。"
    exit 0 ;;
  --staged) files=$(git diff --cached --name-only --diff-filter=ACMR) ;;
  "")       files=$(git ls-files) ;;
  *)        echo "usage: $0 [--staged|--messages|--message <file>|--range <rev-range>]" >&2; exit 2 ;;
esac

[ -n "$files" ] || exit 0
# Built artifacts and the rule files themselves would match themselves.
files=$(printf '%s\n' "$files" | grep -vE '^(internal/web/dist/|hack/internal-data-(patterns|allow)[^ ]*$|hack/(check-no-internal-data|lib-internal-data)\.sh$)' || true)
[ -n "$files" ] || exit 0
printf '%s\n' "$files" | tr '\n' '\0' > "$tmp/files"
: > "$tmp/hits"

# -H: with a single file grep leaves the name out, and then the report says
# where the hit is but not in which file.
scan_files() { xargs -0 grep -HnI "$@" < "$tmp/files"; }
leak_scan "$tmp" "" scan_files
rc=$?
[ $rc -eq 2 ] && exit 2
if [ $rc -ne 0 ]; then
  cat >&2 <<'MSG'

这些内容不能进公开仓库。

改掉它们，或者——确实是误报的话——把那个词加进 hack/internal-data-allow.txt，
只加通用英文和本仓库自己编的假名字，真实环境里的东西一个都不许加。

不要用 --no-verify 绕过。
MSG
  exit 1
fi
