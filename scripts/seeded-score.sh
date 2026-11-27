#!/usr/bin/env bash
# Runs review-bot against the seeded-bugs PR and scores it against the answer
# key in internal/seeded/EXPECTED.md.
#
#   scripts/seeded-score.sh -p 8                 # run the bot, then score
#   scripts/seeded-score.sh -l seeded-run.log    # score an existing log
#
# Options:
#   -p PR         pull request with the seeded code (required to run)
#   -b BIN        review-bot binary (default: bin/review-bot)
#   -l LOG        score this log instead of running the bot
#   -k KEY        answer key (default: internal/seeded/EXPECTED.md)
#   -w LINES      how far a comment may be from a planted line (default: 4)
#   -m MIN        exit 1 if fewer than MIN problems are caught
#   -f MAX        exit 1 if there are more than MAX false positives
#
# Model settings (LLM_MODEL, LLM_BASE_URL, REVIEW_PROFILE, ...) are passed
# through from the environment. The run is always a dry run: nothing is posted.
set -euo pipefail

pr="" bin="bin/review-bot" log="" key="internal/seeded/EXPECTED.md" window=4 min="" maxfp=""
while getopts "p:b:l:k:w:m:f:h" opt; do
  case $opt in
    p) pr=$OPTARG ;; b) bin=$OPTARG ;; l) log=$OPTARG ;; k) key=$OPTARG ;;
    w) window=$OPTARG ;; m) min=$OPTARG ;; f) maxfp=$OPTARG ;;
    *) sed -n '2,20p' "$0"; exit 2 ;;
  esac
done

[ -f "$key" ] || { echo "answer key not found: $key" >&2; exit 2; }

if [ -z "$log" ]; then
  [ -n "$pr" ] || { echo "give -p PR to run the bot, or -l LOG to score a log" >&2; exit 2; }
  [ -x "$bin" ] || { echo "review-bot binary not found: $bin (build it, or pass -b)" >&2; exit 2; }
  repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
  log="seeded-run-$(date +%Y%m%d-%H%M%S).log"
  echo "running $bin on $repo#$pr (log: $log)…" >&2
  GITHUB_TOKEN=${GITHUB_TOKEN:-$(gh auth token)} REVIEW_DRAFTS=true \
    REVIEW_IGNORE="${REVIEW_IGNORE:+$REVIEW_IGNORE,}scripts/**" \
    "$bin" -repo "$repo" -pr "$pr" -dry-run -force >"$log" 2>&1 || {
      echo "review-bot failed; last lines of $log:" >&2; tail -5 "$log" >&2; exit 2; }
fi

# Comments from the dry run: "line severity category" for each finding on store.go.
comments=$(awk '
  /dry run: would comment on internal\/seeded\/store\.go:/ {
    n = $0; sub(/.*store\.go:/, "", n); sub(/[^0-9].*/, "", n)
    b = ""; getline b
    sev = "?"; cat = "?"
    if (b ~ /\*\*Critical/) sev = "critical"; else if (b ~ /\*\*High/) sev = "high"
    else if (b ~ /\*\*Medium/) sev = "medium"; else if (b ~ /\*\*Low/) sev = "low"
    if (b ~ / Bugs\*\*/) cat = "bugs"; else if (b ~ / Security\*\*/) cat = "security"
    print n, sev, cat
  }' "$log")

# The answer key's tables: planted rows "| B1 | 38 | bugs | high | ..." (the
# line column may list alternatives: "16, 165"), decoy rows "| D1 | 54–62 | ...".
planted=$(sed 's/–/-/g' "$key" | awk -F'|' '
  $2 ~ /^ *[BS][0-9]+ *$/ { gsub(/ /, "", $2); gsub(/ /, "", $3); gsub(/ /, "", $4); gsub(/ /, "", $5); print $2, $3, $4, $5 }')
decoys=$(sed 's/–/-/g' "$key" | awk -F'|' '
  $2 ~ /^ *D[0-9]+ *$/ { gsub(/ /, "", $2); gsub(/ /, "", $3); print $2, $3 }')

[ -n "$planted" ] || { echo "no planted rows found in $key" >&2; exit 2; }

result=$(awk -v window="$window" -v P="$planted" -v D="$decoys" -v C="$comments" '
  function abs(x) { return x < 0 ? -x : x }
  BEGIN {
    np = split(P, prow, "\n"); nd = split(D, drow, "\n"); nc = (C == "") ? 0 : split(C, crow, "\n")
    for (i = 1; i <= nc; i++) { split(crow[i], f, " "); cl[i] = f[1]; cs[i] = f[2]; cc[i] = f[3]; used[i] = 0 }
    caught = 0
    printf "%-4s %-9s %-22s %-22s %s\n", "ID", "LINE", "EXPECTED", "GOT", "RESULT"
    for (i = 1; i <= np; i++) {
      split(prow[i], f, " "); id = f[1]; lines = f[2]; cat = f[3]; sev = f[4]
      nl = split(lines, L, ",")
      best = 0; bestd = window + 1
      for (j = 1; j <= nc; j++) {
        if (used[j]) continue
        for (k = 1; k <= nl; k++) { d = abs(cl[j] - L[k]); if (d < bestd) { bestd = d; best = j } }
      }
      want = cat "/" sev
      if (best && bestd <= window) {
        used[best] = 1; caught++
        got = cc[best] "/" cs[best]
        note = "caught (line " cl[best] ")"
        if (cc[best] != cat) note = note ", labelled " cc[best]
        if (index("/" sev "/", "/" cs[best] "/") == 0) note = note ", severity " cs[best]
        printf "%-4s %-9s %-22s %-22s %s\n", id, lines, want, got, note
      } else {
        printf "%-4s %-9s %-22s %-22s %s\n", id, lines, want, "-", "MISSED"
      }
    }
    decoyhits = 0; other = 0; extra = ""
    for (j = 1; j <= nc; j++) {
      if (used[j]) continue
      hit = ""
      for (k = 1; k <= nd; k++) {
        split(drow[k], f, " "); split(f[2], r, "-"); lo = r[1]; hi = (r[2] == "") ? r[1] : r[2]
        if (cl[j] >= lo && cl[j] <= hi) hit = f[1]
      }
      if (hit != "") { decoyhits++; extra = extra sprintf("  decoy %s: comment on line %s (%s/%s)\n", hit, cl[j], cc[j], cs[j]) }
      else { other++; extra = extra sprintf("  extra: comment on line %s (%s/%s)\n", cl[j], cc[j], cs[j]) }
    }
    printf "\n"
    if (extra != "") printf "False positives:\n%s\n", extra
    printf "SCORE caught=%d planted=%d decoy_hits=%d other=%d\n", caught, np, decoyhits, other
  }')

echo "$result" | grep -v '^SCORE'
read -r _ caught planted_n decoy_hits other <<<"$(echo "$result" | grep '^SCORE' | tr '=' ' ' | awk '{print $1, $3, $5, $7, $9}')"
fp=$((decoy_hits + other))
echo "Caught $caught/$planted_n, false positives $fp ($decoy_hits on decoys)"
echo "Log: $log"

status=0
if [ -n "$min" ] && [ "$caught" -lt "$min" ]; then echo "FAIL: caught fewer than $min" >&2; status=1; fi
if [ -n "$maxfp" ] && [ "$fp" -gt "$maxfp" ]; then echo "FAIL: more than $maxfp false positives" >&2; status=1; fi
exit $status
