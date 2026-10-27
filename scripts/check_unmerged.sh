#!/usr/bin/env bash

set -e

TARGET=${1:-$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "main")}

echo "Checking for commits not merged into: $TARGET"
echo "--------------------------------------------------------"

# Get all local and remote branches
branches=$(git for-each-ref --format='%(refname:short)' refs/heads refs/remotes | grep -v 'HEAD' | sort -u)

found=0

for branch in $branches; do
    if [ "$branch" = "$TARGET" ] || [[ "$branch" == "origin/$TARGET" ]]; then
        continue
    fi

    # Count commits in 'branch' that are not in 'TARGET'
    count=$(git rev-list --count "$TARGET..$branch" 2>/dev/null || echo 0)

    if [ "$count" -gt 0 ]; then
        last_date=$(git log -1 --format="%cd" --date=short "$branch")
        last_author=$(git log -1 --format="%an" "$branch")
        printf "Branch \033[1;34m%-35s\033[0m has \033[1;31m%3d\033[0m unmerged commit(s). (Last: %s by %s)\n" "$branch" "$count" "$last_date" "$last_author"
        found=1
    fi
done

if [ $found -eq 0 ]; then
    echo "No unmerged commits found! All branches are merged into $TARGET."
fi
