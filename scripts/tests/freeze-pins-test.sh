#!/usr/bin/env bash
#
# Offline tests for scripts/freeze-pins.sh. `git` is stubbed from a fixture
# table, so nothing here touches the network.

set -uo pipefail

script_dir=$(cd "$(dirname "$0")/.." && pwd)
subject="$script_dir/freeze-pins.sh"
tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin"
cat >"$tmp/bin/git" <<'STUB'
#!/usr/bin/env bash
# Stub for `git ls-remote --tags <repo> <ref> [<ref> ...]`.
# Fixture lines are "<repo>\t<ref>\t<sha>"; repos marked "invalid" fail.
repo=""
refs=()
for arg in "$@"; do
    case "$arg" in
        ls-remote|--tags|--) ;;
        *)
            if [ -z "$repo" ]; then
                repo="$arg"
            else
                refs[${#refs[@]}]="$arg"
            fi
            ;;
    esac
done
case "$repo" in
    *invalid*)
        echo "fatal: unable to access '$repo'" >&2
        exit 128
        ;;
esac
for ref in ${refs[@]+"${refs[@]}"}; do
    while IFS=$'\t' read -r frepo fref fsha; do
        [ "$frepo" = "$repo" ] || continue
        [ "$fref" = "$ref" ] || continue
        printf '%s\t%s\n' "$fsha" "$fref"
    done <"${GIT_FIXTURE:?GIT_FIXTURE is not set}"
done
exit 0
STUB
chmod +x "$tmp/bin/git"
export PATH="$tmp/bin:$PATH"

repeat() {
    local count="$1" char="$2" out="" i=0
    while [ "$i" -lt "$count" ]; do
        out="${out}${char}"
        i=$((i + 1))
    done
    printf '%s' "$out"
}

# Fake SHAs carry a letter: a purely numeric 40-character rev is parsed as an
# integer by YAML, which pre-commit rejects as "Expected string got int".
TAG_OBJECT="a$(repeat 39 1)"
COMMIT="b$(repeat 39 2)"
LIGHT_TAG="c$(repeat 39 3)"
for sha in "$TAG_OBJECT" "$COMMIT" "$LIGHT_TAG"; do
    [ "${#sha}" -eq 40 ] || { echo "bad test fixture SHA: ${sha}" >&2; exit 2; }
done

export GIT_FIXTURE="$tmp/fixture"
{
    printf 'https://example.test/annotated\trefs/tags/v1.0.0\t%s\n' "$TAG_OBJECT"
    printf 'https://example.test/annotated\trefs/tags/v1.0.0^{}\t%s\n' "$COMMIT"
    printf 'https://example.test/light\trefs/tags/v2.0.0\t%s\n' "$LIGHT_TAG"
} >"$GIT_FIXTURE"

failures=0
pass() { printf 'ok - %s\n' "$1"; }
fail() {
    printf 'FAIL - %s\n' "$1"
    failures=$((failures + 1))
}

# config <path> <repo> <rev> <frozen tag>
config() {
    cat >"$1" <<EOF
repos:
  - repo: $2
    rev: $3  # frozen: $4
    hooks:
      - id: example
EOF
}

# assert_status <expected exit> <description> <script args...>
assert_status() { assert_status_env "" "$@"; }

# assert_status_env <path prefix> <expected exit> <description> <script args...>
assert_status_env() {
    local prefix="$1" expected="$2" desc="$3" rc
    shift 3
    PATH="${prefix:+${prefix}:}${PATH}" "$subject" "$@" >"$tmp/out" 2>"$tmp/err"
    rc=$?
    if [ "$rc" -eq "$expected" ]; then
        pass "$desc"
    else
        fail "$desc (exit ${rc}, want ${expected})"
        sed 's/^/    /' "$tmp/err" >&2
    fi
}

# assert_grep <pattern> <file> <description>
assert_grep() {
    if grep -q -- "$1" "$2"; then
        pass "$3"
    else
        fail "$3"
    fi
}

# assert_same <file a> <file b> <description>
assert_same() {
    if cmp -s "$1" "$2"; then
        pass "$3"
    else
        fail "$3"
    fi
}

echo "== an annotated tag object is repinned to its commit =="
config "$tmp/annotated.yaml" https://example.test/annotated "$TAG_OBJECT" v1.0.0
assert_status 1 "check exits 1 on drift" --check --file "$tmp/annotated.yaml"
assert_grep "${TAG_OBJECT}.*commit is ${COMMIT}" "$tmp/out" "check names the pinned and the peeled commit"
assert_grep "--config ${tmp}/annotated.yaml" "$tmp/err" "hint keeps the --file path"
assert_status 0 "write exits 0" --file "$tmp/annotated.yaml"
assert_grep "rev: ${COMMIT}" "$tmp/annotated.yaml" "pin holds the commit"
assert_grep "# frozen: v1.0.0" "$tmp/annotated.yaml" "release comment survives"

echo "== a second run is a no-op =="
cp "$tmp/annotated.yaml" "$tmp/annotated.before"
assert_status 0 "write exits 0" --file "$tmp/annotated.yaml"
assert_same "$tmp/annotated.yaml" "$tmp/annotated.before" "file unchanged"

echo "== a lightweight tag resolves to the same commit =="
config "$tmp/light.yaml" https://example.test/light "$LIGHT_TAG" v2.0.0
assert_status 0 "write exits 0" --file "$tmp/light.yaml"
assert_grep "rev: ${LIGHT_TAG}" "$tmp/light.yaml" "pin untouched"

echo "== quoted revisions are parsed and keep their quotes =="
config "$tmp/quoted.yaml" https://example.test/annotated "'${TAG_OBJECT}'" v1.0.0
assert_status 0 "write exits 0" --file "$tmp/quoted.yaml"
assert_grep "rev: '${COMMIT}'" "$tmp/quoted.yaml" "quoted pin rewritten"

echo "== a rev that is not a frozen SHA is an error, not a silent skip =="
config "$tmp/broken.yaml" https://example.test/annotated "a1111" v1.0.0
cp "$tmp/broken.yaml" "$tmp/broken.before"
assert_status 2 "exits 2" --file "$tmp/broken.yaml"
assert_grep "a remote rev must be" "$tmp/err" "reports the bad revision"
assert_same "$tmp/broken.yaml" "$tmp/broken.before" "file unchanged"

echo "== a full SHA without a frozen comment is an error =="
cat >"$tmp/unfrozen.yaml" <<EOF
repos:
  - repo: https://example.test/annotated
    rev: $TAG_OBJECT
    hooks:
      - id: example
EOF
cp "$tmp/unfrozen.yaml" "$tmp/unfrozen.before"
assert_status 2 "check exits 2" --check --file "$tmp/unfrozen.yaml"
assert_grep "a remote rev must be" "$tmp/err" "reports the missing frozen comment"
assert_same "$tmp/unfrozen.yaml" "$tmp/unfrozen.before" "file unchanged"

echo "== a tag-style revision is an error =="
config "$tmp/tagrev.yaml" https://example.test/light "v2.0.0" v2.0.0
cp "$tmp/tagrev.yaml" "$tmp/tagrev.before"
assert_status 2 "check exits 2" --check --file "$tmp/tagrev.yaml"
assert_grep "a remote rev must be" "$tmp/err" "reports the tag-style revision"
assert_same "$tmp/tagrev.yaml" "$tmp/tagrev.before" "file unchanged"

echo "== a failed lookup is not read as a lightweight tag =="
config "$tmp/unreachable.yaml" https://example.test/invalid "$TAG_OBJECT" v1.0.0
cp "$tmp/unreachable.yaml" "$tmp/unreachable.before"
assert_status 2 "exits 2" --file "$tmp/unreachable.yaml"
assert_grep "git ls-remote failed" "$tmp/err" "reports the failed lookup"
assert_same "$tmp/unreachable.yaml" "$tmp/unreachable.before" "file unchanged"

echo "== an option-like repository is refused =="
config "$tmp/injection.yaml" "--upload-pack=touch /tmp/poc" "$TAG_OBJECT" v1.0.0
cp "$tmp/injection.yaml" "$tmp/injection.before"
assert_status 2 "exits 2" --file "$tmp/injection.yaml"
assert_grep "option-like repository" "$tmp/err" "reports the rejected value"
assert_same "$tmp/injection.yaml" "$tmp/injection.before" "file unchanged"

echo "== a failing awk is an error, not a clean run =="
mkdir -p "$tmp/noawk"
printf '#!/usr/bin/env bash\nexit 1\n' >"$tmp/noawk/awk"
chmod +x "$tmp/noawk/awk"
config "$tmp/readable.yaml" https://example.test/annotated "$TAG_OBJECT" v1.0.0
cp "$tmp/readable.yaml" "$tmp/readable.before"
assert_status_env "$tmp/noawk" 2 "exits 2" --file "$tmp/readable.yaml"
assert_grep "could not read" "$tmp/err" "reports the unreadable config"
assert_same "$tmp/readable.yaml" "$tmp/readable.before" "file unchanged"

echo "== a config with no remote entry says so =="
cat >"$tmp/nothing.yaml" <<EOF
repos:
  - repo: local
    hooks:
      - id: local-hook
        name: local hook
        entry: "true"
        language: system
EOF
assert_status 0 "check exits 0" --check --file "$tmp/nothing.yaml"
assert_grep "no remote revisions to verify" "$tmp/out" "says there was nothing to verify"

echo "== a tag-style revision next to a pin is an error =="
cat >"$tmp/mixed.yaml" <<EOF
repos:
  - repo: https://example.test/annotated
    rev: $TAG_OBJECT  # frozen: v1.0.0
    hooks:
      - id: example
  - repo: local
    hooks:
      - id: local-hook
        name: local hook
        entry: "true"
        language: system
  - repo: https://example.test/light
    rev: v2.0.0
    hooks:
      - id: example
EOF
cp "$tmp/mixed.yaml" "$tmp/mixed.before"
assert_status 2 "write exits 2" --file "$tmp/mixed.yaml"
assert_grep "a remote rev must be" "$tmp/err" "reports the tag-style revision"
assert_same "$tmp/mixed.yaml" "$tmp/mixed.before" "file unchanged"

echo "== a local entry alone is skipped =="
cat >"$tmp/localonly.yaml" <<EOF
repos:
  - repo: local
    hooks:
      - id: local-hook
        name: local hook
        entry: "true"
        language: system
EOF
assert_status 0 "write exits 0" --file "$tmp/localonly.yaml"
assert_grep "repo: local" "$tmp/localonly.yaml" "local entry kept"

echo "== a quoted repository is unquoted for the lookup and kept as written =="
config "$tmp/qsingle.yaml" "'https://example.test/annotated'" "$TAG_OBJECT" v1.0.0
assert_status 1 "check exits 1 on drift" --check --file "$tmp/qsingle.yaml"
assert_status 0 "write exits 0" --file "$tmp/qsingle.yaml"
assert_grep "repo: 'https://example.test/annotated'" "$tmp/qsingle.yaml" "single-quoted repo kept"
assert_grep "rev: ${COMMIT}" "$tmp/qsingle.yaml" "single-quoted repo repinned"

config "$tmp/qdouble.yaml" "\"https://example.test/annotated\"" "$TAG_OBJECT" v1.0.0
assert_status 0 "write exits 0" --file "$tmp/qdouble.yaml"
assert_grep "repo: \"https://example.test/annotated\"" "$tmp/qdouble.yaml" "double-quoted repo kept"
assert_grep "rev: ${COMMIT}" "$tmp/qdouble.yaml" "double-quoted repo repinned"

echo "== the repo key does not have to open the entry =="
cat >"$tmp/reordered.yaml" <<EOF
repos:
  - hooks:
      - id: example
    repo: https://example.test/light
    rev: v2.0.0
EOF
cp "$tmp/reordered.yaml" "$tmp/reordered.before"
assert_status 2 "check exits 2" --check --file "$tmp/reordered.yaml"
assert_grep "a remote rev must be" "$tmp/err" "reports the tag-style revision"
assert_same "$tmp/reordered.yaml" "$tmp/reordered.before" "file unchanged"

cat >"$tmp/reorderedpin.yaml" <<EOF
repos:
  - hooks:
      - id: example
    repo: https://example.test/annotated
    rev: $TAG_OBJECT  # frozen: v1.0.0
EOF
assert_status 1 "check exits 1 on drift" --check --file "$tmp/reorderedpin.yaml"
assert_status 0 "write exits 0" --file "$tmp/reorderedpin.yaml"
assert_grep "rev: ${COMMIT}  # frozen: v1.0.0" "$tmp/reorderedpin.yaml" "repinned with the repo key last"

cat >"$tmp/reorderedrev.yaml" <<EOF
repos:
  - hooks:
      - id: example
    rev: $TAG_OBJECT  # frozen: v1.0.0
    repo: https://example.test/annotated
EOF
assert_status 0 "write exits 0" --file "$tmp/reorderedrev.yaml"
assert_grep "rev: ${COMMIT}  # frozen: v1.0.0" "$tmp/reorderedrev.yaml" "repinned with the rev key first"
assert_grep "repo: https://example.test/annotated" "$tmp/reorderedrev.yaml" "repo line kept in place"

echo "== a rev with no repo in its entry is an error, not the previous repo =="
cat >"$tmp/norepo.yaml" <<EOF
repos:
  - repo: https://example.test/annotated
    rev: $TAG_OBJECT  # frozen: v1.0.0
    hooks:
      - id: example
  - hooks:
      - id: example
    rev: $LIGHT_TAG  # frozen: v2.0.0
EOF
cp "$tmp/norepo.yaml" "$tmp/norepo.before"
assert_status 2 "write exits 2" --file "$tmp/norepo.yaml"
assert_grep "a rev outside an entry with a repo" "$tmp/err" "reports the orphaned rev"
assert_same "$tmp/norepo.yaml" "$tmp/norepo.before" "file unchanged"

echo "== unquoting a repository does not bypass the option guard =="
config "$tmp/qinjection.yaml" "'--upload-pack=touch /tmp/poc'" "$TAG_OBJECT" v1.0.0
cp "$tmp/qinjection.yaml" "$tmp/qinjection.before"
assert_status 2 "exits 2" --file "$tmp/qinjection.yaml"
assert_grep "option-like repository" "$tmp/err" "reports the quoted rejected value"
assert_same "$tmp/qinjection.yaml" "$tmp/qinjection.before" "file unchanged"

echo "== a rev on the entry's opening dash line is parsed =="
cat >"$tmp/dashrev.yaml" <<EOF
repos:
  - rev: $TAG_OBJECT  # frozen: v1.0.0
    repo: https://example.test/annotated
    hooks:
      - id: example
EOF
assert_status 1 "check exits 1 on drift" --check --file "$tmp/dashrev.yaml"
assert_status 0 "write exits 0" --file "$tmp/dashrev.yaml"
assert_grep "rev: ${COMMIT}  # frozen: v1.0.0" "$tmp/dashrev.yaml" "dash-line pin repinned"

cat >"$tmp/dashrevtag.yaml" <<EOF
repos:
  - rev: v2.0.0
    repo: https://example.test/light
    hooks:
      - id: example
EOF
cp "$tmp/dashrevtag.yaml" "$tmp/dashrevtag.before"
assert_status 2 "check exits 2" --check --file "$tmp/dashrevtag.yaml"
assert_grep "a remote rev must be" "$tmp/err" "reports the dash-line tag revision"
assert_same "$tmp/dashrevtag.yaml" "$tmp/dashrevtag.before" "file unchanged"

echo "== the default config points the hint at make hooks-update =="
mkdir -p "$tmp/default"
config "$tmp/default/.pre-commit-config.yaml" https://example.test/annotated "$TAG_OBJECT" v1.0.0
run_dir=$PWD
cd "$tmp/default" || exit 2
"$subject" --check >"$tmp/out" 2>"$tmp/err"
rc=$?
cd "$run_dir" || exit 2
if [ "$rc" -eq 1 ]; then
    pass "check exits 1"
else
    fail "check exits 1 (exit ${rc}, want 1)"
fi
assert_grep "make hooks-update" "$tmp/err" "names the make target"

echo "== a quoted root key is still read =="
cat >"$tmp/qroot.yaml" <<EOF
'repos':
  - repo: https://example.test/annotated
    rev: $TAG_OBJECT  # frozen: v1.0.0
    hooks:
      - id: example
EOF
assert_status 1 "check exits 1 on drift" --check --file "$tmp/qroot.yaml"
assert_status 0 "write exits 0" --file "$tmp/qroot.yaml"
assert_grep "rev: ${COMMIT}" "$tmp/qroot.yaml" "quoted root key repinned"

cat >"$tmp/qroottag.yaml" <<EOF
"repos":
  - repo: https://example.test/light
    rev: v2.0.0
    hooks:
      - id: example
EOF
cp "$tmp/qroottag.yaml" "$tmp/qroottag.before"
assert_status 2 "check exits 2" --check --file "$tmp/qroottag.yaml"
assert_grep "a remote rev must be" "$tmp/err" "reports the tag revision"
assert_same "$tmp/qroottag.yaml" "$tmp/qroottag.before" "file unchanged"

echo "== a file without a repos: sequence is an error =="
printf 'something: else\n' >"$tmp/notconfig.yaml"
assert_status 2 "check exits 2" --check --file "$tmp/notconfig.yaml"
assert_grep "no repos: sequence found" "$tmp/err" "reports the missing sequence"

echo "== an empty repos: list verifies nothing =="
printf 'repos: []\n' >"$tmp/emptyrepos.yaml"
assert_status 0 "check exits 0" --check --file "$tmp/emptyrepos.yaml"
assert_grep "no remote revisions to verify" "$tmp/out" "says there was nothing to verify"

echo
if [ "$failures" -ne 0 ]; then
    printf '%d check(s) failed\n' "$failures" >&2
    exit 1
fi
printf 'all checks passed\n'
