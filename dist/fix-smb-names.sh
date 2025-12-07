#!/bin/bash
# fix-smb-names.sh - Find and optionally fix filenames with SMB-incompatible characters

set -e

usage() {
    cat <<'EOF'
fix-smb-names.sh - Fix filenames incompatible with SMB/Windows

PROBLEM:
  Windows/SMB cannot handle certain characters in filenames:
    : (colon)  * (asterisk)  ? (question)  " (quote)  < > |

  Samba will "mangle" these into unreadable names like "3U0D2T~Q".
  This script finds files with : or ? and can rename them.

  Run this on a Linux machine where the files are stored locally.

USAGE:
  ./fix-smb-names.sh scan <path>     Scan and report problematic files
  ./fix-smb-names.sh rename <path>   Rename files (: -> -, ? removed)

EXAMPLES:
  ./fix-smb-names.sh scan /mnt/movies
  ./fix-smb-names.sh rename /mnt/movies

EOF
    exit 0
}

if [[ $# -lt 2 || "$1" == "-h" || "$1" == "--help" ]]; then
    usage
fi

ACTION="$1"
TARGET="$2"

if [[ "$ACTION" != "scan" && "$ACTION" != "rename" ]]; then
    echo "Error: First argument must be 'scan' or 'rename'" >&2
    echo "Run with --help for usage" >&2
    exit 1
fi

if [[ ! -d "$TARGET" ]]; then
    echo "Error: $TARGET is not a directory" >&2
    exit 1
fi

# Find files/dirs with : or ? in their names
count=0
while IFS= read -r -d '' path; do
    name=$(basename "$path")

    # Skip if name doesn't contain bad chars (shouldn't happen due to regex, but safety check)
    if [[ "$name" != *:* && "$name" != *\?* ]]; then
        continue
    fi

    ((count++)) || true

    # Calculate new name: replace : with -, remove ?
    newname="${name//:/-}"
    newname="${newname//\?/}"

    dir=$(dirname "$path")
    newpath="$dir/$newname"

    if [[ "$ACTION" == "rename" ]]; then
        if [[ -e "$newpath" ]]; then
            echo "SKIP (target exists): $path -> $newname" >&2
        else
            mv "$path" "$newpath"
            echo "RENAMED: $path -> $newname"
        fi
    else
        echo "$path"
        echo "  -> $newname"
    fi
done < <(find "$TARGET" -depth -regex '.*[:\?].*' -print0)

echo ""
echo "Found $count items with problematic characters"

if [[ "$ACTION" == "scan" && $count -gt 0 ]]; then
    echo "To rename, run:"
    echo "  $0 rename \"$TARGET\""
fi
