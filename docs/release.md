# Releasing ntx

Releases are built and signed automatically by the SLSA Level 3 workflow when a version tag is pushed. The workflow produces binaries for linux/darwin × amd64/arm64 plus a signed provenance attestation.

## Prerequisites

- Write access to the repository
- `just` installed locally
- All changes merged to `main` and tests passing

## Steps

### 1. Update VERSION

```sh
echo "1.2.3" > VERSION
git add VERSION
git commit -m "Bump version to 1.2.3"
git push origin main
```

Wait for the CI test run on `main` to pass before tagging.

### 2. Tag the release

Tags must be prefixed with `v`. The SLSA workflow only triggers on `v*` tags.

```sh
git tag -a v1.2.3 -m "Release v1.2.3"
git push origin v1.2.3
```

### 3. Watch the workflow

Go to **Actions → SLSA Go releaser** in the GitHub UI. Two jobs run:

| Job | What it does |
|---|---|
| `args` | Extracts version, commit SHA, date, and tree state from git |
| `build` | Builds all four platform binaries, generates signed SLSA provenance, creates the GitHub release |

The `build` job calls the `slsa-framework/slsa-github-generator` reusable workflow. It will request permission to write to the repository — GitHub will show an approval prompt in the Actions UI if your repo requires it.

### 4. Verify the release

Once the workflow completes, check **Releases** in GitHub. The release should contain:

```
ntx-linux-amd64
ntx-linux-arm64
ntx-darwin-amd64
ntx-darwin-arm64
ntx.intoto.jsonl        ← SLSA provenance attestation
```

Release notes are auto-generated from commits since the previous tag.

### 5. Verify provenance locally (optional)

Install [`slsa-verifier`](https://github.com/slsa-framework/slsa-verifier) and run:

```sh
slsa-verifier verify-artifact ntx-linux-amd64 \
  --provenance-path ntx.intoto.jsonl \
  --source-uri github.com/nod/ntx \
  --source-tag v1.2.3
```

A clean result confirms the binary was built from the tagged commit in GitHub Actions and was not tampered with.

## If something goes wrong

**Workflow fails before the release is created** — delete the tag, fix the issue, re-tag:

```sh
git tag -d v1.2.3
git push origin :refs/tags/v1.2.3
# fix, then re-tag
```

**Release was created but is incomplete** — delete the release in the GitHub UI (Releases → Edit → Delete), delete the tag as above, and re-tag after fixing.

**Accidental tag on the wrong commit** — same as above. Never force-push a tag that has already triggered a release; delete and recreate cleanly.

## Version convention

ntx uses `MAJOR.MINOR.PATCH` without any `v` prefix in the `VERSION` file. The `v` prefix lives only in the git tag. The binary reports the version without the `v`:

```sh
ntx version   # prints: 1.2.3
```
