#!/usr/bin/env bash
#
# fiber-v3 audit — mechanically detectable regressions in Fiber v3 code.
#
#   bash scripts/audit.sh [path ...]     # default: .
#
# Output: "<SEVERITY> <file>:<line>: <rule>: <why>"
# Exit:   0 = no FAIL hits, 1 = at least one FAIL hit. WARN never fails the run.
#
# Limits, stated up front so nobody trusts this more than it deserves:
#   * every rule is a text pattern — it cannot see types, scopes or call graphs;
#   * FAIL means "this is wrong in every reading I can think of";
#   * WARN means "a human must judge this" — read the reason, then decide.
# Anything semantic (middleware really wrapping, a transaction really committing)
# is out of scope and belongs in a test.

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

printf 'fiber-v3 audit — paths: %s\n\n' "${paths[*]}"

# ---------------------------------------------------------------- FAIL rules

scan FAIL handler-owns-sql \
  "a handler that talks to the database: the request layer is doing the persistence layer's job" \
  '\.New(Select|Insert|Update|Delete)\(|database/sql|sql\.Open' \
  'fiber\.Ctx'

scan FAIL panic-in-handler \
  "panic is not an error path; return an error and let recover stay a net, not a strategy" \
  '^[[:space:]]*panic\(' \
  'fiber\.Ctx'

scan FAIL string-built-sql \
  "SQL assembled by concatenation: parameters must be bound, not formatted in" \
  '"(SELECT|INSERT|UPDATE|DELETE)[^"]*"[[:space:]]*\+|Sprintf\("(SELECT|INSERT|UPDATE|DELETE)'

scan FAIL cors-wildcard-with-credentials \
  "AllowOrigins \"*\" with AllowCredentials: browsers reject it and any reflection bug becomes a real hole" \
  'AllowOrigins[[:space:]]*:.*"\*"' \
  'AllowCredentials:[[:space:]]*true'

# ---------------------------------------------------------------- WARN rules

scan WARN config-mutated-after-new \
  "config read at fiber.New; mutating it afterwards silently changes nothing for registered routes" \
  'app\.Config\(\)\.[A-Za-z]+[[:space:]]*='

scan WARN string-locals-key \
  "Locals keyed by a string is a global namespace: use a private type so no other package can collide" \
  'c\.Locals\("[^"]+"\)'

scan WARN binding-bypass \
  "raw input access bypasses Bind() and the validator: if this field is validated, bind it into a struct" \
  'c\.(Params|Query|FormValue)\(' \
  'fiber\.Ctx'

# Fixed-window limiter on a user-facing surface: match "limiter.New(" in a file
# that never mentions the sliding window.
while IFS= read -r f; do
  [ -z "$f" ] && continue
  if ! grep -qE 'SlidingWindow' "$f" 2>/dev/null; then
    while IFS= read -r line; do
      emit WARN "$f" "$line" fixed-window-limiter \
        "fixed window lets a burst straddle the boundary and get double the allowance; use limiter.SlidingWindow"
    done < <(grep -nE 'limiter\.New\(' "$f" | cut -d: -f1)
  fi
done < <(grep -rlE --include='*.go' 'limiter\.New\(' "${paths[@]}" 2>/dev/null || true)

# Listen without a shutdown path: a process that cannot drain.
while IFS= read -r f; do
  [ -z "$f" ] && continue
  if grep -qE 'app\.Listen\(' "$f" 2>/dev/null && ! grep -qE 'app\.Shutdown\(' "$f" 2>/dev/null; then
    while IFS= read -r line; do
      emit WARN "$f" "$line" listen-without-shutdown \
        "Listen returns only after the server stops: without app.Shutdown there is no graceful drain"
    done < <(grep -nE 'app\.Listen\(' "$f" | cut -d: -f1)
  fi
done < <(grep -rlE --include='*.go' 'signal\.Notify|Run\(' "${paths[@]}" 2>/dev/null || true)

# Log-and-return: the same error is recorded and then handed on to be handled
# (and probably logged) again.
#
# Only meaningful where there IS a boundary that will handle the error, so the
# rule is restricted to HTTP files: a CLI command that warns and returns is
# doing the right thing (it is the top of its own stack).
while IFS= read -r f; do
  [ -z "$f" ] && continue
  grep -qE 'fiber\.Ctx' "$f" 2>/dev/null || continue
  while IFS= read -r hit; do
    emit WARN "$f" "$hit" log-and-return \
      "error logged and returned: handle it once — the boundary logs, the middle layers wrap"
  done < <(awk '
      # A log call, not err.Error(): require a logger-ish receiver on the line.
      # The pairing is one-shot so a stale line number cannot match a later return.
      /log/ && /\.(Error|Errorf|Warn|Warnf)\(/ && /err/ { lg = NR }
      # Only a bare "return err" means the error travels on to be handled again.
      # "return c.Redirect(...)" / "return errs.Internal(err)" consumed it or
      # re-classified it, which is the intended shape - so do not flag them.
      /^[[:space:]]*return[[:space:]]+err[[:space:]]*$/ {
        if (lg && NR - lg <= 2) { print NR; lg = 0 }
      }
  ' "$f")
done < <(grep -rlE --include='*.go' '\.(Error|Errorf)\(' "${paths[@]}" 2>/dev/null || true)

# ------------------------------------------------------------------- summary

printf '\n%s hit(s) reported; FAIL rules are decisive, WARN rules need judgement.\n' "$hits"
if [ "$fail" -eq 1 ]; then
  printf 'FAIL: at least one decisive rule fired.\n'
  exit 1
fi
printf 'OK: no FAIL rule fired.\n'
exit 0
