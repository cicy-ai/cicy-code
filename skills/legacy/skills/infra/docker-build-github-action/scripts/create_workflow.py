#!/usr/bin/env python3
import argparse
import base64
import json
import os
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.request


API = "https://api.github.com"
DEFAULT_CONFIG = "~/cicy-ai/db/docker-build-ghcr.json"


def workflow_yaml(config):
    build_args = "\n".join(config["build_args"])
    return f"""name: GHCR Docker Build

on:
  workflow_dispatch:
    inputs:
      image:
        description: GHCR image, for example ghcr.io/owner/name
        required: true
        default: {config["image"]}
      tag:
        description: Image tag
        required: true
        default: {config["tag"]}
      dockerfile:
        description: Dockerfile path in this repository
        required: true
        default: {config["dockerfile"]}
      context:
        description: Docker build context
        required: true
        default: {config["context"]}
      platforms:
        description: Build platforms
        required: true
        default: {config["platforms"]}
      build_args:
        description: Docker build args, one KEY=VALUE per line
        required: false
        default: |
{indent_yaml_block(build_args, 10)}
      no_cache:
        description: Disable BuildKit cache for this run
        type: boolean
        required: true
        default: {str(config["no_cache"]).lower()}
      push:
        description: Push image to GHCR
        type: boolean
        required: true
        default: true

jobs:
  docker:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Login to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{{{ github.actor }}}}
          password: ${{{{ secrets.GITHUB_TOKEN }}}}

      - name: Build and optionally push
        uses: docker/build-push-action@v6
        with:
          context: ${{{{ inputs.context }}}}
          file: ${{{{ inputs.dockerfile }}}}
          platforms: ${{{{ inputs.platforms }}}}
          push: ${{{{ inputs.push }}}}
          no-cache: ${{{{ inputs.no_cache }}}}
          build-args: ${{{{ inputs.build_args }}}}
          cache-from: type=gha
          cache-to: type=gha,mode=max
          tags: |
            ${{{{ inputs.image }}}}:${{{{ inputs.tag }}}}
"""


def indent_yaml_block(text, spaces):
    prefix = " " * spaces
    if not text:
        return prefix
    return "\n".join(prefix + line for line in text.splitlines())


def github_token():
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if token:
        return token
    gh = shutil.which("gh")
    if gh:
        result = subprocess.run([gh, "auth", "token"], capture_output=True, text=True)
        token = result.stdout.strip()
        if result.returncode == 0 and looks_like_token(token):
            return token
    token = github_token_from_hosts()
    if token:
        return token
    raise SystemExit("missing GitHub auth: run gh auth login or set GITHUB_TOKEN/GH_TOKEN")


def looks_like_token(token):
    return bool(re.fullmatch(r"[A-Za-z0-9_./:=+-]{20,}", token.strip()))


def github_token_from_hosts():
    path = os.path.expanduser("~/.config/gh/hosts.yml")
    try:
        with open(path, "r", encoding="utf-8") as f:
            for line in f:
                stripped = line.strip()
                if not stripped.startswith("oauth_token:"):
                    continue
                token = stripped.split(":", 1)[1].strip().strip('"').strip("'")
                if looks_like_token(token):
                    return token
    except Exception:
        return ""
    return ""


def request_json(method, url, token, body=None):
    headers = {
        "Accept": "application/vnd.github+json",
        "Authorization": f"Bearer {token}",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "cicy-ghcr-docker-build",
    }
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read()
            return json.loads(raw.decode("utf-8")) if raw else {}
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")
        raise SystemExit(f"GitHub API {method} {url} failed: HTTP {exc.code}: {detail}") from exc


def repo_info(repo, token):
    return request_json("GET", f"{API}/repos/{repo}", token)


def get_file_sha(repo, path, branch, token):
    url = f"{API}/repos/{repo}/contents/{path}?ref={branch}"
    try:
        data = request_json("GET", url, token)
        return data.get("sha")
    except SystemExit as exc:
        if "HTTP 404" in str(exc):
            return None
        raise


def put_file(repo, path, content, branch, message, token):
    sha = get_file_sha(repo, path, branch, token)
    body = {
        "message": message,
        "content": base64.b64encode(content.encode("utf-8")).decode("ascii"),
        "branch": branch,
    }
    if sha:
        body["sha"] = sha
    request_json("PUT", f"{API}/repos/{repo}/contents/{path}", token, body)


def run_checked(cmd, input_text=None):
    result = subprocess.run(cmd, input=input_text, text=True, capture_output=True)
    if result.returncode != 0:
        stderr = result.stderr.strip()
        stdout = result.stdout.strip()
        raise SystemExit(f"command failed: {' '.join(cmd)}\n{stderr or stdout}")
    return result.stdout


def run_workflow(config, branch, watch):
    gh = shutil.which("gh")
    if not gh:
        raise SystemExit("gh CLI is required for --run")
    workflow_name = config["workflow_path"].split("/")[-1]
    cmd = [
        gh, "workflow", "run", workflow_name,
        "-R", config["repo"],
        "--ref", branch,
        "-f", f"image={config['image']}",
        "-f", f"tag={config['tag']}",
        "-f", f"dockerfile={config['dockerfile']}",
        "-f", f"context={config['context']}",
        "-f", f"platforms={config['platforms']}",
        "-f", f"build_args={chr(10).join(config['build_args'])}",
        "-f", f"no_cache={str(config['no_cache']).lower()}",
        "-f", "push=true",
    ]
    run_checked(cmd)
    print(f"triggered {workflow_name} on {config['repo']}@{branch} for {config['image']}:{config['tag']}")
    if watch:
        subprocess.run([gh, "run", "watch", "-R", config["repo"]])


def default_config():
    return {
        "repo": "owner/name",
        "dockerfile": "api/Dockerfile.runtime.base",
        "local_dockerfile": "",
        "context": "api",
        "image": "ghcr.io/owner/cicy-code-base",
        "tag": "1.0.7",
        "platforms": "linux/amd64",
        "build_args": [
            "BASE_DOCKERFILE_HASH=replace-me"
        ],
        "no_cache": False,
        "branch": "",
        "workflow_path": ".github/workflows/ghcr-docker-build.yml"
    }


def load_config(path):
    path = expand_path(path)
    if not os.path.exists(path):
        raise SystemExit(f"missing config file {path}; create it or run --init-config")
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    config = default_config()
    config.update(data)
    config["build_args"] = normalize_build_args(config.get("build_args", []))
    validate_config(config, path)
    return config


def normalize_build_args(value):
    if isinstance(value, dict):
        return [f"{key}={val}" for key, val in value.items()]
    if isinstance(value, list):
        return [str(item) for item in value if str(item).strip()]
    if isinstance(value, str) and value.strip():
        return [line.strip() for line in value.splitlines() if line.strip()]
    return []


def validate_config(config, path):
    required = ["repo", "dockerfile", "context", "image", "tag", "platforms", "workflow_path"]
    missing = [key for key in required if not str(config.get(key, "")).strip()]
    if missing:
        raise SystemExit(f"{path} missing required fields: {', '.join(missing)}")
    if not config["image"].startswith("ghcr.io/"):
        raise SystemExit(f"{path} image must start with ghcr.io/: {config['image']}")


def write_config(path):
    path = expand_path(path)
    if os.path.exists(path):
        raise SystemExit(f"config already exists: {path}")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(default_config(), f, indent=2)
        f.write("\n")
    print(f"wrote {path}")


def expand_path(path):
    return os.path.abspath(os.path.expanduser(path))


def parse_args():
    parser = argparse.ArgumentParser(description="Create, commit, and run a config-driven GHCR Docker build workflow.")
    parser.add_argument("--config", default=DEFAULT_CONFIG, help=f"Config file path; defaults to {DEFAULT_CONFIG}")
    parser.add_argument("--init-config", action="store_true", help="Write a starter config file and exit")
    parser.add_argument("--commit", action="store_true", help="Commit workflow, and optional local Dockerfile, to GitHub")
    parser.add_argument("--run", action="store_true", help="Trigger the workflow")
    parser.add_argument("--watch", action="store_true", help="Watch the triggered workflow with gh run watch")
    parser.add_argument("--output", default="", help="Write workflow YAML to a local file")
    parser.add_argument("--print", action="store_true", help="Print workflow YAML")
    return parser.parse_args()


def main():
    args = parse_args()
    if args.init_config:
        write_config(args.config)
        return

    config = load_config(args.config)
    yaml_text = workflow_yaml(config)

    if args.print:
        print(yaml_text, end="")

    if args.output:
        with open(args.output, "w", encoding="utf-8") as f:
            f.write(yaml_text)
        print(f"wrote {args.output}", file=sys.stderr)

    if not args.commit and not args.run:
        return

    token = github_token()
    info = repo_info(config["repo"], token)
    branch = config["branch"] or info.get("default_branch") or "main"

    local_dockerfile = str(config.get("local_dockerfile", "")).strip()
    if args.commit and local_dockerfile:
        with open(local_dockerfile, "r", encoding="utf-8") as f:
            dockerfile_text = f.read()
        put_file(
            config["repo"],
            config["dockerfile"],
            dockerfile_text,
            branch,
            f"Add Dockerfile for GHCR build: {config['dockerfile']}",
            token,
        )
        print(f"committed {config['dockerfile']} to {config['repo']}@{branch}")

    if args.commit:
        put_file(
            config["repo"],
            config["workflow_path"],
            yaml_text,
            branch,
            f"Add GHCR Docker build workflow for {config['image']}:{config['tag']}",
            token,
        )
        print(f"committed {config['workflow_path']} to {config['repo']}@{branch}")

    if args.run:
        run_workflow(config, branch, args.watch)


if __name__ == "__main__":
    main()
