package commitlint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/commitlint"
	ghclient "github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/github"
	"github.com/spf13/cobra"
)

const commitStandardURL = "https://github.com/openshift-hyperfleet/" +
	"architecture/blob/main/hyperfleet/standards/commit-standard.md"

var (
	// errValidationFailed is returned when commit message validation fails
	errValidationFailed = errors.New("validation failed")

	// shaPattern matches valid git SHA (7-40 hex characters)
	shaPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

var (
	pr bool
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

  # CI: Validate entire PR (all commits + PR title)
  hyperfleet-hooks commitlint --pr

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

	cmd.Flags().BoolVar(&pr, "pr", false, "Validate all commits and PR title (CI mode, auto-detect from environment)")

	return cmd
}

func run(cmd *cobra.Command, args []string) error {
	validator := commitlint.NewValidator()

	// If --pr is provided, validate all commits and PR title
	if pr {
		return validatePR(cmd.Context(), validator)
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

func validatePR(ctx context.Context, validator *commitlint.Validator) error {
	// Open git repository once and reuse
	repo, err := git.PlainOpen(".")
	if err != nil {
		return fmt.Errorf("failed to open git repository: %w", err)
	}

	// Get commit range from environment
	baseSHA, headSHA, err := getCommitRange(repo)
	if err != nil {
		return fmt.Errorf("failed to get commit range: %w", err)
	}

	// Validate SHA format
	if errBase := validateSHA(baseSHA); errBase != nil {
		return fmt.Errorf("invalid base SHA: %w", errBase)
	}
	if errHead := validateSHA(headSHA); errHead != nil {
		return fmt.Errorf("invalid head SHA: %w", errHead)
	}

	fmt.Fprintf(os.Stderr, "🔍 Validating commits in range: %s..%s\n", shortSHA(baseSHA), shortSHA(headSHA))

	// Get all commits in range using git log
	commits, err := getCommitsInRange(repo, baseSHA, headSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Local git history unavailable: %v\n", err)
		fmt.Fprintln(os.Stderr, "   Falling back to GitHub API...")
		return validatePRFromAPI(ctx, validator)
	}

	if len(commits) == 0 {
		fmt.Fprintln(os.Stderr, "⚠️  No commits found in range")
		return nil
	}

	fmt.Fprintf(os.Stderr, "📝 Found %d commit(s) to validate\n\n", len(commits))

	// Validate each commit
	failedCommits, passedCount := validateCommits(validator, repo, commits)

	// Validate PR title
	title, prTitleFailed := validatePRTitle(ctx, validator)

	// Print summary and return result
	return printSummary(failedCommits, prTitleFailed, commits, passedCount, title)
}

func validatePRFromAPI(ctx context.Context, validator *commitlint.Validator) error {
	client := ghclient.NewClient()

	apiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prCommits, err := client.GetPRCommitsFromEnv(apiCtx)
	if err != nil {
		return fmt.Errorf("failed to get commits from GitHub API: %w", err)
	}

	if len(prCommits) == 0 {
		fmt.Fprintln(os.Stderr, "⚠️  No commits found in PR")
		return nil
	}

	fmt.Fprintf(os.Stderr, "📝 Found %d commit(s) to validate (via GitHub API)\n\n", len(prCommits))

	var failedCommits []string
	var passedCount int
	commitSHAs := make([]string, 0, len(prCommits))

	for _, pc := range prCommits {
		commitSHAs = append(commitSHAs, pc.SHA)
		subject := strings.SplitN(strings.TrimSpace(pc.Message), "\n", 2)[0]
		fmt.Fprintf(os.Stderr, "Checking: %s - %s\n", shortSHA(pc.SHA), subject)

		if validator.IsWhitelistedAuthor(pc.AuthorEmail, pc.AuthorName) {
			fmt.Fprintf(os.Stderr, "  ⏭️  SKIP (whitelisted author: %s)\n", pc.AuthorEmail)
			passedCount++
			continue
		}

		result := validator.Validate(strings.TrimSpace(pc.Message))
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
			failedCommits = append(failedCommits, shortSHA(pc.SHA))
		}
	}

	title, prTitleFailed := validatePRTitle(ctx, validator)

	return printSummary(failedCommits, prTitleFailed, commitSHAs, passedCount, title)
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

// validatePRTitle fetches and validates the PR title via GitHub API
func validatePRTitle(ctx context.Context, validator *commitlint.Validator) (title string, failed bool) {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(os.Stderr, "📋 Validating PR title...")

	client := ghclient.NewClient()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prInfo, prNumberStr, err := client.GetPRInfoFromEnv(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Could not fetch PR title: %v\n", err)
		fmt.Fprintln(os.Stderr, "   Skipping PR title validation")
		return "", false
	}

	title = prInfo.Title
	fmt.Fprintf(os.Stderr, "PR #%s: %s\n", prNumberStr, title)

	if validator.IsWhitelistedAuthor(prInfo.AuthorLogin) {
		fmt.Fprintf(os.Stderr, "  ⏭️  SKIP (whitelisted author: %s)\n", prInfo.AuthorLogin)
		return title, false
	}

	result := validator.ValidatePRTitle(title)
	if result.Valid {
		fmt.Fprintln(os.Stderr, "  ✅ PASS")
		return title, false
	}

	fmt.Fprintln(os.Stderr, "  ❌ FAIL")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Error details:")
	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "    ✖ %s\n", e.Error())
	}
	fmt.Fprintln(os.Stderr, "")
	return title, true
}

// printSummary prints the validation summary and returns an error if any validation failed
func printSummary(failedCommits []string, prTitleFailed bool, commits []string, passedCount int, title string) error {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	if len(failedCommits) == 0 && !prTitleFailed {
		fmt.Fprintf(os.Stderr, "✅ All %d commit(s) passed validation!\n", passedCount)
		if title != "" {
			fmt.Fprintln(os.Stderr, "✅ PR title passed validation!")
		}
		fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return nil
	}

	if len(failedCommits) > 0 {
		fmt.Fprintf(os.Stderr, "❌ %d of %d commit(s) failed validation\n", len(failedCommits), len(commits))
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
