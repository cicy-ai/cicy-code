#!/usr/bin/env python3
import json
import os
import sys
import subprocess
import time
import signal
import argparse
import urllib.request
import urllib.error
import base64

PORT = int(os.environ.get("PORT", "8021"))
SQLITE_PATH = os.environ.get("SQLITE_PATH", f"{os.path.expanduser('~')}/.cicy/data-v1.db")
ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
API_DIR = os.path.join(ROOT_DIR, "api")
GLOBAL_JSON_PATH = os.path.expanduser("~/global.json")
VERSION_SYNC_SCRIPT = os.path.join(ROOT_DIR, "scripts", "sync-version.py")

AI_PROVIDER_ALIASES = {
    "2000run": "2000Run",
    "200run": "2000Run",
    "cicyai": "cicyAi",
}

def load_global_json():
    try:
        with open(GLOBAL_JSON_PATH, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}

def canonical_ai_provider_name(name):
    value = str(name or "").strip()
    if not value:
        return ""
    return AI_PROVIDER_ALIASES.get(value.lower(), value)

def default_ai_provider_config(name, data):
    canonical = canonical_ai_provider_name(name)
    cicy_ai_base = str(data.get("cicyAiUrl", "") or "").strip().rstrip("/")
    cicy_ai_api = f"{cicy_ai_base}/v1" if cicy_ai_base else ""
    defaults = {
        "2000Run": {
            "apiKey": data.get("2000RunApikey", ""),
            "apiUrl": "http://2000.run:6543/v1",
            "anthropicUrl": "http://2000.run:6543",
            "defaultOpencodeModel": "gpt-5.4",
            "defaultClaudeModel": "opus[1m]",
            "codexModel": "gpt-5.4",
            "openclawModel": "claude-sonnet-4-6",
        },
        "cicyAi": {
            "apiKey": data.get("cicyAiapikey", ""),
            "apiUrl": cicy_ai_api or "https://cicy-ai.com/v1",
            "anthropicUrl": cicy_ai_base or "https://cicy-ai.com",
            "defaultOpencodeModel": "gpt-5.4",
            "defaultClaudeModel": "opus[1m]",
            "codexModel": "gpt-5.4",
            "openclawModel": "claude-sonnet-4-6",
        },
    }
    return defaults.get(canonical, {})

def get_ai_provider_config(provider_name=""):
    data = load_global_json()
    ai = data.get("ai", {})
    provider_map = ai.get("provider", {}) if isinstance(ai, dict) else {}

    selected = canonical_ai_provider_name(
        provider_name
        or os.environ.get("CICY_AI_PROVIDER")
        or (ai.get("currentProvider", "") if isinstance(ai, dict) else "")
        or "cicyAi"
    ) or "cicyAi"

    config = dict(default_ai_provider_config(selected, data))
    for key, value in provider_map.items() if isinstance(provider_map, dict) else []:
        if canonical_ai_provider_name(key) != selected or not isinstance(value, dict):
            continue
        config.update({k: v for k, v in value.items() if v not in ("", None)})

    if not config.get("apiUrl") and config.get("baseUrl"):
        config["apiUrl"] = config["baseUrl"]
    return selected, config

def get_ai_env_defaults(provider_name=""):
    selected, config = get_ai_provider_config(provider_name)
    return {
        "CICY_AI_PROVIDER": selected,
        "CICY_API_KEY": os.environ.get("CICY_API_KEY") or config.get("apiKey", ""),
        "CICY_API_URL": os.environ.get("CICY_API_URL") or config.get("apiUrl", "http://2000.run:6543/v1"),
        "CICY_ANTHROPIC_URL": os.environ.get("CICY_ANTHROPIC_URL") or config.get("anthropicUrl", "http://2000.run:6543"),
        "CICY_DEFAULT_OPENCODE_MODEL": os.environ.get("CICY_DEFAULT_OPENCODE_MODEL") or os.environ.get("CICY_DEFAULT_MODEL") or config.get("defaultOpencodeModel") or config.get("defaultModel", "gpt-5.4"),
        "CICY_DEFAULT_CLAUDE_MODEL": os.environ.get("CICY_DEFAULT_CLAUDE_MODEL") or os.environ.get("CICY_CLAUDE_MODEL") or config.get("defaultClaudeModel") or config.get("claudeModel", "opus[1m]"),
        "CICY_CODEX_MODEL": os.environ.get("CICY_CODEX_MODEL") or config.get("codexModel", "gpt-5.4"),
        "CICY_OPENCLAW_MODEL": os.environ.get("CICY_OPENCLAW_MODEL") or config.get("openclawModel", "claude-sonnet-4-6"),
    }

def get_cicy_api_key():
    return get_ai_env_defaults().get("CICY_API_KEY", "")

def get_local_api_token():
    value = os.environ.get("CICY_API_TOKEN", "").strip()
    if value:
        return value
    data = load_global_json()
    return str(data.get("api_token", "") or "").strip()

def get_cicy_cluster():
    data = load_global_json().get("cicy-cluster", {})
    return data if isinstance(data, dict) else {}

def get_images_config():
    data = load_global_json().get("images", {})
    return data if isinstance(data, dict) else {}

def get_cloudrun_env():
    cluster = get_cicy_cluster()
    env = os.environ.copy()
    cloudrun_image = cluster.get("image", "") or cluster.get("image_repository", "")
    ai_env = get_ai_env_defaults()
    defaults = {
        "PROJECT": cluster.get("project_id", ""),
        "SERVICE": cluster.get("service", ""),
        "REGION": cluster.get("region", ""),
        "IMAGE": cloudrun_image,
        "MEMORY": cluster.get("memory", "2Gi"),
        "MAX_INSTANCES": cluster.get("max_instances", "1"),
        "MIN_INSTANCES": cluster.get("min_instances", "1"),
        "CONCURRENCY": cluster.get("concurrency", "1"),
        "CICY_PUBLIC_URL": cluster.get("service_url", ""),
        "CICY_API_TOKEN": cluster.get("api_token", ""),
        "CICY_INSTANCE_KEY": cluster.get("instance_key", ""),
        "CICY_INSTANCE_LABEL": cluster.get("instance_label", ""),
        **ai_env,
    }
    for key, value in defaults.items():
        if value and not env.get(key):
            env[key] = str(value)
    return env

def mask_secret(value, keep=4):
    if not value:
        return ""
    if len(value) <= keep * 2:
        return "*" * len(value)
    return f"{value[:keep]}...{value[-keep:]}"

def validate_cloudrun_env(env):
    required = ["PROJECT", "SERVICE", "REGION", "IMAGE", "CICY_API_TOKEN"]
    missing = [key for key in required if not env.get(key)]
    if missing:
        print(f"[dev] missing Cloud Run config: {', '.join(missing)}")
        print(f"[dev] checked env and {GLOBAL_JSON_PATH} -> cicy-cluster")
        sys.exit(1)

def validate_cloudrun_list_env(env):
    required = ["PROJECT", "REGION"]
    missing = [key for key in required if not env.get(key)]
    if missing:
        print(f"[dev] missing Cloud Run list config: {', '.join(missing)}")
        print(f"[dev] checked env and {GLOBAL_JSON_PATH} -> cicy-cluster")
        sys.exit(1)

def print_cloudrun_summary(env):
    print("[dev] Cloud Run config:")
    print(f"[dev]   project={env.get('PROJECT', '')}")
    print(f"[dev]   service={env.get('SERVICE', '')}")
    print(f"[dev]   region={env.get('REGION', '')}")
    print(f"[dev]   image={env.get('IMAGE', '')}")
    print(f"[dev]   memory={env.get('MEMORY', '')}")
    print(f"[dev]   min_instances={env.get('MIN_INSTANCES', '')}")
    print(f"[dev]   max_instances={env.get('MAX_INSTANCES', '')}")
    print(f"[dev]   concurrency={env.get('CONCURRENCY', '')}")
    print(f"[dev]   public_url={env.get('CICY_PUBLIC_URL', '')}")
    print(f"[dev]   instance_key={env.get('CICY_INSTANCE_KEY', '')}")
    print(f"[dev]   instance_label={env.get('CICY_INSTANCE_LABEL', '')}")
    print(f"[dev]   api_token={mask_secret(env.get('CICY_API_TOKEN', ''))}")
    print(f"[dev]   api_key={mask_secret(env.get('CICY_API_KEY', ''))}")

def print_access_urls(base_url, token, service_url=""):
    if not base_url or not token:
        return
    open_url = f"{base_url.rstrip('/')}/?token={token}"
    print(f"[dev] API Token: {token}")
    print(f"[dev] URL: {open_url}")
    if service_url and service_url.rstrip("/") != base_url.rstrip("/"):
        print(f"[dev] Service URL: {service_url.rstrip('/')}/?token={token}")

def get_version_info():
    try:
        result = subprocess.run(
            ["python3", VERSION_SYNC_SCRIPT, "--print-json"],
            cwd=ROOT_DIR,
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode == 0 and result.stdout.strip():
            data = json.loads(result.stdout)
            return data if isinstance(data, dict) else {}
    except Exception:
        pass
    return {}

def run_version_sync(version=""):
    cmd = ["python3", VERSION_SYNC_SCRIPT]
    version = str(version or "").strip()
    if version:
        cmd.extend(["--set", version])
    result = subprocess.run(cmd, cwd=ROOT_DIR, capture_output=True, text=True)
    if result.returncode != 0:
        output = (result.stdout or "").strip()
        err = (result.stderr or "").strip()
        if output:
            print(output)
        if err:
            print(err)
        sys.exit(result.returncode or 1)
    return get_version_info()

def get_binary_version():
    info = get_version_info()
    version = str(info.get("version", "")).strip()
    if version:
        return version
    try:
        result = subprocess.run(
            ["node", "-p", "require('./npm/package.json').version"],
            cwd=ROOT_DIR,
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    except Exception:
        pass
    print("[dev] failed to read version from npm/package.json")
    sys.exit(1)

def strip_image_tag(image_ref):
    if not image_ref:
        return ""
    image_ref = image_ref.split("@", 1)[0]
    slash = image_ref.rfind("/")
    colon = image_ref.rfind(":")
    if colon > slash:
        return image_ref[:colon]
    return image_ref

def get_image_tag(image_ref):
    if not image_ref:
        return ""
    image_ref = image_ref.split("@", 1)[0]
    slash = image_ref.rfind("/")
    colon = image_ref.rfind(":")
    if colon > slash:
        return image_ref[colon + 1:]
    return ""

def load_versions_json():
    path = os.path.join(ROOT_DIR, "versions.json")
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}

def local_default_base_tag():
    return str(load_versions_json().get("base", "") or "").strip() or "latest"

def local_default_base_image():
    data = load_global_json()
    images = data.get("images", {}) if isinstance(data, dict) else {}
    if isinstance(images, dict):
        explicit = str(images.get("base", "") or "").strip()
        if explicit:
            return explicit
        repo = str(images.get("base_repository", "") or "").strip()
        tag = str(images.get("base_tag", "") or "").strip()
        if repo and tag:
            return f"{repo}:{tag}"
    cluster = data.get("cicy-cluster", {}) if isinstance(data, dict) else {}
    if isinstance(cluster, dict):
        explicit = str(cluster.get("base_image", "") or "").strip()
        if explicit:
            return explicit
    return f"cicy-code-base:{local_default_base_tag()}"

def ensure_local_base_image_available():
    base_image = os.environ.get("BASE_IMAGE", "").strip() or local_default_base_image()
    if not base_image.startswith("cicy-code-base:"):
        return
    inspect = subprocess.run(["docker", "image", "inspect", base_image], capture_output=True, text=True)
    if inspect.returncode == 0:
        return
    tag = get_image_tag(base_image) or local_default_base_tag()
    print(f"[dev] Base image missing, building {base_image} ...")
    result = subprocess.run(["./build.sh", "docker-base", tag], cwd=ROOT_DIR)
    if result.returncode != 0:
        print("[dev] docker base build failed")
        sys.exit(1)
    return ""

def get_dockerhub_username():
    config_path = os.path.expanduser("~/.docker/config.json")
    try:
        with open(config_path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except Exception:
        return ""
    auth = ((data.get("auths") or {}).get("https://index.docker.io/v1/") or {}).get("auth", "")
    if not auth:
        return ""
    try:
        raw = base64.b64decode(auth).decode("utf-8", "ignore")
    except Exception:
        return ""
    return raw.split(":", 1)[0].strip()

def get_docker_image_repository():
    explicit = os.environ.get("DOCKER_IMAGE_REPOSITORY", "").strip()
    if explicit:
        return strip_image_tag(explicit)
    images = get_images_config()
    runtime_image = images.get("runtime", "")
    if isinstance(runtime_image, dict):
        runtime_image = runtime_image.get("image", "")
    runtime_image = str(runtime_image or "").strip()
    if runtime_image:
        return strip_image_tag(runtime_image)
    dockerhub_user = get_dockerhub_username()
    if dockerhub_user:
        return f"{dockerhub_user}/cicy-code-runtime"
    return ""

def get_current_runtime_image():
    images = get_images_config()
    runtime_image = images.get("runtime", "")
    if isinstance(runtime_image, dict):
        runtime_image = runtime_image.get("image", "")
    return str(runtime_image or "").strip()

def write_cloudrun_image_to_global_json(image_ref, tag):
    data = load_global_json()
    if not isinstance(data, dict):
        data = {}
    cluster = data.get("cicy-cluster", {})
    if not isinstance(cluster, dict):
        cluster = {}
    cluster["image"] = image_ref
    cluster["image_tag"] = tag
    cluster["image_repository"] = strip_image_tag(image_ref)
    data["cicy-cluster"] = cluster
    with open(GLOBAL_JSON_PATH, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")

def write_docker_image_to_global_json(image_ref, tag, repository):
    data = load_global_json()
    if not isinstance(data, dict):
        data = {}
    images = data.get("images", {})
    if not isinstance(images, dict):
        images = {}
    images["runtime"] = image_ref
    images["runtime_repository"] = repository
    images["runtime_tag"] = tag
    data["images"] = images
    with open(GLOBAL_JSON_PATH, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")

def print_docker_version():
    info = get_version_info()
    files = info.get("files", {}) if isinstance(info, dict) else {}
    package_version = str(info.get("version", "")).strip() or get_binary_version()
    runtime_image = get_current_runtime_image()
    runtime_repository = strip_image_tag(runtime_image) or get_images_config().get("runtime_repository", "") or get_docker_image_repository()
    runtime_tag = get_image_tag(runtime_image) or get_images_config().get("runtime_tag", "")
    print(f"[dev] package_version={package_version}")
    if files:
        print(f"[dev] mgr_version={files.get('mgr_main_go', '')}")
        print(f"[dev] ui_version={files.get('workspace_ui', '')}")
        print(f"[dev] tmux_version={files.get('cicy_tmux_conf', '')}")
    print(f"[dev] dockerhub_repository={runtime_repository}")
    print(f"[dev] current_runtime_image={runtime_image}")
    print(f"[dev] current_runtime_tag={runtime_tag}")
    sys.exit(0)

def bump_version(version):
    version = str(version or "").strip()
    if not version:
        print("[dev] missing bump version")
        sys.exit(1)
    info = run_version_sync(version)
    files = info.get("files", {}) if isinstance(info, dict) else {}
    final_version = str(info.get("version", "")).strip() or version
    print(f"[dev] bumped version={final_version}")
    if files:
        print(f"[dev] npm_version={files.get('npm_package', '')}")
        print(f"[dev] mgr_version={files.get('mgr_main_go', '')}")
        print(f"[dev] ui_version={files.get('workspace_ui', '')}")
        print(f"[dev] tmux_version={files.get('cicy_tmux_conf', '')}")
    sys.exit(0)

def set_docker_version(tag):
    tag = str(tag or "").strip()
    if not tag:
        print("[dev] missing docker version tag")
        sys.exit(1)
    repository = get_docker_image_repository()
    if not repository:
        print("[dev] missing Docker Hub target repository")
        print(f"[dev] set DOCKER_IMAGE_REPOSITORY or configure Docker Hub login in ~/.docker/config.json")
        sys.exit(1)
    image_ref = f"{repository}:{tag}"
    write_docker_image_to_global_json(image_ref, tag, repository)
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime={image_ref}")
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime_repository={repository}")
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime_tag={tag}")
    sys.exit(0)

def run_checked(cmd, cwd=None, env=None):
    result = subprocess.run(cmd, cwd=cwd, env=env)
    if result.returncode != 0:
        sys.exit(result.returncode)
    return result

def get_pid_on_port(port):
    try:
        result = subprocess.run(
            ["lsof", "-ti", f"TCP:{port}"],
            capture_output=True, text=True
        )
        return result.stdout.strip().split("\n")[0] if result.stdout.strip() else None
    except:
        return None

def kill_process(pid):
    try:
        os.kill(int(pid), signal.SIGTERM)
    except:
        pass

def wait_for_probe(url, timeout=180, interval=1):
    start = time.time()
    last_error = None
    while time.time() - start < timeout:
        try:
            with urllib.request.urlopen(url, timeout=5) as resp:
                if 200 <= resp.status < 500:
                    return time.time() - start
        except Exception as e:
            last_error = e
        time.sleep(interval)
    raise TimeoutError(f"probe timeout after {timeout}s: {url} ({last_error})")

def get_cloudrun_service_url(project, region, service):
    try:
        result = subprocess.run(
            [
                "gcloud", "run", "services", "describe", service,
                "--project", project,
                "--region", region,
                "--format=value(status.url)",
            ],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode == 0:
            return result.stdout.strip()
    except Exception:
        pass
    return ""

def list_cloudrun_services(project, region):
    try:
        result = subprocess.run(
            [
                "gcloud", "run", "services", "list",
                "--project", project,
                "--region", region,
                "--format=json",
            ],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode != 0:
            err = (result.stderr or result.stdout or "").strip()
            print(f"[dev] cloud run list failed: {err}")
            sys.exit(result.returncode or 1)
        data = json.loads(result.stdout or "[]")
        return data if isinstance(data, list) else []
    except FileNotFoundError:
        print("[dev] gcloud not found")
        sys.exit(1)
    except json.JSONDecodeError as e:
        print(f"[dev] invalid gcloud json output: {e}")
        sys.exit(1)
    except Exception as e:
        print(f"[dev] cloud run list error: {e}")
        sys.exit(1)

def run_cloudrun_list():
    env = get_cloudrun_env()
    validate_cloudrun_list_env(env)
    project = env.get("PROJECT", "")
    region = env.get("REGION", "")
    configured_service = env.get("SERVICE", "")
    token = env.get("CICY_API_TOKEN", "")

    print(f"[dev] Listing Cloud Run services for project={project} region={region}")
    services = list_cloudrun_services(project, region)
    if not services:
        print("[dev] No Cloud Run services found.")
        sys.exit(0)

    for svc in services:
        metadata = svc.get("metadata", {}) if isinstance(svc, dict) else {}
        status = svc.get("status", {}) if isinstance(svc, dict) else {}
        spec = svc.get("spec", {}) if isinstance(svc, dict) else {}
        name = metadata.get("name", "")
        url = status.get("url", "")
        ready = "unknown"
        conditions = status.get("conditions", [])
        if isinstance(conditions, list):
            for cond in conditions:
                if cond.get("type") == "Ready":
                    ready = cond.get("status", "unknown")
                    break
        latest = status.get("latestReadyRevisionName", "")
        service_account = spec.get("template", {}).get("spec", {}).get("serviceAccountName", "")
        marker = " *" if name == configured_service else ""

        print(f"[dev] Service: {name}{marker}")
        print(f"[dev]   ready={ready}")
        if latest:
            print(f"[dev]   revision={latest}")
        if service_account:
            print(f"[dev]   service_account={service_account}")
        if url:
            print(f"[dev]   service_url={url}")
            if token:
                print(f"[dev]   url={url.rstrip('/')}/?token={token}")
        else:
            print("[dev]   service_url=")

    if configured_service:
        print(f"[dev] * configured service from env/global.json: {configured_service}")
    sys.exit(0)

def run_docker_build(version_override=""):
    info = run_version_sync(version_override)
    version = str(info.get("version", "")).strip() or str(version_override or "").strip() or get_binary_version()
    repository = get_docker_image_repository()
    if not repository:
        print("[dev] missing Docker Hub target repository")
        print(f"[dev] set DOCKER_IMAGE_REPOSITORY or configure Docker Hub login in ~/.docker/config.json")
        sys.exit(1)
    target_image = f"{repository}:{version}"
    latest_image = f"{repository}:latest"

    print(f"[dev] Docker build version: {version}")
    print(f"[dev] Build local image tag: cicy-code:{version}")
    print(f"[dev] Push target image: {target_image}")
    print(f"[dev] Push latest image: {latest_image}")

    run_checked(["./build.sh", "docker", version], cwd=ROOT_DIR)
    run_checked(["docker", "tag", f"cicy-code:{version}", target_image], cwd=ROOT_DIR)
    run_checked(["docker", "tag", f"cicy-code:{version}", latest_image], cwd=ROOT_DIR)
    run_checked(["docker", "push", target_image], cwd=ROOT_DIR)
    run_checked(["docker", "push", latest_image], cwd=ROOT_DIR)

    write_docker_image_to_global_json(target_image, version, repository)
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime={target_image}")
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime_repository={repository}")
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime_tag={version}")
    print(f"[dev] Next deploy: use ~/global.json images.runtime")
    sys.exit(0)

def run_docker(ports):
    run_version_sync()
    print(f"[dev] Building and running Docker...")
    ensure_local_base_image_available()
    result = subprocess.run(["./build.sh", "docker", "latest"], cwd=ROOT_DIR)
    if result.returncode != 0:
        print("[dev] docker build failed")
        sys.exit(1)
    
    container_name = "cicy-code-dev"
    subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)
    
    # Pass AI config env vars to docker
    env_vars = []
    for key, value in get_ai_env_defaults().items():
        env_vars.extend(["-e", f"{key}={value}"])
    local_api_token = get_local_api_token()
    if local_api_token:
        env_vars.extend(["-e", f"CICY_API_TOKEN={local_api_token}"])
    
    run_cmd = [
        "docker", "run", "-d",
        "--name", container_name,
    ] + env_vars + [
        "-p", f"{ports}:8008",
        "cicy-code:latest", "--public", "--agents=all"
    ]
    print(f"[dev] docker run: {' '.join(run_cmd)}")
    docker_run_started_at = time.time()
    subprocess.run(run_cmd)
    print(f"[dev] Docker started on port {ports}")

    probe_url = f"http://localhost:{ports}/api/health"
    print(f"[dev] Waiting for probe: {probe_url}")
    try:
        probe_elapsed = wait_for_probe(probe_url)
        print(f"[dev] Probe ready in {probe_elapsed:.1f}s (since docker run)")
    except Exception as e:
        print(f"[dev] Probe failed: {e}")
        probe_elapsed = time.time() - docker_run_started_at
        print(f"[dev] Elapsed since docker run: {probe_elapsed:.1f}s")

    version_result = subprocess.run(
        ["docker", "exec", container_name, "sh", "-lc", "openclaw --version"],
        capture_output=True, text=True
    )
    if version_result.returncode == 0 and version_result.stdout.strip():
        print(f"[dev] OpenClaw: {version_result.stdout.strip()}")
    else:
        version_err = (version_result.stderr or "").strip()
        if version_err:
            print(f"[dev] OpenClaw version check failed: {version_err}")

    # Get public IP
    pub_ip = "localhost"
    try:
        result = subprocess.run(
            ["curl", "-s", "ifconfig.me"],
            capture_output=True, text=True, timeout=5
        )
        if result.returncode == 0 and result.stdout.strip():
            pub_ip = result.stdout.strip()
    except:
        pass

    # Get and display the API token
    token = local_api_token
    if not token:
        result = subprocess.run(
            ["docker", "exec", container_name, "cat", "/root/global.json"],
            capture_output=True, text=True
        )
        if result.returncode == 0:
            import json
            try:
                config = json.loads(result.stdout)
                token = config.get("api_token", "")
            except Exception as e:
                print(f"Error: {e}")
    if token:
        print(f"[dev] API Token: {token}")
        print(f"[dev] URL: http://{pub_ip}:{ports}/?token={token}")
        print(f"[dev] Token available in {time.time() - docker_run_started_at:.1f}s (since docker run)")

        # Test agents
        print("[dev] Testing agents...")
        test_agents(container_name, token, ports)
    
    sys.exit(0)


def test_agents(container_name, token, port):
    """Test each agent by checking ttyd port connectivity"""
    import urllib.request
    import urllib.error

    base_url = f"http://localhost:{port}"

    # Get panes with retry
    panes = None
    for attempt in range(3):
        try:
            req = urllib.request.Request(f"{base_url}/api/panes?token={token}")
            with urllib.request.urlopen(req, timeout=10) as resp:
                panes = json.loads(resp.read())["panes"]
            break
        except Exception as e:
            if attempt < 2:
                time.sleep(3)
            else:
                print(f"  ❌ Failed to get panes after 3 attempts: {e}")
                return

    if not panes:
        print("  ❌ No panes found")
        return

    print(f"\n[dev] Found {len(panes)} agents:")
    for pane in panes:
        title = pane.get("title", "")
        pane_id = pane.get("pane_id", "")
        agent_type = pane.get("agent_type", "")
        ttyd_port = pane.get("ttyd_port", 0)
        active = pane.get("active", 0)

        status = "✅" if active == 1 else "❌"
        print(f"  {status} {title} (type: {agent_type}, port: {ttyd_port})")

        # Check if ttyd is running
        if ttyd_port > 0:
            try:
                req = urllib.request.Request(f"http://localhost:{ttyd_port}")
                urllib.request.urlopen(req, timeout=5)
                print(f"     ✅ ttyd port {ttyd_port} is accessible")
            except urllib.error.URLError:
                print(f"     ❌ ttyd port {ttyd_port} not accessible")
            except Exception as e:
                print(f"     ❌ ttyd port {ttyd_port} error: {e}")

    print("\n[dev] Agent test completed.")

def run_cloudrun():
    env = get_cloudrun_env()
    validate_cloudrun_env(env)
    env["PREBUILD"] = "0"
    env["BUILD_IMAGE"] = "0"
    started_at = time.time()
    print(f"[dev] Cloud Run trigger started at {time.strftime('%Y-%m-%d %H:%M:%S', time.gmtime(started_at))} UTC")
    print_cloudrun_summary(env)
    print("[dev] Deploying to Cloud Run via scripts/deploy-cloudrun.sh")
    result = subprocess.run(
        ["./scripts/deploy-cloudrun.sh"],
        cwd=ROOT_DIR,
        env=env,
    )
    elapsed = time.time() - started_at
    print(f"[dev] Deploy command finished in {elapsed:.1f}s")
    if result.returncode == 0:
        token = env.get("CICY_API_TOKEN", "")
        project = env.get("PROJECT", "")
        region = env.get("REGION", "")
        service = env.get("SERVICE", "")
        service_url = get_cloudrun_service_url(project, region, service) if project and region and service else ""
        public_url = env.get("CICY_PUBLIC_URL", "") or service_url
        if service_url:
            probe_url = f"{service_url.rstrip('/')}/api/health"
            print(f"[dev] Waiting for probe: {probe_url}")
            try:
                probe_delay = wait_for_probe(probe_url, timeout=180, interval=2)
                total_elapsed = time.time() - started_at
                print(f"[dev] Cloud Run probe ready in {total_elapsed:.1f}s (since trigger)")
                print(f"[dev] Probe check time after deploy: {probe_delay:.1f}s")
            except Exception as e:
                print(f"[dev] Probe failed: {e}")
                print(f"[dev] Elapsed since trigger: {time.time() - started_at:.1f}s")
        print_access_urls(public_url, token, service_url)
    sys.exit(result.returncode)

def run_ttyd_assets():
    print("[dev] Rebuilding ttyd embedded assets via `make asset`...")
    run_checked(["make", "asset"], cwd=API_DIR)
    print("[dev] ttyd assets rebuilt.")
    sys.exit(0)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--docker", action="store_true", help="Build and run Docker container")
    parser.add_argument("--dockerBuild", "--docker-build", "--cloudRunBuild", "--cloudrun-build", dest="dockerBuild", action="store_true", help="Build image, push to Docker Hub, then update ~/global.json images.runtime")
    parser.add_argument("--dockerBuildVersion", "--docker-build-version", dest="dockerBuildVersion", default="", help="Override version tag used by --dockerBuild")
    parser.add_argument("--dockerVersion", "--docker-version", dest="dockerVersion", action="store_true", help="Show current package version and configured images.runtime")
    parser.add_argument("--dockerSetVersion", "--docker-set-version", dest="dockerSetVersion", default="", help="Update ~/global.json images.runtime to the specified tag without building")
    parser.add_argument("--bumpVersion", "--bump-version", dest="bumpVersion", default="", help="Set runtime version and sync all version targets")
    parser.add_argument("--cloudRun", "--cloudrun", dest="cloudRun", action="store_true", help="Deploy to Cloud Run using scripts/deploy-cloudrun.sh")
    parser.add_argument("--cloudRunList", "--cloudrun-list", dest="cloudRunList", action="store_true", help="List Cloud Run services for current project/region")
    parser.add_argument("--ttydAssets", "--ttyd-assets", dest="ttydAssets", action="store_true", help="Rebuild embedded ttyd/goTTY static assets via api/Makefile `make asset`")
    parser.add_argument("--port", type=int, default=8026, help="Base port for Docker (default: 8026)")
    args = parser.parse_args()

    if args.docker:
        run_docker(args.port)
    if args.dockerVersion:
        print_docker_version()
    if args.bumpVersion:
        bump_version(args.bumpVersion)
    if args.dockerSetVersion:
        set_docker_version(args.dockerSetVersion)
    if args.dockerBuild:
        run_docker_build(args.dockerBuildVersion)
    if args.cloudRun:
        run_cloudrun()
    if args.cloudRunList:
        run_cloudrun_list()
    if args.ttydAssets:
        run_ttyd_assets()

    existing_pid = get_pid_on_port(PORT)
    if existing_pid:
        try:
            cmd = subprocess.run(["ps", "-p", existing_pid, "-o", "command="],
                               capture_output=True, text=True).stdout.strip()
            if "cicy-code" in cmd:
                print(f"[dev] stop existing cicy process on :{PORT} (pid={existing_pid})")
                kill_process(existing_pid)
                for _ in range(30):
                    if not get_pid_on_port(PORT):
                        break
                    time.sleep(0.2)
            else:
                print(f"[dev] port {PORT} is in use by non-cicy process: {cmd}")
                sys.exit(1)
        except:
            pass

    platform = "darwin" if sys.platform == "darwin" else "linux"
    os.environ["SKIP_NPM"] = "1"
    os.environ["SQLITE_PATH"] = SQLITE_PATH
    for key, value in get_ai_env_defaults().items():
        os.environ[key] = value
    run_version_sync()

    result = subprocess.run(["./build.sh", "build", platform], cwd=ROOT_DIR)
    if result.returncode != 0:
        print("[dev] build failed, not starting")
        sys.exit(1)

    os.execl(os.path.join(API_DIR, "cicy-code"), "cicy-code", "--public", "--dev")

if __name__ == "__main__":
    main()
