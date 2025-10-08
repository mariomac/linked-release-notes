package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

type Config struct {
	Token               string
	Repository          string
	Tag                 string
	PreviousTag         string
	SubmodulePath       string
	SubmoduleRepository string
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
		Token:               getEnv("INPUT_GITHUB-TOKEN", ""),
		Repository:          getEnv("INPUT_REPOSITORY", ""),
		Tag:                 getEnv("INPUT_TAG", ""),
		PreviousTag:         getEnv("INPUT_PREVIOUS-TAG", ""),
		SubmodulePath:       getEnv("INPUT_SUBMODULE-PATH", ""),
		SubmoduleRepository: getEnv("INPUT_SUBMODULE-REPOSITORY", ""),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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

	// Get release notes for main repository
	releaseNotes, err := generateReleaseNotes(ctx, client, owner, repo, config.Tag, config.PreviousTag)
	if err != nil {
		return fmt.Errorf("failed to generate release notes: %w", err)
	}

	// Check for submodule changes
	hasSubmoduleChanges := false
	submoduleNotes := ""

	if config.SubmodulePath != "" && config.SubmoduleRepository != "" {
		subParts := strings.Split(config.SubmoduleRepository, "/")
		if len(subParts) != 2 {
			return fmt.Errorf("invalid submodule repository format: %s", config.SubmoduleRepository)
		}
		subOwner, subRepo := subParts[0], subParts[1]

		// Get submodule commit changes
		oldCommit, newCommit, err := getSubmoduleChanges(ctx, client, owner, repo, config.SubmodulePath, config.PreviousTag, config.Tag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get submodule changes: %v\n", err)
		} else if oldCommit != newCommit {
			hasSubmoduleChanges = true

			// Try to find tags for these commits
			oldTag, _ := findTagForCommit(ctx, client, subOwner, subRepo, oldCommit)
			newTag, _ := findTagForCommit(ctx, client, subOwner, subRepo, newCommit)

			if oldTag != "" && newTag != "" {
				notes, err := generateReleaseNotes(ctx, client, subOwner, subRepo, newTag, oldTag)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to generate submodule release notes: %v\n", err)
				} else {
					submoduleNotes = fmt.Sprintf("\n\n## Submodule Changes: %s\n\nUpdated from %s to %s\n\n%s",
						config.SubmoduleRepository, oldTag, newTag, notes)
				}
			} else {
				submoduleNotes = fmt.Sprintf("\n\n## Submodule Changes: %s\n\nUpdated from %s to %s",
					config.SubmoduleRepository, oldCommit[:8], newCommit[:8])
			}
		}
	}

	// Combine release notes
	finalNotes := releaseNotes + submoduleNotes

	// Set outputs
	setOutput("release-notes", finalNotes)
	setOutput("has-submodule-changes", fmt.Sprintf("%t", hasSubmoduleChanges))

	fmt.Println("Release notes generated successfully")
	return nil
}

func generateReleaseNotes(ctx context.Context, client *github.Client, owner, repo, tag, previousTag string) (string, error) {
	// If no previous tag is provided, try to find it
	if previousTag == "" {
		tags, _, err := client.Repositories.ListTags(ctx, owner, repo, &github.ListOptions{PerPage: 100})
		if err != nil {
			return "", err
		}

		// Find current tag index
		currentIndex := -1
		for i, t := range tags {
			if t.GetName() == tag {
				currentIndex = i
				break
			}
		}

		// Get previous tag
		if currentIndex > 0 && currentIndex < len(tags) {
			previousTag = tags[currentIndex+1].GetName()
		}
	}

	if previousTag == "" {
		// No previous tag, just get commits for this tag
		tagObj, _, err := client.Repositories.GetReleaseByTag(ctx, owner, repo, tag)
		if err == nil && tagObj.GetBody() != "" {
			return tagObj.GetBody(), nil
		}
		return fmt.Sprintf("Release %s", tag), nil
	}

	// Generate release notes using GitHub API
	notes, _, err := client.Repositories.GenerateReleaseNotes(ctx, owner, repo, &github.GenerateNotesOptions{
		TagName:         tag,
		PreviousTagName: &previousTag,
	})
	if err != nil {
		return "", err
	}

	return notes.Body, nil
}

func getSubmoduleChanges(ctx context.Context, client *github.Client, owner, repo, submodulePath, oldTag, newTag string) (string, string, error) {
	// Get the commit SHA for old tag
	oldRef, _, err := client.Git.GetRef(ctx, owner, repo, "tags/"+oldTag)
	if err != nil {
		return "", "", fmt.Errorf("failed to get old tag: %w", err)
	}
	oldCommit := oldRef.Object.GetSHA()

	// Get the commit SHA for new tag
	newRef, _, err := client.Git.GetRef(ctx, owner, repo, "tags/"+newTag)
	if err != nil {
		return "", "", fmt.Errorf("failed to get new tag: %w", err)
	}
	newCommit := newRef.Object.GetSHA()

	// Get submodule commit at old tag
	oldTree, _, err := client.Git.GetTree(ctx, owner, repo, oldCommit, true)
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
	newTree, _, err := client.Git.GetTree(ctx, owner, repo, newCommit, true)
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

func findTagForCommit(ctx context.Context, client *github.Client, owner, repo, commitSHA string) (string, error) {
	tags, _, err := client.Repositories.ListTags(ctx, owner, repo, &github.ListOptions{PerPage: 100})
	if err != nil {
		return "", err
	}

	for _, tag := range tags {
		if tag.GetCommit().GetSHA() == commitSHA {
			return tag.GetName(), nil
		}
	}

	return "", fmt.Errorf("tag not found for commit")
}

func setOutput(name, value string) {
	// GitHub Actions output format
	outputFile := os.Getenv("GITHUB_OUTPUT")
	if outputFile != "" {
		f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			defer f.Close()
			// Use multiline format for release notes
			if name == "release-notes" {
				fmt.Fprintf(f, "%s<<EOF\n%s\nEOF\n", name, value)
			} else {
				fmt.Fprintf(f, "%s=%s\n", name, value)
			}
		}
	}
}
