package commitlint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	pkgcommitlint "github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/commitlint"
	"github.com/stretchr/testify/require"
)

func TestValidateSHA(t *testing.T) {
	tests := []struct {
		name    string
		sha     string
		wantErr bool
	}{
		{
			name:    "valid: full SHA (40 chars)",
			sha:     "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
			wantErr: false,
		},
		{
			name:    "valid: short SHA (7 chars)",
			sha:     "a1b2c3d",
			wantErr: false,
		},
		{
			name:    "valid: medium SHA (12 chars)",
			sha:     "a1b2c3d4e5f6",
			wantErr: false,
		},
		{
			name:    "invalid: empty SHA",
			sha:     "",
			wantErr: true,
		},
		{
			name:    "invalid: too short (6 chars)",
			sha:     "a1b2c3",
			wantErr: true,
		},
		{
			name:    "invalid: too long (41 chars)",
			sha:     "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b01",
			wantErr: true,
		},
		{
			name:    "invalid: contains uppercase",
			sha:     "A1B2C3D4E5F6",
			wantErr: true,
		},
		{
			name:    "invalid: contains non-hex",
			sha:     "g1h2i3j4k5l6",
			wantErr: true,
		},
		{
			name:    "invalid: contains spaces",
			sha:     "a1b2c3d e5f6",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSHA(tt.sha)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSHA() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParsePullRefs(t *testing.T) {
	tests := []struct {
		name     string
		pullRefs string
		wantBase string
		wantPR   string
		wantErr  bool
	}{
		{
			name:     "valid: standard Prow format",
			pullRefs: "main:abc123,456:def789",
			wantBase: "abc123",
			wantPR:   "def789",
			wantErr:  false,
		},
		{
			name:     "valid: multiple PRs (use first)",
			pullRefs: "main:abc123,456:def789,789:ghi012",
			wantBase: "abc123",
			wantPR:   "def789",
			wantErr:  false,
		},
		{
			name:     "valid: different base branch",
			pullRefs: "release-1.0:abc123,456:def789",
			wantBase: "abc123",
			wantPR:   "def789",
			wantErr:  false,
		},
		{
			name:     "invalid: empty string",
			pullRefs: "",
			wantErr:  true,
		},
		{
			name:     "invalid: missing PR ref",
			pullRefs: "main:abc123",
			wantErr:  true,
		},
		{
			name:     "invalid: malformed base ref",
			pullRefs: "main-abc123,456:def789",
			wantErr:  true,
		},
		{
			name:     "invalid: malformed PR ref",
			pullRefs: "main:abc123,456-def789",
			wantErr:  true,
		},
		{
			name:     "invalid: missing colon in base",
			pullRefs: "mainabc123,456:def789",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBase, gotPR, err := parsePullRefs(tt.pullRefs)

			if (err != nil) != tt.wantErr {
				t.Errorf("parsePullRefs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if gotBase != tt.wantBase {
					t.Errorf("parsePullRefs() gotBase = %v, want %v", gotBase, tt.wantBase)
				}
				if gotPR != tt.wantPR {
					t.Errorf("parsePullRefs() gotPR = %v, want %v", gotPR, tt.wantPR)
				}
			}
		})
	}
}

func TestGetCommitRange_EnvVariables(t *testing.T) {
	// Create a temporary git repository for testing
	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("Failed to create temp repo: %v", err)
	}

	// Create initial commit
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)
	_, err = worktree.Add("test.txt")
	require.NoError(t, err)
	initialCommit, err := worktree.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@example.com"},
	})
	require.NoError(t, err)

	// Change to temp repo directory (auto-restores on cleanup)
	t.Chdir(tempDir)

	tests := []struct {
		env     map[string]string
		name    string
		wantErr bool
	}{
		{
			name: "Priority 1: PULL_REFS",
			env: map[string]string{
				"PULL_REFS": "main:" + initialCommit.String() + ",123:" + initialCommit.String(),
			},
			wantErr: false,
		},
		{
			name: "Priority 2: PULL_BASE_SHA + PULL_PULL_SHA",
			env: map[string]string{
				"PULL_BASE_SHA": initialCommit.String(),
				"PULL_PULL_SHA": initialCommit.String(),
			},
			wantErr: false,
		},
		{
			name: "Priority 3: PULL_BASE_REF (should fail - no origin)",
			env: map[string]string{
				"PULL_BASE_REF": "main",
			},
			wantErr: true,
		},
		{
			name: "Invalid: Malformed PULL_REFS",
			env: map[string]string{
				"PULL_REFS": "invalid-format",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars using t.Setenv (auto-restores when subtest ends)
			for _, key := range []string{"PULL_REFS", "PULL_BASE_SHA", "PULL_PULL_SHA", "PULL_BASE_REF"} {
				t.Setenv(key, "")
			}

			// Set test env vars
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			baseSHA, headSHA, err := getCommitRange(repo)

			if (err != nil) != tt.wantErr {
				t.Errorf("getCommitRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if baseSHA == "" || headSHA == "" {
					t.Errorf("getCommitRange() returned empty SHAs: base=%s, head=%s", baseSHA, headSHA)
				}
			}
		})
	}
}

func TestGetCommitsInRange_Integration(t *testing.T) {
	// Create a temporary git repository
	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("Failed to create temp repo: %v", err)
	}

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Helper to create a commit
	createCommit := func(filename, content, message string) string {
		t.Helper()
		testFile := filepath.Join(tempDir, filename)
		if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}
		if _, err := worktree.Add(filename); err != nil {
			t.Fatalf("Failed to add test file: %v", err)
		}
		hash, err := worktree.Commit(message, &git.CommitOptions{
			Author: &object.Signature{Name: "Test", Email: "test@example.com"},
		})
		require.NoError(t, err)
		return hash.String()
	}

	// Create commit history: base -> commit1 -> commit2 -> head
	base := createCommit("file1.txt", "base", "feat: base commit")
	commit1 := createCommit("file2.txt", "content1", "feat: commit 1")
	commit2 := createCommit("file3.txt", "content2", "fix: commit 2")
	head := createCommit("file4.txt", "content3", "docs: commit 3")

	tests := []struct {
		name        string
		baseSHA     string
		headSHA     string
		wantCommits []string
		wantCount   int
		wantErr     bool
	}{
		{
			name:        "valid: 3 commits in range",
			baseSHA:     base,
			headSHA:     head,
			wantCount:   3,
			wantCommits: []string{head, commit2, commit1}, // Reverse chronological order
			wantErr:     false,
		},
		{
			name:        "valid: 1 commit in range",
			baseSHA:     commit2,
			headSHA:     head,
			wantCount:   1,
			wantCommits: []string{head},
			wantErr:     false,
		},
		{
			name:        "valid: head == base (no commits)",
			baseSHA:     head,
			headSHA:     head,
			wantCount:   0,
			wantCommits: []string{},
			wantErr:     false,
		},
		// Note: go-git's repo.Log() doesn't immediately error on non-existent SHA
		// The error occurs during iteration, but if base == head it returns empty
		// SHA validation is done at the validatePR level, not here
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commits, err := getCommitsInRange(repo, tt.baseSHA, tt.headSHA)

			if (err != nil) != tt.wantErr {
				t.Errorf("getCommitsInRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(commits) != tt.wantCount {
					t.Errorf("getCommitsInRange() got %d commits, want %d", len(commits), tt.wantCount)
				}

				for i, want := range tt.wantCommits {
					if i >= len(commits) {
						t.Errorf("getCommitsInRange() missing commit at index %d", i)
						continue
					}
					if commits[i] != want {
						t.Errorf("getCommitsInRange() commit[%d] = %s, want %s", i, commits[i][:8], want[:8])
					}
				}
			}
		})
	}
}

func TestGetCommitsInRange_DivergedBranches(t *testing.T) {
	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	createCommit := func(filename, content, message string) string {
		t.Helper()
		testFile := filepath.Join(tempDir, filename)
		require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))
		_, err = worktree.Add(filename)
		require.NoError(t, err)
		hash, commitErr := worktree.Commit(message, &git.CommitOptions{
			Author: &object.Signature{Name: "Test", Email: "test@example.com"},
		})
		require.NoError(t, commitErr)
		return hash.String()
	}

	// Create common ancestor on main
	createCommit("init.txt", "init", "feat: initial commit")

	// Create feature branch from this point
	err = worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	})
	require.NoError(t, err)

	// Add commits on feature branch (these are the PR commits)
	prCommit1 := createCommit("feature1.txt", "f1", "feat: pr commit 1")
	prCommit2 := createCommit("feature2.txt", "f2", "fix: pr commit 2")

	// Go back to main and add more commits (simulates main moving forward)
	err = worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.ReferenceName("refs/heads/master"),
	})
	require.NoError(t, err)

	createCommit("main1.txt", "m1", "feat: main commit 1")
	mainHead := createCommit("main2.txt", "m2", "feat: main commit 2")

	// baseSHA = main HEAD, headSHA = feature HEAD (diverged)
	// Should return only the 2 PR commits, not the main commits
	commits, err := getCommitsInRange(repo, mainHead, prCommit2)
	require.NoError(t, err)
	require.Len(t, commits, 2, "should only return PR commits, not main branch commits")
	require.Equal(t, prCommit2, commits[0])
	require.Equal(t, prCommit1, commits[1])
}

func TestValidateCommits_MultiLineMessage(t *testing.T) {
	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)
	_, err = worktree.Add("test.txt")
	require.NoError(t, err)

	commitMsg := "feat: add new feature\n\nDetailed description\nspanning multiple lines."
	multilineHash, err := worktree.Commit(commitMsg, &git.CommitOptions{
		Author: &object.Signature{Name: "developer", Email: "developer@redhat.com"},
	})
	require.NoError(t, err)

	validator := pkgcommitlint.NewValidator()

	failedCommits, passedCount := validateCommits(
		validator, repo, []string{multilineHash.String()},
	)

	require.Empty(t, failedCommits, "multi-line commit with valid header should pass")
	require.Equal(t, 1, passedCount)
}

func TestValidateCommits_WhitelistedAuthor(t *testing.T) {
	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Create a commit from a whitelisted bot with a non-conforming message
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)
	_, err = worktree.Add("test.txt")
	require.NoError(t, err)

	botHash, err := worktree.Commit("Red Hat Konflux kflux-prd-rh02 update hyperfleet-api", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "red-hat-konflux-kflux-prd-rh02",
			Email: "konflux@no-reply.konflux-ci.dev",
		},
	})
	require.NoError(t, err)

	// Create a commit from a regular user with a valid message
	testFile2 := filepath.Join(tempDir, "test2.txt")
	err = os.WriteFile(testFile2, []byte("test2"), 0644)
	require.NoError(t, err)
	_, err = worktree.Add("test2.txt")
	require.NoError(t, err)

	userHash, err := worktree.Commit("feat: add new feature", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "developer",
			Email: "developer@redhat.com",
		},
	})
	require.NoError(t, err)

	validator := pkgcommitlint.NewValidator()

	// Bot commit should be skipped, user commit should pass
	failedCommits, passedCount := validateCommits(
		validator, repo, []string{botHash.String(), userHash.String()},
	)

	require.Empty(t, failedCommits, "no commits should fail")
	require.Equal(t, 2, passedCount, "both commits should count as passed (1 skipped + 1 validated)")
}

func TestValidateCommits_WhitelistedAuthorInvalidMessage(t *testing.T) {
	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Non-whitelisted author with a non-conforming message should fail
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)
	_, err = worktree.Add("test.txt")
	require.NoError(t, err)

	badHash, err := worktree.Commit("this does not conform to the standard", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "developer",
			Email: "developer@redhat.com",
		},
	})
	require.NoError(t, err)

	validator := pkgcommitlint.NewValidator()

	failedCommits, passedCount := validateCommits(
		validator, repo, []string{badHash.String()},
	)

	require.Len(t, failedCommits, 1, "non-whitelisted author with bad message should fail")
	require.Equal(t, 0, passedCount)
}

func TestShortSHA(t *testing.T) {
	tests := []struct {
		name string
		sha  string
		want string
	}{
		{name: "full SHA", sha: "a1b2c3d4e5f6a7b8c9d0", want: "a1b2c3d4"},
		{name: "exactly 8 chars", sha: "a1b2c3d4", want: "a1b2c3d4"},
		{name: "short SHA (7 chars)", sha: "a1b2c3d", want: "a1b2c3d"},
		{name: "empty string", sha: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortSHA(tt.sha); got != tt.want {
				t.Errorf("shortSHA(%q) = %q, want %q", tt.sha, got, tt.want)
			}
		})
	}
}

// prEnvVars lists every environment variable the PR mode reads, so tests can start clean
var prEnvVars = []string{
	"JOB_SPEC", "REPO_OWNER", "REPO_NAME", "PULL_NUMBER", "GITHUB_TOKEN",
	"PULL_REFS", "PULL_BASE_SHA", "PULL_PULL_SHA", "PULL_BASE_REF",
}

func clearPREnv(t *testing.T) {
	t.Helper()
	for _, key := range prEnvVars {
		t.Setenv(key, "")
	}
}

func jobSpecJSON(title, author string) string {
	return `{"type":"presubmit","job":"pull-ci-validate-commits","refs":{` +
		`"org":"openshift-hyperfleet","repo":"hyperfleet-api","base_ref":"main",` +
		`"pulls":[{"number":42,"author":"` + author + `","sha":"abc1234","title":"` + title + `"}]}}`
}

// newRepoWithCommit creates a temp repo, chdirs into it, commits one file with the given
// message and author, and returns the repo and the commit hash
func newRepoWithCommit(t *testing.T, message, authorName, authorEmail string) (*git.Repository, string) {
	t.Helper()
	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("test"), 0644))
	_, err = worktree.Add("test.txt")
	require.NoError(t, err)

	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{Name: authorName, Email: authorEmail},
	})
	require.NoError(t, err)

	t.Chdir(tempDir)
	return repo, hash.String()
}

// addCommit commits one more file to repo with the given message and returns the commit hash
func addCommit(t *testing.T, repo *git.Repository, message, filename string) string {
	t.Helper()
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filename, []byte(filename), 0644))
	_, err = worktree.Add(filename)
	require.NoError(t, err)

	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{Name: "developer", Email: "developer@redhat.com"},
	})
	require.NoError(t, err)
	return hash.String()
}

// newRepoWithBadCommit creates a repo whose head is a commit that fails the commit rules,
// on top of a valid base commit, and returns the base and head hashes so a PULL_REFS
// range of base..head contains exactly the bad commit
func newRepoWithBadCommit(t *testing.T) (baseHash, badHash string) {
	t.Helper()
	repo, baseHash := newRepoWithCommit(t, "feat: base commit", "developer", "developer@redhat.com")
	badHash = addCommit(t, repo, "this does not conform to the standard", "bad.txt")
	return baseHash, badHash
}

func TestGetPRTitle(t *testing.T) {
	tests := []struct {
		env        map[string]string
		name       string
		wantTitle  string
		wantAuthor string
		wantNumber string
		wantSource string
		wantErr    bool
	}{
		{
			name: "JOB_SPEC: valid spec yields title without touching the API",
			env: map[string]string{
				"JOB_SPEC": jobSpecJSON("HYPERFLEET-42 - feat: add thing", "developer"),
			},
			wantTitle:  "HYPERFLEET-42 - feat: add thing",
			wantAuthor: "developer",
			wantNumber: "42",
			wantSource: "JOB_SPEC",
			wantErr:    false,
		},
		{
			name: "JOB_SPEC: wins over GitHub API env",
			env: map[string]string{
				"JOB_SPEC":    jobSpecJSON("HYPERFLEET-42 - feat: add thing", "developer"),
				"REPO_OWNER":  "openshift-hyperfleet",
				"REPO_NAME":   "hyperfleet-api",
				"PULL_NUMBER": "99",
			},
			wantTitle:  "HYPERFLEET-42 - feat: add thing",
			wantAuthor: "developer",
			wantNumber: "42",
			wantSource: "JOB_SPEC",
			wantErr:    false,
		},
		{
			name:    "no source: JOB_SPEC unset and no API env",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name: "no source: JOB_SPEC has no pulls and no API env",
			env: map[string]string{
				"JOB_SPEC": `{"type":"periodic","job":"nightly"}`,
			},
			wantErr: true,
		},
		{
			name: "no source: JOB_SPEC malformed and no API env",
			env: map[string]string{
				"JOB_SPEC": `{"refs":{"pulls":[`,
			},
			wantErr: true,
		},
		{
			name: "no source: JOB_SPEC pull has empty title and no API env",
			env: map[string]string{
				"JOB_SPEC": jobSpecJSON("", "developer"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearPREnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := getPRTitle(context.Background())

			if (err != nil) != tt.wantErr {
				t.Errorf("getPRTitle() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			require.Equal(t, tt.wantTitle, got.title)
			require.Equal(t, tt.wantAuthor, got.author)
			require.Equal(t, tt.wantNumber, got.number)
			require.Equal(t, tt.wantSource, got.source)
		})
	}
}

func TestValidatePR_TitleOnlyIgnoresCommitMessages(t *testing.T) {
	clearPREnv(t)

	// The range contains exactly one commit, and it fails the commit rules outright
	baseHash, badHash := newRepoWithBadCommit(t)
	t.Setenv("PULL_REFS", "main:"+baseHash+",42:"+badHash)
	t.Setenv("JOB_SPEC", jobSpecJSON("HYPERFLEET-42 - feat: add thing", "developer"))

	validator := pkgcommitlint.NewValidator()

	err := validatePR(context.Background(), validator, false)
	require.NoError(t, err, "--pr alone validates the title only, so a bad commit must not fail it")
}

func TestValidatePR_WithCommitsValidatesCommitMessages(t *testing.T) {
	clearPREnv(t)

	// Same range as the title-only test, so the two differ only in the flag
	baseHash, badHash := newRepoWithBadCommit(t)
	t.Setenv("PULL_REFS", "main:"+baseHash+",42:"+badHash)
	t.Setenv("JOB_SPEC", jobSpecJSON("HYPERFLEET-42 - feat: add thing", "developer"))

	validator := pkgcommitlint.NewValidator()

	err := validatePR(context.Background(), validator, true)
	require.ErrorIs(t, err, errValidationFailed, "--commits must still fail on a bad commit message")
}

func TestValidatePR_FailsWhenNoTitleAvailable(t *testing.T) {
	clearPREnv(t)

	// Commits are fine; only the title source is missing
	_, goodHash := newRepoWithCommit(t, "feat: add thing", "developer", "developer@redhat.com")
	t.Setenv("PULL_REFS", "main:"+goodHash+",42:"+goodHash)

	validator := pkgcommitlint.NewValidator()

	err := validatePR(context.Background(), validator, false)
	require.Error(t, err, "missing title must fail instead of passing silently")
	require.NotErrorIs(t, err, errValidationFailed, "missing title is a lookup error, not a rule failure")
	require.Contains(t, err.Error(), "could not get PR title")
}

func TestValidatePR_TitleRules(t *testing.T) {
	tests := []struct {
		wantErr error
		name    string
		title   string
		author  string
	}{
		{
			name:    "valid: JIRA prefix and type",
			title:   "HYPERFLEET-42 - feat: add thing",
			author:  "developer",
			wantErr: nil,
		},
		{
			name:    "invalid: missing JIRA prefix",
			title:   "feat: add thing",
			author:  "developer",
			wantErr: errValidationFailed,
		},
		{
			name:    "invalid: bad type",
			title:   "HYPERFLEET-42 - feature: add thing",
			author:  "developer",
			wantErr: errValidationFailed,
		},
		{
			name:    "skipped: whitelisted bot author with non-conforming title",
			title:   "Red Hat Konflux update hyperfleet-api",
			author:  "red-hat-konflux-kflux-prd-rh02[bot]",
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearPREnv(t)
			t.Setenv("JOB_SPEC", jobSpecJSON(tt.title, tt.author))

			validator := pkgcommitlint.NewValidator()

			err := validatePR(context.Background(), validator, false)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestRun_CommitsRequiresPR(t *testing.T) {
	cmd := NewCommand()
	require.NoError(t, cmd.Flags().Set("commits", "true"))
	t.Cleanup(func() {
		flagPR = false
		flagCommits = false
	})

	err := run(cmd, nil)
	require.ErrorIs(t, err, errCommitsRequiresPR)
}
