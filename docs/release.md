# Releasing ntx

Pushing a `v*` tag releases ntx. The **Go** workflow (`.github/workflows/go.yml`) runs the tests, builds linux/darwin × amd64/arm64 binaries, publishes them as a GitHub release, and updates the Homebrew formula on `main`. There is no version file: the version is the tag.

## Prerequisites

- Write access to the repository
- All changes merged to `main`, and the CI run on `main` green

## Steps

### 1. Tag the release

Tags are `v` + `MAJOR.MINOR.PATCH`:

```sh
git switch main && git pull
git tag -a v1.2.3 -m "Release v1.2.3"
git push origin v1.2.3
```

A tag with a suffix (`v1.2.3-rc1`) is published as a **pre-release**: it is not marked "latest", so `install.sh` won't pick it up, and the Homebrew formula is left alone.

### 2. Watch the workflow

**Actions → Go** shows three jobs for the tag:

| Job | What it does |
|---|---|
| `test` | `go vet` and `go test` |
| `release` | Builds the four binaries, checks `ntx version` prints the tag's version, and creates the GitHub release with auto-generated notes |
| `formula` | Regenerates `Formula/ntx.rb` with the new version and checksums (`scripts/gen-formula.sh`) and commits it to `main` as `github-actions[bot]` |

### 3. Verify the release

The release should contain:

```
ntx-linux-amd64
ntx-linux-arm64
ntx-darwin-amd64
ntx-darwin-arm64
checksums.txt
```

and `main` should have a new commit, "Update Homebrew formula to v1.2.3". Then:

```sh
brew update && brew upgrade ntx      # or: brew tap nod/ntx https://github.com/nod/ntx && brew install ntx
ntx version                          # prints: 1.2.3
```

To check a downloaded binary by hand: `sha256sum -c checksums.txt --ignore-missing`.

## Building locally

`just build-all` produces the same four binaries and `checksums.txt` in `build/`, stamped with `git describe` (e.g. `1.2.3`, or `1.2.3-4-gabc1234-dirty` between tags). `just formula` prints the formula those binaries would get.

## If something goes wrong

**Workflow fails before the release is created**: delete the tag, fix the issue, re-tag:

```sh
git tag -d v1.2.3
git push origin :refs/tags/v1.2.3
# fix, then re-tag
```

**Release was created but is incomplete**: delete the release in the GitHub UI (Releases → Edit → Delete), delete the tag as above, and re-tag after fixing.

**The `formula` job failed** (for example, branch protection on `main` rejects the bot's push): the release itself is fine. Update the formula by hand from the release's `checksums.txt`:

```sh
curl -sSfLO https://github.com/nod/ntx/releases/download/v1.2.3/checksums.txt
scripts/gen-formula.sh 1.2.3 checksums.txt > Formula/ntx.rb
```

then commit it to `main`, or re-run the job from the Actions UI once the cause is fixed.

**Accidental tag on the wrong commit**: same as the first case. Never force-push a tag that has already triggered a release; delete it and recreate it cleanly.
