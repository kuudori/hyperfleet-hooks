#!/usr/bin/env bash
#
# Rewrite `rev: <sha>  # frozen: <tag>` pins in a pre-commit config to the
# commit the tag peels to.
#
# `pre-commit autoupdate --freeze` pins whatever `git ls-remote <repo> <tag>`
# returns. For an annotated tag that SHA is the tag object, not the commit.
# Renovate reads the `# frozen: <tag>` comment, resolves the tag to the commit,
# and compares it with the pin; the mismatch makes it open a no-op
# "Update ... digest" PR on every scheduled run. Pinning the commit keeps both
# sides in agreement and keeps the release tag in the comment.
#
# Every remote entry must therefore be a full 40-character SHA with a
# `# frozen: <tag>` comment, the shape the pre-commit guide requires; a rev that
# is a tag or a bare SHA without the comment is an error instead of being
# skipped. `repo: local` entries are exempt.
#
# Exit codes: 0 ok, 1 drift found (--check), 2 usage, resolution, format or
# verification error.

set -uo pipefail

CONFIG=".pre-commit-config.yaml"
MODE="write"
CUSTOM_CONFIG=0

usage() {
    cat <<'EOF'
Rewrite `rev: <sha>  # frozen: <tag>` pins to the commit the tag peels to.

Usage:
  scripts/freeze-pins.sh                    # rewrite .pre-commit-config.yaml
  scripts/freeze-pins.sh --check            # report drift, exit 1 if any
  scripts/freeze-pins.sh --file <path>      # operate on another config
  scripts/freeze-pins.sh --check --file <p>

Every remote rev must be '<40-hex SHA>  # frozen: <tag>'; `repo: local`
entries are exempt.

Exit codes: 0 ok, 1 drift found (--check), 2 usage, resolution, format or
verification error.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --check) MODE="check" ;;
        --file)
            [ $# -ge 2 ] || { echo "ERROR: --file needs a path" >&2; exit 2; }
            CONFIG="$2"
            CUSTOM_CONFIG=1
            shift
            ;;
        -h|--help) usage; exit 0 ;;
        *) echo "ERROR: unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
    shift
done

[ -f "$CONFIG" ] || { echo "ERROR: no such file: $CONFIG" >&2; exit 2; }
command -v git >/dev/null 2>&1 || { echo "ERROR: git is required" >&2; exit 2; }

workdir=$(mktemp -d) || exit 2
trap 'rm -rf "$workdir"' EXIT
pins="$workdir/pins"
resolved="$workdir/resolved"
changes="$workdir/changes"
: >"$resolved"
: >"$changes"

# awks share the same view of an entry: YAML mapping keys have no fixed order, so
# both passes track the `repos:` sequence and reset the repository at each entry
# instead of trusting `repo:` to open it. `unquote()` gives both passes the same
# key, and `rev` matches only the shape the pre-commit guide requires, so every
# other remote rev is reported rather than skipped.
AWK_QUOTES='
    function unquote(value) {
        if (length(value) >= 2) {
            first = substr(value, 1, 1)
            last = substr(value, length(value), 1)
            if (first == last && first ~ quote) {
                return substr(value, 2, length(value) - 2)
            }
        }
        return value
    }
    function sha_of(line) {
        sha = line
        sub(/^[[:space:]]*(-[[:space:]]*)?rev:[[:space:]]*/, "", sha)
        sub(/[[:space:]]*#.*/, "", sha)
        gsub(quote, "", sha)
        return sha
    }
    function tag_of(line) {
        tag = line
        sub(/.*frozen:[[:space:]]*/, "", tag)
        return tag
    }
    BEGIN {
        quote = "[\"" sq "]"
        optional = quote "?"
        entry_indent = -1
        repo_re = "^[[:space:]]*(-[[:space:]]*)?repo:[[:space:]]*(\"[^\"]*\"|" sq "[^" sq "]*" sq "|[^[:space:]#]+)"
        rev = "^[[:space:]]*(-[[:space:]]*)?rev:[[:space:]]*" optional "[0-9a-fA-F]+" optional \
              "[[:space:]]*#[[:space:]]*frozen:[[:space:]]*[^[:space:]]+"
        rev_line = "^[[:space:]]*(-[[:space:]]*)?rev:[[:space:]]*[^[:space:]#]+"
        repos_re = "^[[:space:]]*" optional "repos" optional "[[:space:]]*:[[:space:]]*(\\[\\][[:space:]]*)?(#.*)?$"
    }
'

# Emit "OK<TAB>repo<TAB>tag<TAB>sha" per conforming pin, "BAD<TAB>repo<TAB>raw
# line" per remote rev that is not one, "NOREPO<TAB>-<TAB>raw line" per rev in an
# entry without a repository, and "NOREPOS<TAB>-<TAB>-" when the file has no
# repos: sequence at all.
awk -v sq="'" "$AWK_QUOTES"'
    function flush_pin() {
        if (pending == "") return
        if (repo == "local") {
            pending = ""
            return
        }
        if (repo == "") {
            printf "NOREPO\t-\t%s\n", pending
        } else if (pending ~ rev) {
            sha = sha_of(pending)
            if (length(sha) == 40) {
                printf "OK\t%s\t%s\t%s\n", repo, tag_of(pending), sha
            } else {
                printf "BAD\t%s\t%s\n", repo, pending
            }
        } else {
            printf "BAD\t%s\t%s\n", repo, pending
        }
        pending = ""
    }
    {
        if ($0 ~ repos_re) {
            in_repos = 1
            saw_repos = 1
            next
        }
        if (!in_repos) next
        if (match($0, /^[[:space:]]*-/)) {
            dash = RLENGTH - 1
            if (entry_indent < 0) entry_indent = dash
            if (dash == entry_indent) {
                flush_pin()
                repo = ""
            }
        }
        if (match($0, repo_re)) {
            value = substr($0, RSTART, RLENGTH)
            sub(/^[[:space:]]*(-[[:space:]]*)?repo:[[:space:]]*/, "", value)
            repo = unquote(value)
            next
        }
        if (match($0, rev) || match($0, rev_line)) {
            pending = $0
        }
    }
    END {
        flush_pin()
        if (!saw_repos) printf "NOREPOS\t-\t-\n"
    }
' "$CONFIG" >"$pins" || { echo "ERROR: could not read ${CONFIG}" >&2; exit 2; }

# Print the commit the tag peels to. Fetches the tag ref and its peeled ref in
# one request, and only falls back to the unpeeled ref when that successful
# response carries no peeled entry: a failed request is an error, not a
# lightweight tag.
resolve_commit() {
    local repo="$1" tag="$2" out
    out=$(git ls-remote --tags -- "$repo" "refs/tags/${tag}" "refs/tags/${tag}^{}") || return 1
    printf '%s\n' "$out" | awk -v base="refs/tags/${tag}" '
        $2 == base "^{}" { peeled = $1 }
        $2 == base { unpeeled = $1 }
        END {
            if (peeled != "") print peeled
            else if (unpeeled != "") print unpeeled
        }'
}

drift=0
unresolved=0
unparsable=0
checked=0

while IFS=$'\t' read -r kind repo rest; do
    case "$kind" in
        BAD)
            echo "ERROR: ${CONFIG}: ${repo}: a remote rev must be '<40-hex SHA>  # frozen: <tag>': ${rest}" >&2
            unparsable=1
            ;;
        NOREPO)
            echo "ERROR: ${CONFIG}: a rev outside an entry with a repo: ${rest}" >&2
            unparsable=1
            ;;
        NOREPOS)
            echo "ERROR: ${CONFIG}: no repos: sequence found; expected a pre-commit config" >&2
            unparsable=1
            ;;
        OK)
            tag="${rest%%$'\t'*}"
            sha="${rest#*$'\t'}"
            case "$repo" in
                -*)
                    echo "ERROR: refusing an option-like repository at ${CONFIG}: ${repo}" >&2
                    unparsable=1
                    continue
                    ;;
            esac
            if ! commit=$(resolve_commit "$repo" "$tag"); then
                echo "WARN: git ls-remote failed for ${repo}; leaving ${sha} untouched" >&2
                unresolved=1
                continue
            fi
            if [ -z "$commit" ]; then
                echo "WARN: cannot resolve ${tag} in ${repo}; leaving ${sha} untouched" >&2
                unresolved=1
                continue
            fi
            [ "$commit" = "$sha" ] || drift=1
            checked=$((checked + 1))
            printf '%s\t%s\t%s\t%s\n' "$repo" "$tag" "$sha" "$commit" >>"$resolved"
            ;;
    esac
done <"$pins"

if [ "$unparsable" -ne 0 ]; then
    echo "ERROR: fix the revisions above before repinning; nothing was changed" >&2
    exit 2
fi

if [ "$drift" -eq 0 ]; then
    if [ "$unresolved" -ne 0 ]; then
        echo "WARN: ${CONFIG}: no drift among the pins that could be read, but some could not be verified" >&2
        exit 2
    fi
    if [ "$checked" -eq 0 ]; then
        echo "${CONFIG}: no remote revisions to verify"
        exit 0
    fi
    [ "$MODE" = "check" ] && echo "${CONFIG}: all pins point at commits"
    [ "$MODE" = "write" ] && echo "${CONFIG}: all pins point at commits, nothing to do"
    exit 0
fi

while IFS=$'\t' read -r repo tag sha commit; do
    [ "$sha" = "$commit" ] && continue
    printf '%s\t%s\t%s\t%s\n' "$repo" "$tag" "$sha" "$commit" >>"$changes"
done <"$resolved"

if [ "$MODE" = "check" ]; then
    while IFS=$'\t' read -r repo tag sha commit; do
        echo "${CONFIG}: ${repo} ${tag} is pinned to ${sha}, commit is ${commit}"
    done <"$changes"
    if [ "$CUSTOM_CONFIG" -eq 1 ]; then
        echo "ERROR: repin with 'pre-commit autoupdate --freeze --config ${CONFIG} && $0 --file ${CONFIG}'" >&2
    else
        echo "ERROR: repin with 'make hooks-update'" >&2
    fi
    [ "$unresolved" -eq 0 ] || exit 2
    exit 1
fi

# Rewrite only the rev lines whose repo, tag and current SHA match a change. The
# output is buffered because a rev can precede its repo: the line is patched once
# the entry ends and its repository is known.
awk -v sq="'" -v changes="$changes" "$AWK_QUOTES"'
    function flush_pin(   key) {
        if (pending == 0) return
        if (repo != "" && repo != "local") {
            key = repo "|" pending_tag "|" pending_sha
            if (key in map) sub(pending_sha, map[key], lines[pending])
        }
        pending = 0
    }
    BEGIN {
        FS = "\t"
        while ((getline line < changes) > 0) {
            split(line, field, "\t")
            map[field[1] "|" field[2] "|" field[3]] = field[4]
        }
    }
    {
        lines[++count] = $0
        if ($0 ~ repos_re) in_repos = 1
        if (in_repos && match($0, /^[[:space:]]*-/)) {
            dash = RLENGTH - 1
            if (entry_indent < 0) entry_indent = dash
            if (dash == entry_indent) {
                flush_pin()
                repo = ""
            }
        }
        if (in_repos && match($0, repo_re)) {
            value = substr($0, RSTART, RLENGTH)
            sub(/^[[:space:]]*(-[[:space:]]*)?repo:[[:space:]]*/, "", value)
            repo = unquote(value)
            flush_pin()
        } else if (in_repos && match($0, rev)) {
            pending = count
            pending_sha = sha_of($0)
            pending_tag = tag_of($0)
        }
    }
    END {
        flush_pin()
        for (i = 1; i <= count; i++) print lines[i]
    }
' "$CONFIG" >"$workdir/config.yaml" || { echo "ERROR: rewrite failed" >&2; exit 2; }

# Verify the rewritten file before it replaces the original.
"$0" --check --file "$workdir/config.yaml" >/dev/null 2>&1
verify=$?
case "$verify" in
    0) ;;
    1)
        echo "ERROR: the rewrite left non-commit pins in ${CONFIG}; nothing changed" >&2
        exit 2
        ;;
    2)
        echo "WARN: some pins could not be verified, review the warnings above" >&2
        unresolved=1
        ;;
    *)
        echo "ERROR: verification of the rewrite failed (exit ${verify})" >&2
        exit 2
        ;;
esac

if command -v pre-commit >/dev/null 2>&1; then
    pre-commit validate-config "$workdir/config.yaml" || {
        echo "ERROR: the rewrite does not produce a valid pre-commit config; nothing changed" >&2
        exit 2
    }
fi

if ! mv "$workdir/config.yaml" "$CONFIG"; then
    echo "ERROR: could not replace ${CONFIG}; it was left unchanged" >&2
    exit 2
fi

while IFS=$'\t' read -r repo tag sha commit; do
    echo "normalized ${tag} (${repo}): ${sha:0:12} -> ${commit:0:12}"
done <"$changes"

[ "$unresolved" -eq 0 ] || exit 2
exit 0
