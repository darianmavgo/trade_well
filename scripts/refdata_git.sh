#!/usr/bin/env bash
# Decide which refdata databases git may track.
#
# GitHub warns above 50 MiB and rejects a file above 100 MiB, so the default
# limit is 50 MiB (override with REFDATA_MAX_BYTES). A database at or under
# the limit is un-ignored. A larger one is named in .gitignore, then zipped
# beside the original. The zip is un-ignored when it is under the limit.
# SQLite -wal/-shm files stay ignored. zip(1) is the only extra step; this
# does not call git-lfs or any other backup tool.
#
# Usage: scripts/refdata_git.sh [refdata-dir]
# The directory must live inside this git repo. Paths in .gitignore are
# relative to the repo root.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

dir=${1:-refdata}
dir=${dir#./}
dir=${dir%/}
max_bytes=${REFDATA_MAX_BYTES:-$((50 * 1024 * 1024))}

if [[ "$dir" = /* || "$dir" = ".." || "$dir" == ../* ]]; then
	echo "refdata dir must be inside the repo, got: $dir" >&2
	exit 2
fi
if [[ ! -d "$dir" ]]; then
	echo "No $dir directory. Nothing to do."
	exit 0
fi

begin="# BEGIN refdata-git"
end="# END refdata-git"

file_bytes() {
	wc -c <"$1" | tr -d '[:space:]'
}

human() {
	local n=$1
	if ((n >= 1048576)); then
		awk -v n="$n" 'BEGIN { printf "%.1f MiB", n / 1048576 }'
	elif ((n >= 1024)); then
		awk -v n="$n" 'BEGIN { printf "%.0f KiB", n / 1024 }'
	else
		printf "%d B" "$n"
	fi
}

# Fold a non-empty WAL into the database so the .db file is the whole snapshot.
# Returns 1 when a WAL remains and could not be folded in.
checkpoint() {
	local db=$1 wal="${1}-wal"
	if [[ ! -s "$wal" ]]; then
		return 0
	fi
	if ! command -v sqlite3 >/dev/null 2>&1; then
		echo "  $db has a WAL and sqlite3 is not installed; size includes the WAL" >&2
		return 1
	fi
	sqlite3 "$db" "PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null
}

measure() {
	local db=$1 bytes wal="${1}-wal"
	bytes=$(file_bytes "$db")
	if [[ -s "$wal" ]]; then
		bytes=$((bytes + $(file_bytes "$wal")))
	fi
	printf "%s" "$bytes"
}

dbs=()
while IFS= read -r db; do
	[[ -n "$db" ]] || continue
	dbs+=("$db")
done < <(find "$dir" -type f \( -name '*.db' -o -name '*.sqlite' \) | sort)

keepfile=$(mktemp)
rulesfile=$(mktemp)
trap 'rm -f "$keepfile" "$rulesfile"' EXIT

too_big=0
if ((${#dbs[@]} == 0)); then
	echo "No databases in $dir."
else
	echo "Reference databases in $dir (limit $(human "$max_bytes")):"
fi

for db in "${dbs[@]+"${dbs[@]}"}"; do
	checkpoint "$db" || true
	bytes=$(measure "$db")
	zipfile="${db}.zip"

	if ((bytes <= max_bytes)); then
		printf '  %-40s %10s  track the database\n' "$db" "$(human "$bytes")"
		printf '!%s\n' "$db" >>"$rulesfile"
		printf '%s\n' "$db" >>"$keepfile"
		if [[ -f "$zipfile" ]]; then
			rm -f "$zipfile"
			echo "  removed $zipfile (the database itself fits)"
		fi
		continue
	fi

	echo "  $db is $(human "$bytes"), over the limit; ignoring that file" >&2
	printf '%s\n' "$db" >>"$rulesfile"

	inputs=("$db")
	if [[ -s "${db}-wal" ]]; then
		inputs+=("${db}-wal")
	fi
	if [[ ! -f "$zipfile" || "$db" -nt "$zipfile" ]]; then
		rm -f "$zipfile"
		zip -q -j -9 "$zipfile" "${inputs[@]}"
	fi
	zbytes=$(file_bytes "$zipfile")
	if ((zbytes <= max_bytes)); then
		printf '  %-40s %10s  track %s (%s)\n' \
			"$db" "$(human "$bytes")" "$zipfile" "$(human "$zbytes")"
		printf '!%s\n' "$zipfile" >>"$rulesfile"
		printf '%s\n' "$zipfile" >>"$keepfile"
	else
		printf '  %-40s %10s  zip is %s, still over the limit; both stay ignored\n' \
			"$db" "$(human "$bytes")" "$(human "$zbytes")"
		printf '%s\n' "$zipfile" >>"$rulesfile"
		too_big=1
	fi
done

while IFS= read -r zipfile; do
	[[ -n "$zipfile" ]] || continue
	if [[ ! -f "${zipfile%.zip}" ]]; then
		rm -f "$zipfile"
		echo "removed orphan $zipfile"
	fi
done < <(find "$dir" -type f \( -name '*.db.zip' -o -name '*.sqlite.zip' \))

# Replace the managed block at the end of .gitignore. Last match wins, so
# these lines sit below the blanket *.db rule.
tmp=$(mktemp)
if [[ -f .gitignore ]]; then
	skip=0
	while IFS= read -r line || [[ -n "$line" ]]; do
		if [[ "$line" == "$begin" ]]; then
			skip=1
			continue
		fi
		if [[ "$line" == "$end" ]]; then
			skip=0
			continue
		fi
		if ((skip)); then
			continue
		fi
		printf '%s\n' "$line" >>"$tmp"
	done <.gitignore
	while [[ -s "$tmp" ]]; do
		last=$(tail -n 1 "$tmp")
		[[ -n "$last" ]] && break
		nlines=$(wc -l <"$tmp" | tr -d '[:space:]')
		if ((nlines <= 1)); then
			: >"$tmp"
			break
		fi
		head -n $((nlines - 1)) "$tmp" >"${tmp}.trim"
		mv "${tmp}.trim" "$tmp"
	done
fi

{
	printf '\n%s\n' "$begin"
	printf '# Managed by scripts/refdata_git.sh. A database or zip at or under %s bytes is tracked.\n' "$max_bytes"
	if [[ -s "$rulesfile" ]]; then
		cat "$rulesfile"
	fi
	printf '%s\n' "$end"
} >>"$tmp"
mv "$tmp" .gitignore

while IFS= read -r tracked; do
	[[ -n "$tracked" ]] || continue
	case "$tracked" in
	*.db | *.sqlite | *.db.zip | *.sqlite.zip)
		if ! grep -Fxq -- "$tracked" "$keepfile"; then
			git rm -q --cached --ignore-unmatch -- "$tracked"
			echo "untracked $tracked"
		fi
		;;
	esac
done < <(git ls-files -- "$dir")

git add -- .gitignore
if [[ -s "$keepfile" ]]; then
	while IFS= read -r path; do
		[[ -n "$path" ]] || continue
		git add -- "$path"
	done <"$keepfile"
fi

echo
if ((too_big)); then
	echo "Some reference files are over $(human "$max_bytes") even after zip. They stay on disk and out of git."
	exit 1
fi
echo "Reference data that fits is staged. Commit when you want it on GitHub."
exit 0
