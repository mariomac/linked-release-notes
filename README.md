# linked-release-notes

A GitHub Action that generates standard release notes for a given project, and also adds the release notes from a submodule in the repository whose version might have changed since the last release.

## Features

- Generate release notes for your main repository
- Automatically detect submodule version changes between releases
- Include submodule release notes when the version has changed
- Fully customizable via action inputs

## Usage

### Basic Usage

```yaml
name: Release
on:
  release:
    types: [created]

jobs:
  release-notes:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
          submodules: recursive

      - name: Generate Release Notes
        id: release-notes
        uses: mariomac/linked-release-notes@v1
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}

      - name: Display Release Notes
        run: echo "${{ steps.release-notes.outputs.release-notes }}"
```

### With Submodule Tracking

```yaml
name: Release
on:
  release:
    types: [created]

jobs:
  release-notes:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
          submodules: recursive

      - name: Generate Release Notes
        id: release-notes
        uses: mariomac/linked-release-notes@v1
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
          submodule-path: 'path/to/submodule'
          submodule-repository: 'owner/submodule-repo'

      - name: Update Release with Notes
        if: steps.release-notes.outputs.has-submodule-changes == 'true'
        run: |
          echo "Submodule has changes!"
          echo "${{ steps.release-notes.outputs.release-notes }}"
```

## Inputs

| Input | Description | Required | Default |
|-------|-------------|----------|---------|
| `github-token` | GitHub token for API access | Yes | `${{ github.token }}` |
| `repository` | Repository in owner/repo format | No | `${{ github.repository }}` |
| `tag` | Tag to generate release notes for | No | `${{ github.ref_name }}` |
| `previous-tag` | Previous tag to compare against | No | Auto-detected |
| `submodule-path` | Path to submodule to check for updates | No | - |
| `submodule-repository` | Repository path for the submodule (owner/repo) | No | - |

## Outputs

| Output | Description |
|--------|-------------|
| `release-notes` | Generated release notes including submodule changes |
| `has-submodule-changes` | Whether the submodule version has changed (`true` or `false`) |

## How It Works

1. The action generates release notes for the main repository using GitHub's release notes API
2. If a submodule path is specified, it checks if the submodule commit has changed between tags
3. If the submodule has changed, it tries to find corresponding tags in the submodule repository
4. It generates release notes for the submodule and appends them to the main release notes
5. The combined release notes are provided as an output

## Example Output

```markdown
## What's Changed
* Feature: Add new authentication method by @user1 in #123
* Fix: Resolve memory leak in worker by @user2 in #124

**Full Changelog**: https://github.com/owner/repo/compare/v1.0.0...v1.1.0

## Submodule Changes: owner/submodule-repo

Updated from v2.0.0 to v2.1.0

## What's Changed
* Enhancement: Improve performance by @user3 in #45
* Fix: Handle edge case in parser by @user4 in #46

**Full Changelog**: https://github.com/owner/submodule-repo/compare/v2.0.0...v2.1.0
```

## License

Apache License 2.0 - see [LICENSE](LICENSE) file for details.