package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/google/go-github/v57/github"
	"golang.org/x/mod/semver"
	"golang.org/x/oauth2"
)

type Config struct {
	Token                  string
	Repository             string
	Tag                    string
	PreviousTag            string
	GeneratedSubmoduleLink string
}

func main() {
	config := loadConfig()

	if err := run(config); err != nil {
		log.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig() Config {
	return Config{
		Token:                  getEnv("INPUT_GITHUB_TOKEN", ""),
		Repository:             getEnv("INPUT_REPOSITORY", ""),
		Tag:                    getEnv("INPUT_TAG", ""),
		PreviousTag:            getEnv("INPUT_PREVIOUS_TAG", ""),
		GeneratedSubmoduleLink: getEnv("INPUT_GENERATED_SUBMODULE_LINK", ""),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

type ReleaseNotesWriter struct {
	config      Config
	client      *github.Client
	previousTag string
}

func run(config Config) error {
	ctx := context.Background()

	// Setup GitHub client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: config.Token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Parse repository name
	parts := strings.Split(config.Repository, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repository format: %s (expected owner/repo)", config.Repository)
	}

	owner, repo := parts[0], parts[1]
	rnw := ReleaseNotesWriter{config: config, client: client}
	if err := rnw.fetchPreviousTag(ctx, owner, repo); err != nil {
		return fmt.Errorf("fetching previous tag: %w", err)
	}
	log.Println("Previous tag:", rnw.previousTag)

	// Get release changes for main repository
	commit, prevCommit, changes, err := rnw.changesForMain(ctx, owner, repo)
	if err != nil {
		return err
	}
	log.Println("Commit:", commit)
	log.Println("Previous commit:", prevCommit)

	// get release changes for submodule repository
	submoduleRepository, smChanges, err := rnw.getChangesForSubmodule(ctx, owner, repo, commit, prevCommit)
	if err != nil {
		return err
	}

	// In submodule, replaces #PR_NUMBER by repo/name#PR_NUMBER for proper linking from GitHub
	rnw.replaceSubmoduleLinks(smChanges)

	// Combine release notes
	finalNotes := fmt.Sprintf("## Changes from %s/%s:\n%s\n", owner, repo, strings.Join(changes, "\n"))
	finalNotes += fmt.Sprintf("\n## Changes from %s:\n%s\n", submoduleRepository, strings.Join(smChanges, "\n"))

	// Set outputs
	setOutput("release_notes", finalNotes)

	fmt.Println("\n\nRelease notes generated successfully:")
	fmt.Println(finalNotes)
	return nil
}

func (rnw *ReleaseNotesWriter) getChangesForSubmodule(
	ctx context.Context, owner string, repo string, commit string, prevCommit string,
) (
	submoduleRepoName string, submoduleChanges []string, err error,
) {
	var submodulePath string
	submodulePath, submoduleRepoName, err = rnw.getSubmodulePathRepo(ctx, owner, repo, commit)
	if err != nil {
		err = fmt.Errorf("failed to get submodule path and repository: %w", err)
		return
	}
	log.Printf("Submodule path: %s\n", submodulePath)
	log.Printf("Submodule repository: %s\n", submoduleRepoName)

	var smChanges []string
	if submodulePath == "" || submoduleRepoName == "" {
		log.Printf("No submodule repository found")
		return
	}
	if rnw.config.GeneratedSubmoduleLink == "" {
		rnw.config.GeneratedSubmoduleLink = submoduleRepoName
	}

	// get the changes for the submodule commits
	oldSMCommit, newSMCommit, err := rnw.getSubmoduleCommits(ctx, owner, repo, prevCommit, commit, submodulePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get submodule commits: %w", err)
	}
	log.Printf("Old submodule commit: %s\n", oldSMCommit[:8])
	log.Printf("New submodule commit: %s\n", newSMCommit[:8])
	parts := strings.Split(submoduleRepoName, "/")
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("invalid submodule repository format: %s (expected owner/repo)", submoduleRepoName)
	}
	smOwner, smRepo := parts[0], parts[1]
	smChanges, err = rnw.getChanges(ctx, smOwner, smRepo, newSMCommit, oldSMCommit)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get submodule changes: %w", err)
	}

	return submoduleRepoName, smChanges, nil
}

// gets each release notes entry for the main branch
func (rnw *ReleaseNotesWriter) changesForMain(
	ctx context.Context, owner string, repo string,
) (
	commit, prevCommit string, changes []string, err error,
) {
	commit, err = rnw.commitForTag(ctx, owner, repo, rnw.config.Tag)
	if err != nil {
		// TODO: if tag does not exist, default to branch's latest commit
		err = fmt.Errorf("failed to get commit for tag: %w", err)
		return
	}
	prevCommit, err = rnw.commitForTag(ctx, owner, repo, rnw.previousTag)
	if err != nil {
		err = fmt.Errorf("failed to get commit for previous tag: %w", err)
		return
	}
	changes, err = rnw.getChanges(ctx, owner, repo, commit, prevCommit)
	if err != nil {
		fmt.Errorf("failed to get changes: %w", err)
		return
	}
	return
}

// If PreviousTag is not set, find the previous tag by iterating through all the releases and getting
// the semantically previous, non-prerelease tag
func (rnw *ReleaseNotesWriter) fetchPreviousTag(ctx context.Context, owner, repo string) error {
	if rnw.config.PreviousTag != "" {
		rnw.previousTag = rnw.config.PreviousTag
		return nil
	}
	var tags []string
	for page := 1; ; page++ {
		releases, resp, err := rnw.client.Repositories.ListReleases(ctx, owner, repo, &github.ListOptions{Page: page, PerPage: 100})
		if err != nil {
			return err
		}
		for _, release := range releases {
			if release.TagName != nil && *release.TagName != "" {
				tn := *release.TagName
				// discard prereleases
				fmt.Println(tn)
				if !strings.Contains(tn, "-") {
					tags = append(tags, tn)
				}
			}
		}
		if page >= resp.LastPage {
			break
		}
	}
	semver.Sort(tags)
	log.Println("tags: ", tags)
	if len(tags) == 0 {
		return nil
	}
	if rnw.config.Tag == "" {
		rnw.previousTag = tags[len(tags)-1]
		return nil
	}
	i := len(tags) - 1
	for semver.Compare(rnw.config.Tag, tags[i]) <= 0 {
		i--
		if i < 0 {
			rnw.previousTag = tags[len(tags)-1]
			return nil
		}
	}
	rnw.previousTag = tags[i]
	return nil
}

func (rnw *ReleaseNotesWriter) commitForTag(ctx context.Context, owner, repo, tag string) (string, error) {
	ref, _, err := rnw.client.Git.GetRef(ctx, owner, repo, "tags/"+tag)
	if err != nil {
		return "", fmt.Errorf("failed to get tag reference: %rnw", err)
	}
	return ref.Object.GetSHA(), nil
}

func (rnw *ReleaseNotesWriter) getChanges(ctx context.Context, owner, repo, commit, prevCommit string) ([]string, error) {
	comparison, _, err := rnw.client.Repositories.CompareCommits(ctx, owner, repo, prevCommit, commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %rnw", err)
	}

	var changes []string
	for _, commit := range comparison.Commits {
		if commit.Commit != nil && commit.Commit.Message != nil {
			message := strings.Split(*commit.Commit.Message, "\n")[0]
			changes = append(changes, "* "+message)
		}
	}
	return changes, nil
}

func (rnw *ReleaseNotesWriter) generateReleaseNotes(ctx context.Context, owner, repo string) (string, error) {
	// Generate release notes using GitHub API
	notes, _, err := rnw.client.Repositories.GenerateReleaseNotes(ctx, owner, repo, &github.GenerateNotesOptions{
		TagName:         rnw.config.Tag,
		PreviousTagName: &rnw.config.PreviousTag,
	})
	if err != nil {
		return "", err
	}

	return notes.Body, nil
}

func (rnw *ReleaseNotesWriter) getSubmoduleCommits(ctx context.Context, owner, repo, oldCommit, newCommit, submodulePath string) (old, new string, err error) {
	// Get submodule commit at old tag
	oldTree, _, err := rnw.client.Git.GetTree(ctx, owner, repo, oldCommit, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to get old tree: %rnw", err)
	}

	oldSubmoduleCommit := ""
	for _, entry := range oldTree.Entries {
		if entry.GetPath() == submodulePath && entry.GetType() == "commit" {
			oldSubmoduleCommit = entry.GetSHA()
			break
		}
	}

	// Get submodule commit at new tag
	newTree, _, err := rnw.client.Git.GetTree(ctx, owner, repo, newCommit, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to get new tree: %rnw", err)
	}

	newSubmoduleCommit := ""
	for _, entry := range newTree.Entries {
		if entry.GetPath() == submodulePath && entry.GetType() == "commit" {
			newSubmoduleCommit = entry.GetSHA()
			break
		}
	}

	if oldSubmoduleCommit == "" || newSubmoduleCommit == "" {
		return "", "", fmt.Errorf("submodule not found in one or both tags")
	}

	return oldSubmoduleCommit, newSubmoduleCommit, nil
}

func (rnw *ReleaseNotesWriter) getSubmodulePathRepo(ctx context.Context, owner, repo, commit string) (string, string, error) {
	// Get release notes for submodule repository
	// Read .gitmodules file
	// Get the .gitmodules file content from the repository at a specific commit
	gitmodulesContent, _, _, err := rnw.client.Repositories.GetContents(ctx, owner, repo, ".gitmodules", &github.RepositoryContentGetOptions{
		Ref: commit, // or tag, branch name
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to read .gitmodules from repository: %rnw", err)
	}

	// Decode the content (GitHub API returns base64-encoded content)
	content, err := gitmodulesContent.GetContent()
	if err != nil {
		return "", "", fmt.Errorf("failed to decode .gitmodules content: %rnw", err)
	}

	var submodulePath, submoduleRepo string

	// Parse the .gitmodules file
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Extract path
		if strings.HasPrefix(line, "path = ") {
			submodulePath = strings.TrimPrefix(line, "path = ")
		}

		// Extract URL and convert to owner/repo format
		if strings.HasPrefix(line, "url = ") {
			url := strings.TrimSpace(strings.TrimPrefix(line, "url = "))
			// Remove .git suffix if present
			url = strings.TrimSuffix(url, ".git")
			if strings.HasPrefix(url, "http") {
				// Extract owner/repo from URL (e.g., https://github.com/grafana/opentelemetry-ebpf-instrumentation.git)
				parts := strings.Split(url, "/")
				if len(parts) >= 2 {
					submoduleRepo = parts[len(parts)-2] + "/" + parts[len(parts)-1]
				}
			} else if strings.HasPrefix(url, "git@") {
				parts := strings.Split(url, ":")
				if len(parts) >= 2 {
					submoduleRepo = parts[1]
				}
			}
		}
	}
	return submodulePath, submoduleRepo, nil
}

func (rnw *ReleaseNotesWriter) replaceSubmoduleLinks(entries []string) {
	var linkNum = regexp.MustCompile(`#\d+($|\W)`)
	for i := range entries {
		entries[i] = linkNum.ReplaceAllStringFunc(entries[i], func(s string) string {
			return rnw.config.GeneratedSubmoduleLink + s
		})
	}
}

func setOutput(name, value string) {
	// GitHub Actions output format
	outputFile := os.Getenv("GITHUB_OUTPUT")
	if outputFile != "" {
		f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			defer f.Close()
			// Use multiline format for release notes
			if name == "release_notes" {
				fmt.Fprintf(f, "%s<<EOF\n%s\nEOF\n", name, value)
			} else {
				fmt.Fprintf(f, "%s=%s\n", name, value)
			}
		}
	}
}
