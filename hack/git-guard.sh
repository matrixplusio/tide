#!/usr/bin/env sh
# A pre-command check for automated tooling: refuses shell commands that
# would get a commit or a push past this repository's git hooks. Git hooks
# are skipped by a flag, so the refusal is made here, outside them.
#
# Reads {"tool_input":{"command":"..."}} on stdin; exit 2 blocks the command
# and stderr says why. Anything that is not a git command passes untouched.
cmd=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("command",""))' 2>/dev/null) || exit 0
case "$cmd" in *git*) ;; *) exit 0 ;; esac

deny() {
  printf '已拦截：%s\n命令：%s\n提交和推送必须经过 hack/check-no-internal-data.sh。修掉被拦的内容再来，不要绕过。\n' "$1" "$cmd" >&2
  exit 2
}

# Normalise runs of spaces so "git  commit" does not slip through.
c=$(printf '%s' "$cmd" | tr -s '[:space:]' ' ')

printf '%s' "$c" | grep -qE -- '--no-verify' && deny '--no-verify 会跳过提交和推送检查'
printf '%s' "$c" | grep -qE 'git( -[cC] [^ ]+)* commit( [^|;&]*)? -[a-zA-Z]*n' && deny 'git commit -n 就是 --no-verify'
# Pointing the hooks anywhere but .githooks, or unsetting them, switches the
# gates off. Reading the setting, or setting it back, is fine.
printf '%s' "$c" | python3 -c '
import re, sys
c = sys.stdin.read()
for m in re.finditer(r"core\.hookspath\s*(=\s*|\s+)?(\S*)", c, re.I):
    val = m.group(2).strip("\"\x27;")
    if re.search(r"--unset[\w-]*\s+core\.hookspath", c[max(0, m.start()-30):m.end()], re.I) or (m.group(1) and val not in ("", ".githooks")):
        sys.exit(1)
' || deny '把 core.hooksPath 改成别的或取消，会让门禁钩子失效'
printf '%s' "$c" | grep -qE 'ALLOW_PUSH=' && deny 'ALLOW_PUSH 会放行 main 和 tag 以外的推送，要由人亲自决定'
printf '%s' "$c" | grep -qE 'git( -[cC] [^ ]+)* push( [^|;&]*)? (--mirror|--all)' && deny '--mirror / --all 会把所有本地引用推出去'
printf '%s' "$c" | grep -qE 'git( -[cC] [^ ]+)* push( [^|;&]*)? (-f|--force|--force-with-lease)( |$)' && deny '强推会改写公开历史'
printf '%s' "$c" | grep -qE '(rm|mv|chmod)[^|;&]*\.githooks' && deny '不能删除或改动 .githooks 里的钩子'
exit 0
