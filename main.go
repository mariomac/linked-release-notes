package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v57/github"
	"golang.org/x/mod/semver"
	"golang.org/x/oauth2"
)

type Config struct {
	Token       string
	Repository  string
	Tag         string
	PreviousTag string
}

func main() {
	config := loadConfig()

	if err := run(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig() Config {
	return Config{
		Token:       getEnv("INPUT_GITHUB_TOKEN", ""),
		Repository:  getEnv("INPUT_REPOSITORY", ""),
		Tag:         getEnv("INPUT_TAG", ""),
		PreviousTag: getEnv("INPUT_PREVIOUS_TAG", ""),
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

func (w *ReleaseNotesWriter) fetchPreviousTag(ctx context.Context, owner, repo string) error {
	if w.config.PreviousTag != "" {
		w.previousTag = w.config.PreviousTag
		return nil
	}
	var tags []string
	for page := 1; ; page++ {
		releases, resp, err := w.client.Repositories.ListReleases(ctx, owner, repo, &github.ListOptions{Page: page, PerPage: 100})
		if err != nil {
			return err
		}
		for _, release := range releases {
			if release.TagName != nil && *release.TagName != "" {
				tn := *release.TagName // strings.TrimPrefix(*release.TagName, "v")
				// discard prereleases and other data
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
	fmt.Println("tags: ", tags)
	if len(tags) == 0 {
		return nil
	}
	if w.config.Tag == "" {
		w.previousTag = tags[len(tags)-1]
		return nil
	}
	i := len(tags) - 1
	for semver.Compare(w.config.Tag, tags[i]) <= 0 {
		i--
		if i < 0 {
			w.previousTag = tags[len(tags)-1]
			return nil
		}
	}
	w.previousTag = tags[i]
	return nil
}

func run(config Config) error {
	ctx := context.Background()

	// Setup GitHub client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: config.Token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Parse repository
	parts := strings.Split(config.Repository, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repository format: %s (expected owner/repo)", config.Repository)
	}
	owner, repo := parts[0], parts[1]
	rnw := ReleaseNotesWriter{config: config, client: client}
	if err := rnw.fetchPreviousTag(ctx, owner, repo); err != nil {
		return fmt.Errorf("fetching previous tag: %w", err)
	}
	fmt.Println("Previous tag:", rnw.previousTag)

	// Get release notes for main repository
	commit, err := rnw.commitForTag(ctx, owner, repo, rnw.config.Tag)
	if err != nil {
		// TODO: if tag does not exist, default to branch's latest commit
		return fmt.Errorf("failed to get commit for tag: %w", err)
	}
	fmt.Println("Commit:", commit)
	prevCommit, err := rnw.commitForTag(ctx, owner, repo, rnw.previousTag)
	if err != nil {
		return fmt.Errorf("failed to get commit for previous tag: %w", err)
	}
	fmt.Println("Previous commit:", prevCommit)
	changes, err := rnw.getChanges(ctx, owner, repo, commit, prevCommit)
	if err != nil {
		return fmt.Errorf("failed to get changes: %w", err)
	}
	submodulePath, submoduleRepository, err := rnw.getSubmodulePathRepo(ctx, owner, repo, commit)
	if err != nil {
		return fmt.Errorf("failed to get submodule path and repository: %w", err)
	}
	fmt.Printf("Submodule path: %s\n", submodulePath)
	fmt.Printf("Submodule repository: %s\n", submoduleRepository)

	var smChanges []string
	if submodulePath == "" || submoduleRepository == "" {
		fmt.Printf("No submodule repository specified: %q %q\n", submodulePath, submoduleRepository)
	} else {
		oldSMCommit, newSMCommit, err := rnw.getSubmoduleCommits(ctx, owner, repo, prevCommit, commit, submodulePath)
		if err != nil {
			return fmt.Errorf("failed to get submodule commits: %w", err)
		}
		fmt.Printf("Old submodule commit: %s\n", oldSMCommit[:8])
		fmt.Printf("New submodule commit: %s\n", newSMCommit[:8])
		parts := strings.Split(submoduleRepository, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid submodule repository format: %s (expected owner/repo)", submoduleRepository)
		}
		owner, repo := parts[0], parts[1]
		smChanges, err = rnw.getChanges(ctx, owner, repo, newSMCommit, oldSMCommit)
		if err != nil {
			return fmt.Errorf("failed to get submodule changes: %w", err)
		}
	}

	// Combine release notes
	finalNotes := fmt.Sprintf("## Changes from %s/%s:\n%s\n", owner, repo, strings.Join(changes, "\n"))
	finalNotes += fmt.Sprintf("\n## Changes from %s:\n%s\n", submoduleRepository, strings.Join(smChanges, "\n"))

	// Set outputs
	setOutput("release_notes", finalNotes)

	fmt.Println("\n\nRelease notes generated successfully:\n", finalNotes)
	return nil
}

func (w *ReleaseNotesWriter) commitForTag(ctx context.Context, owner, repo, tag string) (string, error) {
	ref, _, err := w.client.Git.GetRef(ctx, owner, repo, "tags/"+tag)
	if err != nil {
		return "", fmt.Errorf("failed to get tag reference: %w", err)
	}
	return ref.Object.GetSHA(), nil
}

func (w *ReleaseNotesWriter) getChanges(ctx context.Context, owner, repo, commit, prevCommit string) ([]string, error) {
	comparison, _, err := w.client.Repositories.CompareCommits(ctx, owner, repo, prevCommit, commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %w", err)
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

func (w *ReleaseNotesWriter) generateReleaseNotes(ctx context.Context, owner, repo string) (string, error) {
	// Generate release notes using GitHub API
	notes, _, err := w.client.Repositories.GenerateReleaseNotes(ctx, owner, repo, &github.GenerateNotesOptions{
		TagName:         w.config.Tag,
		PreviousTagName: &w.config.PreviousTag,
	})
	if err != nil {
		return "", err
	}

	return notes.Body, nil
}

func (w *ReleaseNotesWriter) getSubmoduleCommits(ctx context.Context, owner, repo, oldCommit, newCommit, submodulePath string) (old, new string, err error) {
	// Get submodule commit at old tag
	oldTree, _, err := w.client.Git.GetTree(ctx, owner, repo, oldCommit, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to get old tree: %w", err)
	}

	oldSubmoduleCommit := ""
	for _, entry := range oldTree.Entries {
		if entry.GetPath() == submodulePath && entry.GetType() == "commit" {
			oldSubmoduleCommit = entry.GetSHA()
			break
		}
	}

	// Get submodule commit at new tag
	newTree, _, err := w.client.Git.GetTree(ctx, owner, repo, newCommit, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to get new tree: %w", err)
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

func (w *ReleaseNotesWriter) getSubmodulePathRepo(ctx context.Context, owner, repo, commit string) (string, string, error) {
	// Get release notes for submodule repository
	// Read .gitmodules file
	// Get the .gitmodules file content from the repository at a specific commit
	gitmodulesContent, _, _, err := w.client.Repositories.GetContents(ctx, owner, repo, ".gitmodules", &github.RepositoryContentGetOptions{
		Ref: commit, // or tag, branch name
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to read .gitmodules from repository: %w", err)
	}

	// Decode the content (GitHub API returns base64-encoded content)
	content, err := gitmodulesContent.GetContent()
	if err != nil {
		return "", "", fmt.Errorf("failed to decode .gitmodules content: %w", err)
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
			url := strings.TrimPrefix(line, "url = ")
			// Remove .git suffix if present
			url = strings.TrimSuffix(url, ".git")
			// Extract owner/repo from URL (e.g., https://github.com/grafana/opentelemetry-ebpf-instrumentation.git)
			parts := strings.Split(url, "/")
			if len(parts) >= 2 {
				submoduleRepo = parts[len(parts)-2] + "/" + parts[len(parts)-1]
			}
		}
	}
	return submodulePath, submoduleRepo, nil
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
