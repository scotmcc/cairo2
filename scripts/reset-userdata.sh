#!/bin/bash
# Reset user-generated data in cairo.db while preserving config, roles,
# aspects, prompts, skills, custom tools, and hooks. Useful between test runs.
#
# Usage:
#   scripts/reset-userdata.sh             # interactive confirm
#   scripts/reset-userdata.sh --yes       # skip confirm
#   scripts/reset-userdata.sh --db PATH   # override DB path
#   scripts/reset-userdata.sh --dry-run   # show row counts, change nothing

set -euo pipefail

DB="${CAIRO_DATA_DIR:-$HOME/.cairo}/cairo.db"
ASSUME_YES=0
DRY_RUN=0

while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y) ASSUME_YES=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --db) DB="$2"; shift 2 ;;
    --help|-h)
      sed -n '2,11p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [ ! -f "$DB" ]; then
  echo "no DB at $DB" >&2; exit 1
fi

# Tables to wipe — user-generated data only.
WIPE=(
  sessions
  messages
  consider_activations
  summaries
  memories
  facts
  dreams
  dream_log
  tasks
  task_artifacts
  jobs
  worktrees
  notes
  indexed_files
  indexed_chunks
  code_index
  state_daily
  projects
)

# FTS shadow tables — emptied via DELETE on the parent above when the FTS is
# external-content; we issue explicit DELETEs to be safe across both styles.
FTS=(
  memories_fts
  notes_fts
  skills_fts   # skills_fts is rebuilt from skills (kept), so this is a no-op for kept rows
)

echo "DB: $DB"
echo
echo "Row counts before:"
for t in "${WIPE[@]}"; do
  n=$(sqlite3 "$DB" "SELECT COUNT(*) FROM $t" 2>/dev/null || echo "?")
  printf "  %-20s %s\n" "$t" "$n"
done

if [ $DRY_RUN -eq 1 ]; then
  echo
  echo "(dry-run — no changes)"
  exit 0
fi

if [ $ASSUME_YES -ne 1 ]; then
  echo
  read -r -p "Wipe these tables? Config / roles / aspects / prompts / skills are kept. [y/N] " ans
  case "$ans" in
    y|Y|yes|YES) ;;
    *) echo "aborted"; exit 0 ;;
  esac
fi

# Filter WIPE to tables that actually exist — older DBs may predate newer
# migrations (e.g. consider_activations was added in v0.3.0). Without this
# guard, set -e aborts mid-wipe on a missing table.
EXISTING=()
for t in "${WIPE[@]}"; do
  exists=$(sqlite3 "$DB" "SELECT 1 FROM sqlite_master WHERE type='table' AND name='$t' LIMIT 1;")
  if [ "$exists" = "1" ]; then
    EXISTING+=("$t")
  fi
done

# Build the SQL: foreign keys off, delete, vacuum, foreign keys on.
SQL="PRAGMA foreign_keys = OFF;
BEGIN;
"
for t in "${EXISTING[@]}"; do
  SQL+="DELETE FROM $t;
"
done
# Reset autoincrement counters so new sessions start at id=1
NAMELIST=""
for t in "${EXISTING[@]}"; do
  if [ -z "$NAMELIST" ]; then
    NAMELIST="'$t'"
  else
    NAMELIST="$NAMELIST,'$t'"
  fi
done
SQL+="DELETE FROM sqlite_sequence WHERE name IN ($NAMELIST);
COMMIT;
PRAGMA foreign_keys = ON;
VACUUM;
"

sqlite3 "$DB" <<< "$SQL"

echo
echo "Done. Row counts after:"
for t in "${WIPE[@]}"; do
  n=$(sqlite3 "$DB" "SELECT COUNT(*) FROM $t" 2>/dev/null || echo "?")
  printf "  %-20s %s\n" "$t" "$n"
done
