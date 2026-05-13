---
name: docker-build-github-action
description: Solve slow or stuck local Docker builds by creating, committing, and triggering a config-driven GitHub Actions workflow that builds a specified Dockerfile, especially base development images, with BuildKit cache and pushes the tagged image to GHCR. Use when the user wants Docker builds moved off the local machine, wants concise commands backed by a config file, or needs Codex/Claude to publish base images through GitHub Actions.
---

# Docker Build GitHub Action

Use this skill to move slow local Docker builds, especially base development image builds, to GitHub Actions and GHCR.

## Config First

Keep build settings in `~/cicy-ai/db/docker-build-ghcr.json`. Command-line usage should stay short.

Create a starter config:

```bash
python3 scripts/create_workflow.py --init-config
```

Example config:

```json
{
  "repo": "cicy-ai/cicy-code",
  "dockerfile": "api/Dockerfile.runtime.base",
  "local_dockerfile": "/home/cicy/projects/cicy-code/api/Dockerfile.runtime.base",
  "context": "api",
  "image": "ghcr.io/cicy-ai/cicy-code-base",
  "tag": "1.0.7",
  "platforms": "linux/amd64",
  "build_args": {
    "BASE_DOCKERFILE_HASH": "caee2a25225a8bdf8e8424d191d1cb92d16d50adc7b9ffca241e4dcd542381f0"
  },
  "no_cache": false,
  "branch": "",
  "workflow_path": ".github/workflows/ghcr-docker-build.yml"
}
```

Normal commands:

```bash
python3 scripts/create_workflow.py --print
python3 scripts/create_workflow.py --commit
python3 scripts/create_workflow.py --run --watch
python3 scripts/create_workflow.py --commit --run --watch
```

Use `--config <path>` only when the config is not `~/cicy-ai/db/docker-build-ghcr.json`.

## Required Inputs

- GitHub repo: `owner/name`
- GitHub token source: existing `gh` login, `GITHUB_TOKEN`, or `GH_TOKEN`
- Dockerfile path in the target repo, or a local Dockerfile to upload
- GHCR image name, for example `ghcr.io/cicy-ai/cicy-code-base`
- Image tag, for example `1.0.7`

The workflow uses GitHub Actions `GITHUB_TOKEN` to push to GHCR.

## Workflow Rules

- Use `docker/setup-buildx-action` and `docker/build-push-action`.
- Use `docker/login-action` with `registry: ghcr.io`, `${{ github.actor }}`, and `${{ secrets.GITHUB_TOKEN }}`.
- Keep `workflow_dispatch` inputs for `image`, `tag`, `dockerfile`, `context`, `platforms`, `build_args`, `no_cache`, and `push`.
- Default `push` to `true`.
- Prefer `linux/amd64` unless the config asks for multi-platform builds.
- Use BuildKit GitHub Actions cache: `cache-from: type=gha` and `cache-to: type=gha,mode=max`.
- If the Dockerfile uses repo-local files, set `context` to the correct repo directory.

## Common Recovery

If a local build is stuck on apt, package downloads, or network:

1. Ensure `~/cicy-ai/db/docker-build-ghcr.json` has the correct repo, Dockerfile, context, image, and tag.
2. Run `python3 scripts/create_workflow.py --commit --run --watch`.
3. Verify the image with `docker manifest inspect ghcr.io/<owner>/<image>:<tag>`.
