#!/usr/bin/env bash
#
# bun-orm audit — mechanically detectable regressions in Bun ORM code.
#
#   bash scripts/audit.sh [path ...]     # default: .
#
# Output: "<SEVERITY> <file>:<line>: <rule>: <why>"
# Exit:   0 = no FAIL hits, 1 = at least one FAIL hit. WARN never fails the run.
#
# Limits, stated up front so nobody trusts this more than it deserves:
#   * every rule is a text pattern — it cannot see types, scopes or call graphs;
#   * proximity rules use a small line window, so a predicate built far from the
#     statement it applies to can be missed (a false negative, never a false alarm);
#   * FAIL means "this is wrong in every reading I can think of";
#   * WARN means "a human must judge this" — read the reason, then decide.
# Anything semantic (the statement really joined the transaction, the rollback really
# rolled back) is out of scope and belongs in a test.

set -uo pipefail

if [ "$#" -gt 0 ]; then paths=("$@"); else paths=("."); fi

fail=0
hits=0

emit() { # severity file line rule why
  printf '%s %s:%s: %s: %s\n' "$1" "$2" "$3" "$4" "$5"
  hits=$((hits + 1))
  [ "$1" = "FAIL" ] && fail=1
  return 0
}

# scan <severity> <rule> <why> <match-pattern> [<file-must-also-contain>]
scan() {
  local sev="$1" rule="$2" why="$3" pat="$4" restrict="${5:-}"
  local f line

  while IFS= read -r f; do
    [ -z "$f" ] && continue
    if [ -n "$restrict" ] && ! grep -qE "$restrict" "$f" 2>/dev/null; then
      continue
    fi
    while IFS= read -r line; do
      emit "$sev" "$f" "$line" "$rule" "$why"
    done < <(grep -nE "$pat" "$f" 2>/dev/null | cut -d: -f1)
  done < <(grep -rlE --include='*.go' "$pat" "${paths[@]}" 2>/dev/null || true)
}

# unbounded_write <rule> <pattern> <why>
# Flags a write statement with no Where in the following window: the shape that
# touches every row. A predicate built onto a query variable further away is not
# detected — deliberately, so the rule never cries wolf.
unbounded_write() {
  local rule="$1" pat="$2" why="$3" f line

  while IFS= read -r f; do
    [ -z "$f" ] && continue
    while IFS= read -r line; do
      emit FAIL "$f" "$line" "$rule" "$why"
    done < <(awk -v pat="$pat" '
      # Close the previous window first, then open a new one, then let a Where on
      # the same line count. END closes a window the file ended inside - without it
      # a write in the last six lines is never judged.
      {
        if (start && NR - start >= 6) { if (!found) print start; start = 0 }
        if ($0 ~ pat) { start = NR; found = 0 }
        if (start && $0 ~ /Where\(|WherePK\(/) { found = 1 }
      }
      END { if (start && !found) print start }
    ' "$f")
  done < <(grep -rlE --include='*.go' "$pat" "${paths[@]}" 2>/dev/null || true)
}

printf 'bun-orm audit — paths: %s\n\n' "${paths[*]}"

# ---------------------------------------------------------------- FAIL rules

scan FAIL string-built-sql \
  "SQL assembled by concatenation: values must be bound with ?, identifiers validated against an allow-list" \
  '"(SELECT|INSERT|UPDATE|DELETE)[^"]*"[[:space:]]*\+|Sprintf\("(SELECT|INSERT|UPDATE|DELETE)'

scan FAIL query-on-background-context \
  "a query on context.Background()/TODO() cannot be cancelled and cannot join a caller's transaction: pass the request context" \
  '(Scan|Exec|Query)\(context\.(Background|TODO)\(\)'

# soft_delete on a VALUE time.Time needs nullzero, or the column is written as a
# zero timestamp instead of NULL and the implicit "deleted_at IS NULL" filter never
# matches the deleted row. Two shapes are correct and must stay silent:
#   * a pointer column (*time.Time) already encodes NULL, so nullzero adds nothing;
#   * the tag may legitimately live on a struct built by hand (still needs nullzero
#     on a value field, so only the pointer case is skipped).
while IFS= read -r f; do
  [ -z "$f" ] && continue
  while IFS= read -r hit; do
    lineno="${hit%%:*}"
    content="${hit#*:}"
    case "$content" in
      *'bun:"'*soft_delete*) ;;      # a struct tag carrying soft_delete
      *) continue ;;
    esac
    case "$content" in
      *nullzero*) continue ;;        # the documented value-field shape
    esac
    case "$content" in
      *'*'*) continue ;;             # a pointer column: NULL is already expressible
    esac
    emit FAIL "$f" "$lineno" soft-delete-without-nullzero \
      "soft_delete on a value time.Time needs nullzero: without it the column gets a zero time, not NULL, and the implicit filter misses it"
  done < <(grep -nE 'soft_delete' "$f" 2>/dev/null)
done < <(grep -rlE --include='*.go' 'soft_delete' "${paths[@]}" 2>/dev/null || true)

unbounded_write unbounded-delete 'NewDelete\(\)' \
  "a delete with no Where in sight removes every row (and on a soft-deleting model it updates every row)"

unbounded_write unbounded-update 'NewUpdate\(\)' \
  "an update with no Where in sight rewrites every row"

# ---------------------------------------------------------------- WARN rules

scan WARN holds-root-handle \
  "holding *bun.DB invites a statement that cannot join a caller's transaction — the composition root and CLI helpers are the exceptions; a service that writes on a caller's behalf should store bun.IDB and resolve it from the context" \
  '^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*[[:space:]]+\*bun\.DB\b'

scan WARN unsafe-raw-sql \
  "Unsafe() bypasses the builder: make sure every value in this string is either a constant or bound" \
  '\.Unsafe\('

# A query statement inside a loop over rows is the N+1 shape that Relation() removes.
while IFS= read -r f; do
  [ -z "$f" ] && continue
  while IFS= read -r line; do
    emit WARN "$f" "$line" query-in-loop \
      "a query inside a loop: if it walks a page or batch cursor that is intended; if it iterates parent rows to fetch their children, the statement count grows with the rows — load the relation in one query"
  done < <(awk '
      /^[[:space:]]*for / { loop = NR }
      /(NewSelect|NewInsert|NewUpdate|NewDelete)\(\)/ { if (loop && NR - loop <= 4) { print NR; loop = 0 } }
  ' "$f")
done < <(grep -rlE --include='*.go' '(NewSelect|NewInsert|NewUpdate|NewDelete)\(\)' "${paths[@]}" 2>/dev/null || true)

# Count and Scan side by side in one file: ScanAndCount does both in one statement,
# and its count query drops ORDER BY/LIMIT/OFFSET so the total is the full result size.
while IFS= read -r f; do
  [ -z "$f" ] && continue
  if grep -qE '\.Count\(ctx' "$f" 2>/dev/null && grep -qE '\.Scan\(ctx' "$f" 2>/dev/null; then
    while IFS= read -r line; do
      emit WARN "$f" "$line" count-then-scan \
        "count and scan in the same file: ScanAndCount returns the full total in one statement and keeps both in sync"
    done < <(grep -nE '\.Count\(ctx' "$f" | cut -d: -f1)
  fi
done < <(grep -rlE --include='*.go' '\.Count\(ctx' "${paths[@]}" 2>/dev/null || true)

# A pool with no open-connection ceiling is how one service exhausts its database.
while IFS= read -r f; do
  [ -z "$f" ] && continue
  if ! grep -qE 'SetMaxOpenConns\(' "$f" 2>/dev/null; then
    while IFS= read -r line; do
      emit WARN "$f" "$line" pool-untuned \
        "no SetMaxOpenConns in this file: the default ceiling is unlimited — size it against the database limit divided by replicas"
    done < <(grep -nE 'bun\.NewDB\(' "$f" | cut -d: -f1)
  fi
done < <(grep -rlE --include='*.go' 'bun\.NewDB\(' "${paths[@]}" 2>/dev/null || true)

# ------------------------------------------------------------------- summary

printf '\n%s hit(s) reported; FAIL rules are decisive, WARN rules need judgement.\n' "$hits"
if [ "$fail" -eq 1 ]; then
  printf 'FAIL: at least one decisive rule fired.\n'
  exit 1
fi
printf 'OK: no FAIL rule fired.\n'
exit 0
