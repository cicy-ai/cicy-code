# GHCR Docker Build Tool

## create_workflow.py

Creates a config-driven GitHub Actions workflow for cached Docker builds that publish to GHCR.

Configuration lives in `~/cicy-ai/db/docker-build-ghcr.json` by default.

Required config fields:

- `repo`: GitHub repository as `owner/name`
- `dockerfile`: Dockerfile path in the target repo
- `context`: Docker build context in the target repo
- `image`: GHCR image, for example `ghcr.io/cicy-ai/cicy-code-base`
- `tag`: image tag
- `platforms`: build platforms, usually `linux/amd64`

Optional config fields:

- `local_dockerfile`: local Dockerfile to upload to `dockerfile` when committing
- `build_args`: object, list, or newline string of Docker build args
- `no_cache`: boolean
- `branch`: target branch; empty means repo default branch
- `workflow_path`: default `.github/workflows/ghcr-docker-build.yml`

Commands:

- `python3 scripts/create_workflow.py --init-config`
- `python3 scripts/create_workflow.py --print`
- `python3 scripts/create_workflow.py --commit`
- `python3 scripts/create_workflow.py --run --watch`
- `python3 scripts/create_workflow.py --commit --run --watch`
- `python3 scripts/create_workflow.py --config path/to/config.json --commit --run`
