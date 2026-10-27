#!/usr/bin/env bash

TARGET=${1:-$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "main")}

echo "Interactive merge for commits not merged into: $TARGET"
echo "--------------------------------------------------------"

# Ensure we're on the target branch
current_branch=$(git rev-parse --abbrev-ref HEAD)
if [ "$current_branch" != "$TARGET" ]; then
    echo "Checking out $TARGET..."
    git checkout "$TARGET"
fi

branches=$(git for-each-ref --format='%(refname:short)' refs/heads refs/remotes | grep -v 'HEAD' | sort -u)

found=0

for branch in $branches; do
    if [ "$branch" = "$TARGET" ] || [[ "$branch" == "origin/$TARGET" ]]; then
        continue
    fi

    count=$(git rev-list --count "$TARGET..$branch" 2>/dev/null || echo 0)

    if [ "$count" -gt 0 ]; then
        last_date=$(git log -1 --format="%cd" --date=short "$branch")
        last_author=$(git log -1 --format="%an" "$branch")
        echo ""
        printf "Branch \033[1;34m%-35s\033[0m has \033[1;31m%3d\033[0m unmerged commit(s). (Last: %s by %s)\n" "$branch" "$count" "$last_date" "$last_author"

        while true; do
            read -p "Merge this branch? (y/n/q to quit): " yn </dev/tty
            case $yn in
                [Yy]* )
                    echo "Merging $branch..."
                    if git merge --no-edit "$branch"; then
                        echo "✅ Successfully merged $branch."
                    else
                        echo "❌ Merge conflict! Please resolve conflicts, commit, and run this script again."
                        exit 1
                    fi
                    break;;
                [Nn]* )
                    echo "Skipping $branch."
                    break;;
                [Qq]* )
                    echo "Exiting."
                    exit 0;;
                * ) echo "Please answer yes (y), no (n), or quit (q).";;
            esac
        done
        found=1
    fi
done

if [ $found -eq 0 ]; then
    echo "No unmerged commits found! All branches are merged into $TARGET."
fi
