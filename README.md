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
name: Generate Release Notes
on:
  release:
    types:
      - published

permissions: write-all

jobs:
  release-notes:
    permissions: write-all
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
          submodules: recursive

      - name: Generate Release Notes
        id: release-notes
        uses: mariomac/linked-release-notes@v0.0.5
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          tag: ${{ github.event.release.tag_name }}

      - name: Update Release with Notes
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          RELEASE_ID: ${{ github.event.release.id }}
          RELEASE_NOTES: ${{ steps.release-notes.outputs.release_notes }}
        run: |
          curl -L \
            -X PATCH \
            -H "Accept: application/vnd.github+json" \
            -H "Authorization: Bearer $GITHUB_TOKEN" \
            -H "X-GitHub-Api-Version: 2022-11-28" \
            https://api.github.com/repos/${{ github.repository }}/releases/$RELEASE_ID \
            -d "{\"body\":$(jq -Rs . <<< "$RELEASE_NOTES")}"
```

## Inputs

| Input                  | Description | Required | Default |
|------------------------|-------------|----------|---------|
| `github_token`         | GitHub token for API access | Yes | `${{ github.token }}` |
| `repository`           | Repository in owner/repo format | No | `${{ github.repository }}` |
| `tag`                  | Tag to generate release notes for | No | `${{ github.ref_name }}` |
| `previous_tag`         | Previous tag to compare against | No | Auto-detected |

## Outputs

| Output                  | Description |
|-------------------------|-------------|
| `release_notes`         | Generated release notes including submodule changes |

## License

Apache License 2.0 - see [LICENSE](LICENSE) file for details.