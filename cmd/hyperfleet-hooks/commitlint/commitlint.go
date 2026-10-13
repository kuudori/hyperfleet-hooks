package commitlint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/commitlint"
	ghclient "github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/github"
	"github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/prow"
	"github.com/spf13/cobra"
)

const commitStandardURL = "https://github.com/openshift-hyperfleet/" +
	"architecture/blob/main/hyperfleet/standards/commit-standard.md"

var (
	// errValidationFailed is returned when commit message validation fails
	errValidationFailed = errors.New("validation failed")

	// errCommitsRequiresPR is returned when --commits is used without --pr
	errCommitsRequiresPR = errors.New("--commits can only be used together with --pr")

	// shaPattern matches valid git SHA (7-40 hex characters)
	shaPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

var (
	flagPR      bool
	flagCommits bool
)

// NewCommand creates the commitlint command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commitlint [commit-msg-file]",
		Short: "Validate commit message",
		Long: `Validate commit messages against HyperFleet Commit Standard.

Usage:
  # Local: Validate single commit message
  hyperfleet-hooks commitlint <file>           # from file (e.g., .git/COMMIT_EDITMSG)
  echo "feat: ..." | hyperfleet-hooks commitlint  # from stdin

  # CI: Validate the PR title (the title becomes the squashed commit on merge)
  hyperfleet-hooks commitlint --pr

  # CI: Also validate every commit in the PR range
  hyperfleet-hooks commitlint --pr --commits

In --pr mode the title is read from the JOB_SPEC environment variable that Prow
sets on every job, falling back to the GitHub API (REPO_OWNER, REPO_NAME,
PULL_NUMBER). Validation fails when no title can be obtained.

Commits inside a PR are not validated by CI unless --commits is given: they only
need the <type>: <subject> shape enforced locally by the commit-msg hook.

Examples:
  # Pre-commit hook (local)
  hyperfleet-hooks commitlint .git/COMMIT_EDITMSG

  # Manual validation (local)
  echo "feat: add new feature" | hyperfleet-hooks commitlint

  # Prow CI (auto-detects PR from environment)
  hyperfleet-hooks commitlint --pr`,
		Args: cobra.MaximumNArgs(1),
		RunE: run,
	}

	cmd.Flags().BoolVar(&flagPR, "pr", false,
		"Validate the PR title (CI mode, auto-detect from environment)")
	cmd.Flags().BoolVar(&flagCommits, "commits", false,
		"With --pr, also validate every commit in the PR range")

	return cmd
}

func run(cmd *cobra.Command, args []string) error {
	validator := commitlint.NewValidator()

	if flagCommits && !flagPR {
		return errCommitsRequiresPR
	}

	// If --pr is provided, validate the PR title (and the commits with --commits)
	if flagPR {
		return validatePR(cmd.Context(), validator, flagCommits)
	}

	// Single commit validation (from file or stdin)
	if len(args) > 0 {
		// Read from file
		filePath := args[0]
		result, err := validator.ValidateFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read commit message: %w", err)
		}
		return handleValidationResult(result, filePath)
	}

	// Read from stdin
	content, err := readStdin()
	if err != nil {
		return fmt.Errorf("failed to read from stdin: %w", err)
	}

	result := validator.Validate(content)
	return handleValidationResult(result, "stdin")
}

func handleValidationResult(result *commitlint.ValidationResult, source string) error {
	if result.Valid {
		return nil
	}

	// Print errors in commitlint format
	fmt.Fprintf(os.Stderr, "⧗   input: %s\n", source)
	for _, err := range result.Errors {
		fmt.Fprintf(os.Stderr, "✖   %s\n", err.Error())
	}
	fmt.Fprintf(os.Stderr, "\n✖   found %d problems, 0 warnings\n", len(result.Errors))
	fmt.Fprintf(os.Stderr, "ⓘ   Get help: %s\n", commitStandardURL)

	return errValidationFailed
}

func readStdin() (string, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat stdin: %w", err)
	}

	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "", fmt.Errorf("no input provided (stdin is empty)")
	}

	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read from stdin: %w", err)
	}

	return string(content), nil
}

// commitsResult holds the outcome of per-commit validation for the summary
type commitsResult struct {
	commits     []string
	failed      []string
	passedCount int
	checked     bool
}

// validatePR validates the PR title and, when withCommits is set, every commit in the PR range.
// The title is required: validation fails when it cannot be obtained.
func validatePR(ctx context.Context, validator *commitlint.Validator, withCommits bool) error {
	var commits commitsResult
	if withCommits {
		result, err := validatePRCommits(ctx, validator)
		if err != nil {
			return err
		}
		commits = *result
	}

	title, prTitleFailed, err := validatePRTitle(ctx, validator)
	if err != nil {
		return err
	}

	return printSummary(commits, prTitleFailed, title)
}

// validatePRCommits validates each commit in the PR range, preferring local git history
// and falling back to the GitHub API when the history is unavailable.
func validatePRCommits(ctx context.Context, validator *commitlint.Validator) (*commitsResult, error) {
	// Open git repository once and reuse
	repo, err := git.PlainOpen(".")
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}

	// Get commit range from environment
	baseSHA, headSHA, err := getCommitRange(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit range: %w", err)
	}

	// Validate SHA format
	if errBase := validateSHA(baseSHA); errBase != nil {
		return nil, fmt.Errorf("invalid base SHA: %w", errBase)
	}
	if errHead := validateSHA(headSHA); errHead != nil {
		return nil, fmt.Errorf("invalid head SHA: %w", errHead)
	}

	fmt.Fprintf(os.Stderr, "🔍 Validating commits in range: %s..%s\n", shortSHA(baseSHA), shortSHA(headSHA))

	// Get all commits in range using git log
	commits, err := getCommitsInRange(repo, baseSHA, headSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Local git history unavailable: %v\n", err)
		fmt.Fprintln(os.Stderr, "   Falling back to GitHub API...")
		return validatePRCommitsFromAPI(ctx, validator)
	}

	result := &commitsResult{checked: true, commits: commits}
	if len(commits) == 0 {
		fmt.Fprintln(os.Stderr, "⚠️  No commits found in range")
		return result, nil
	}

	fmt.Fprintf(os.Stderr, "📝 Found %d commit(s) to validate\n\n", len(commits))

	// Validate each commit
	result.failed, result.passedCount = validateCommits(validator, repo, commits)

	return result, nil
}

func validatePRCommitsFromAPI(ctx context.Context, validator *commitlint.Validator) (*commitsResult, error) {
	client := ghclient.NewClient()

	apiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prCommits, err := client.GetPRCommitsFromEnv(apiCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get commits from GitHub API: %w", err)
	}

	result := &commitsResult{checked: true}
	if len(prCommits) == 0 {
		fmt.Fprintln(os.Stderr, "⚠️  No commits found in PR")
		return result, nil
	}

	fmt.Fprintf(os.Stderr, "📝 Found %d commit(s) to validate (via GitHub API)\n\n", len(prCommits))

	result.commits = make([]string, 0, len(prCommits))

	for _, pc := range prCommits {
		result.commits = append(result.commits, pc.SHA)
		subject := strings.SplitN(strings.TrimSpace(pc.Message), "\n", 2)[0]
		fmt.Fprintf(os.Stderr, "Checking: %s - %s\n", shortSHA(pc.SHA), subject)

		if validator.IsWhitelistedAuthor(pc.AuthorEmail, pc.AuthorName) {
			fmt.Fprintf(os.Stderr, "  ⏭️  SKIP (whitelisted author: %s)\n", pc.AuthorEmail)
			result.passedCount++
			continue
		}

		res := validator.Validate(strings.TrimSpace(pc.Message))
		if res.Valid {
			fmt.Fprintln(os.Stderr, "  ✅ PASS")
			result.passedCount++
		} else {
			fmt.Fprintln(os.Stderr, "  ❌ FAIL")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  Error details:")
			for _, e := range res.Errors {
				fmt.Fprintf(os.Stderr, "    ✖ %s\n", e.Error())
			}
			fmt.Fprintln(os.Stderr, "")
			result.failed = append(result.failed, shortSHA(pc.SHA))
		}
	}

	return result, nil
}

// validateCommits validates each commit message and returns failed commit SHAs and passed count
func validateCommits(
	validator *commitlint.Validator, repo *git.Repository, commits []string,
) (failedCommits []string, passedCount int) {
	for _, sha := range commits {
		hash := plumbing.NewHash(sha)
		commit, err := repo.CommitObject(hash)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to get commit for %s: %v\n", shortSHA(sha), err)
			failedCommits = append(failedCommits, shortSHA(sha))
			continue
		}

		msg := strings.TrimSpace(commit.Message)
		lines := strings.SplitN(msg, "\n", 2)
		subject := strings.TrimSpace(lines[0])

		fmt.Fprintf(os.Stderr, "Checking: %s - %s\n", shortSHA(sha), subject)

		if validator.IsWhitelistedAuthor(commit.Author.Email, commit.Author.Name) {
			fmt.Fprintf(os.Stderr, "  ⏭️  SKIP (whitelisted author: %s)\n", commit.Author.Email)
			passedCount++
			continue
		}

		result := validator.Validate(msg)
		if result.Valid {
			fmt.Fprintln(os.Stderr, "  ✅ PASS")
			passedCount++
		} else {
			fmt.Fprintln(os.Stderr, "  ❌ FAIL")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  Error details:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "    ✖ %s\n", e.Error())
			}
			fmt.Fprintln(os.Stderr, "")
			failedCommits = append(failedCommits, shortSHA(sha))
		}
	}
	return failedCommits, passedCount
}

// prTitle is a PR title together with where it was read from
type prTitle struct {
	title  string
	author string
	number string
	source string
}

// getPRTitle resolves the PR title.
// Priority: JOB_SPEC (set by Prow on every job) > GitHub API (REPO_OWNER, REPO_NAME, PULL_NUMBER).
// It returns an error when neither source yields a title.
func getPRTitle(ctx context.Context) (*prTitle, error) {
	var jobSpecErr error
	if raw := os.Getenv(prow.JobSpecEnv); raw != "" {
		pull, err := prow.PullFromJobSpec(raw)
		if err == nil {
			return &prTitle{
				title:  pull.Title,
				author: pull.Author,
				number: strconv.Itoa(pull.Number),
				source: prow.JobSpecEnv,
			}, nil
		}
		jobSpecErr = err
		fmt.Fprintf(os.Stderr, "⚠️  Could not read PR title from %s: %v\n", prow.JobSpecEnv, err)
		fmt.Fprintln(os.Stderr, "   Falling back to GitHub API...")
	}

	client := ghclient.NewClient()

	apiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	info, number, err := client.GetPRInfoFromEnv(apiCtx)
	if err != nil {
		if jobSpecErr != nil {
			return nil, fmt.Errorf("could not get PR title: %s: %w; GitHub API: %w", prow.JobSpecEnv, jobSpecErr, err)
		}
		return nil, fmt.Errorf("could not get PR title: %s not set; GitHub API: %w", prow.JobSpecEnv, err)
	}

	return &prTitle{
		title:  info.Title,
		author: info.AuthorLogin,
		number: number,
		source: "GitHub API",
	}, nil
}

// validatePRTitle resolves and validates the PR title.
// It returns an error when the title cannot be obtained, and failed=true when it does not
// meet the standard.
func validatePRTitle(ctx context.Context, validator *commitlint.Validator) (title string, failed bool, err error) {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(os.Stderr, "📋 Validating PR title...")

	info, err := getPRTitle(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ❌ %v\n", err)
		return "", true, err
	}

	title = info.title
	fmt.Fprintf(os.Stderr, "PR #%s (via %s): %s\n", info.number, info.source, title)

	if validator.IsWhitelistedAuthor(info.author) {
		fmt.Fprintf(os.Stderr, "  ⏭️  SKIP (whitelisted author: %s)\n", info.author)
		return title, false, nil
	}

	result := validator.ValidatePRTitle(title)
	if result.Valid {
		fmt.Fprintln(os.Stderr, "  ✅ PASS")
		return title, false, nil
	}

	fmt.Fprintln(os.Stderr, "  ❌ FAIL")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Error details:")
	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "    ✖ %s\n", e.Error())
	}
	fmt.Fprintln(os.Stderr, "")
	return title, true, nil
}

// printSummary prints the validation summary and returns an error if any validation failed
func printSummary(commits commitsResult, prTitleFailed bool, title string) error {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	if len(commits.failed) == 0 && !prTitleFailed {
		if commits.checked {
			fmt.Fprintf(os.Stderr, "✅ All %d commit(s) passed validation!\n", commits.passedCount)
		}
		if title != "" {
			fmt.Fprintln(os.Stderr, "✅ PR title passed validation!")
		}
		fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return nil
	}

	if len(commits.failed) > 0 {
		fmt.Fprintf(os.Stderr, "❌ %d of %d commit(s) failed validation\n", len(commits.failed), len(commits.commits))
	}
	if prTitleFailed {
		fmt.Fprintln(os.Stderr, "❌ PR title failed validation")
	}
	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "📖 HyperFleet Commit Standard:")
	fmt.Fprintln(os.Stderr, "   Format: [<JIRA_PROJECT_ID>-<TICKET_NUM> - ]<type>: <subject>")
	fmt.Fprintln(os.Stderr, "   Types: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert")
	fmt.Fprintln(os.Stderr, "   Max length: 72 chars (excluding JIRA prefix)")
	fmt.Fprintln(os.Stderr, "   PR titles must include the JIRA ticket: the title becomes the squashed commit")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "   Full spec: %s\n", commitStandardURL)

	return errValidationFailed
}

// getCommitRange returns the base and head SHA for the PR commits
// Priority: PULL_REFS > PULL_BASE_SHA+PULL_PULL_SHA > PULL_BASE_REF+HEAD
func getCommitRange(repo *git.Repository) (baseSHA, headSHA string, err error) {
	// Priority 1: PULL_REFS (most accurate, Prow standard)
	if pullRefs := os.Getenv("PULL_REFS"); pullRefs != "" {
		baseSHA, headSHA, err = parsePullRefs(pullRefs)
		if err == nil {
			return baseSHA, headSHA, nil
		}
		fmt.Fprintf(os.Stderr, "⚠️  Failed to parse PULL_REFS: %v\n", err)
	}

	// Priority 2: PULL_BASE_SHA + PULL_PULL_SHA
	baseSHA = os.Getenv("PULL_BASE_SHA")
	headSHA = os.Getenv("PULL_PULL_SHA")
	if baseSHA != "" && headSHA != "" {
		return baseSHA, headSHA, nil
	}

	// Priority 3: PULL_BASE_REF + HEAD (fallback for local testing)
	baseBranch := os.Getenv("PULL_BASE_REF")
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Get SHA of origin/baseBranch
	baseRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", baseBranch), true)
	if err != nil {
		return "", "", fmt.Errorf("failed to get base SHA: %w", err)
	}
	baseSHA = baseRef.Hash().String()

	// Get SHA of HEAD
	head, err := repo.Head()
	if err != nil {
		return "", "", fmt.Errorf("failed to get HEAD SHA: %w", err)
	}
	headSHA = head.Hash().String()

	return baseSHA, headSHA, nil
}

// parsePullRefs parses PULL_REFS environment variable
// Format: "base:base_sha,pr_number:pr_sha[,pr_number:pr_sha...]"
// Example: "main:abc123,456:def789"
func parsePullRefs(pullRefs string) (baseSHA, prSHA string, err error) {
	if pullRefs == "" {
		return "", "", fmt.Errorf("PULL_REFS is empty")
	}

	// Split by comma: ["base:base_sha", "pr_number:pr_sha", ...]
	refs := strings.Split(pullRefs, ",")
	if len(refs) < 2 {
		return "", "", fmt.Errorf("invalid PULL_REFS format: %s", pullRefs)
	}

	// Parse base ref (first element)
	baseParts := strings.Split(refs[0], ":")
	if len(baseParts) != 2 {
		return "", "", fmt.Errorf("invalid base ref: %s", refs[0])
	}
	baseSHA = baseParts[1]

	// Parse PR ref (second element, first PR)
	prParts := strings.Split(refs[1], ":")
	if len(prParts) != 2 {
		return "", "", fmt.Errorf("invalid PR ref: %s", refs[1])
	}
	prSHA = prParts[1]

	return baseSHA, prSHA, nil
}

// shortSHA truncates a SHA to at most 8 characters for display
func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

// validateSHA checks if a string is a valid git SHA
func validateSHA(sha string) error {
	if sha == "" {
		return fmt.Errorf("SHA cannot be empty")
	}
	if !shaPattern.MatchString(sha) {
		return fmt.Errorf("invalid SHA format: %s (expected 7-40 hex characters)", sha)
	}
	return nil
}

// getCommitsInRange returns commit SHAs reachable from headSHA but not from baseSHA.
func getCommitsInRange(repo *git.Repository, baseSHA, headSHA string) ([]string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	//nolint:gosec // args are SHA hashes from git, not user input
	cmd := exec.Command("git", "rev-list", baseSHA+".."+headSHA)
	cmd.Dir = wt.Filesystem.Root()

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git rev-list failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("failed to run git rev-list: %w", err)
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return nil, nil
	}

	return strings.Split(trimmed, "\n"), nil
}
