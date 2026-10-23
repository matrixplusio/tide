# Shared by every check that keeps one environment's data out of this public
# repository: the commit and push hooks, CI and the audit of what GitHub
# serves. One copy of the rules, so the four cannot disagree about what is a
# hit. Source it; it defines functions and touches nothing.
#
# Three inputs, all under hack/:
#   internal-data-patterns.txt          generic rules, public: <name>|<ERE>
#   internal-data-patterns.local.txt    rules naming this environment, local
#   internal-data-denylist.local.txt    real values, one per line, matched as
#                                       whole words regardless of case — made
#                                       by refresh-internal-denylist.sh from
#                                       where the values actually live
#   internal-data-allow.txt             words that look like a hit and are not
#
# The local files are not in the repository (a list of what you hide is
# itself worth hiding); CI and a fresh clone run with the generic rules only.

# leak_setup <hack dir> <scratch dir>: gathers the rule files into the scratch
# dir. Missing local files are not an error.
leak_setup() {
  _h=$1; _t=$2
  cat "$_h/internal-data-patterns.txt" "$_h/internal-data-patterns.local.txt" 2>/dev/null > "$_t/patterns"
  grep -vE '^[[:space:]]*(#|$)' "$_h/internal-data-denylist.local.txt" 2>/dev/null > "$_t/denylist" || : > "$_t/denylist"
  grep -vE '^[[:space:]]*(#|$)' "$_h/internal-data-allow.txt" 2>/dev/null | tr 'A-Z' 'a-z' > "$_t/allow" || : > "$_t/allow"
}

# leak_drop_allowed reads "location:matched text" lines on stdin and drops the
# ones whose matched text is a word the allow list vouches for, with nothing
# word-like touching it.
leak_drop_allowed() {
  LC_ALL=C awk -v allowfile="$1/allow" '
    BEGIN { n = 0; while ((getline w < allowfile) > 0) if (w != "") allow[++n] = w }
    function bare(c) { return c ~ /[a-z0-9-]/ }
    {
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

# leak_report <title> <hits>
leak_report() {
  printf '\n\033[31m✗ %s\033[0m\n' "$1" >&2
  printf '%s\n' "$2" | sed 's/^/    /' >&2
}

# leak_scan <scratch dir> <label> <grep-like command...>
# Runs the command once per rule with the rule's options appended, the way
# grep takes them, and reports what survives the allow list. The command
# must print "location:line:match". Returns 1 when anything was found.
leak_scan() {
  _t=$1; _label=$2; shift 2
  _found=0
  while IFS= read -r _line; do
    case "$_line" in ''|\#*) continue ;; esac
    _name=${_line%%|*}; _re=${_line#*|}
    # A rule that does not compile would otherwise pass silently.
    printf '' | grep -qE "$_re" 2>/dev/null
    if [ $? -gt 1 ]; then
      printf '\033[31m✗ 规则 %s 的正则无法编译\033[0m\n  %s\n' "$_name" "$_re" >&2
      return 2
    fi
    _hits=$("$@" -oE "$_re" 2>/dev/null | leak_drop_allowed "$_t" | head -20)
    [ -n "$_hits" ] || continue
    _found=1
    printf '%s\n' "$_hits" >> "$_t/hits"
    leak_report "$_name$_label" "$_hits"
  done < "$_t/patterns"
  # Real values: fixed strings, whole words, any case. -w treats '-' as a
  # boundary, so a service name is caught inside a longer application name.
  if [ -s "$_t/denylist" ]; then
    _hits=$("$@" -o -i -w -F -f "$_t/denylist" 2>/dev/null | leak_drop_allowed "$_t" | head -20)
    if [ -n "$_hits" ]; then
      _found=1
      printf '%s\n' "$_hits" >> "$_t/hits"
      leak_report "本地清单$_label" "$_hits"
    fi
  fi
  return $_found
}
