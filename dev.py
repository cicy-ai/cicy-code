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
import tempfile
import shutil
from pathlib import Path

PORT = 8008
ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
API_DIR = os.path.join(ROOT_DIR, "api")
CICY_ROOT_DIR = os.path.expanduser("~/cicy-ai")
CICY_STATE_DIR = os.path.join(CICY_ROOT_DIR, ".cicy")
HOST_PROJECTS_DIR = os.path.expanduser("~/projects")
CICY_DOCKER_HOMES_DIR = os.path.expanduser("~/docker-homes")
CICY_GLOBAL_JSON_PATH = os.path.join(CICY_ROOT_DIR, "global.json")
CICY_PROXY_JSON_PATH = os.path.join(CICY_ROOT_DIR, "proxy.json")
DOCKER_HOME_DIR = "/home/cicy"
DOCKER_PROJECTS_DIR = f"{DOCKER_HOME_DIR}/projects"
LEGACY_PROXY_JSON_PATH = os.path.expanduser("~/proxy.json")
SQLITE_PATH = os.environ.get(
    "SQLITE_PATH", os.path.join(CICY_ROOT_DIR, "db", "data.db")
)
GLOBAL_JSON_PATH = CICY_GLOBAL_JSON_PATH
PROXY_JSON_PATH = CICY_PROXY_JSON_PATH
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
            "openclawModel": "gpt-5.5",
            "hermesModel": "gpt-5.5",
        },
        "cicyAi": {
            "apiKey": data.get("cicyAiapikey", ""),
            "apiUrl": cicy_ai_api or "https://cicy-ai.com/v1",
            "anthropicUrl": cicy_ai_base or "https://cicy-ai.com",
            "defaultOpencodeModel": "gpt-5.4",
            "defaultClaudeModel": "opus[1m]",
            "codexModel": "gpt-5.4",
            "openclawModel": "gpt-5.5",
            "hermesModel": "gpt-5.5",
        },
    }
    return defaults.get(canonical, {})


def get_ai_provider_config(provider_name=""):
    data = load_global_json()
    ai = data.get("ai", {})
    provider_map = ai.get("provider", {}) if isinstance(ai, dict) else {}

    selected = (
        canonical_ai_provider_name(
            provider_name
            or os.environ.get("CICY_AI_PROVIDER")
            or (ai.get("currentProvider", "") if isinstance(ai, dict) else "")
            or "cicyAi"
        )
        or "cicyAi"
    )

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
        "CICY_API_URL": os.environ.get("CICY_API_URL")
        or config.get("apiUrl", "http://2000.run:6543/v1"),
        "CICY_ANTHROPIC_URL": os.environ.get("CICY_ANTHROPIC_URL")
        or config.get("anthropicUrl", "http://2000.run:6543"),
        "CICY_DEFAULT_OPENCODE_MODEL": os.environ.get("CICY_DEFAULT_OPENCODE_MODEL")
        or os.environ.get("CICY_DEFAULT_MODEL")
        or config.get("defaultOpencodeModel")
        or config.get("defaultModel", "gpt-5.4"),
        "CICY_DEFAULT_CLAUDE_MODEL": os.environ.get("CICY_DEFAULT_CLAUDE_MODEL")
        or os.environ.get("CICY_CLAUDE_MODEL")
        or config.get("defaultClaudeModel")
        or config.get("claudeModel", "opus[1m]"),
        "CICY_CODEX_MODEL": os.environ.get("CICY_CODEX_MODEL")
        or config.get("codexModel", "gpt-5.4"),
        "CICY_OPENCLAW_MODEL": os.environ.get("CICY_OPENCLAW_MODEL")
        or config.get("openclawModel", "gpt-5.5"),
        "CICY_HERMES_MODEL": os.environ.get("CICY_HERMES_MODEL")
        or config.get("hermesModel", "gpt-5.5"),
    }


def get_cicy_api_key():
    return get_ai_env_defaults().get("CICY_API_KEY", "")


def build_minimal_runtime_global_json():
    source = load_global_json()
    data = {}
    token = get_local_api_token()
    if token:
        data["api_token"] = token
    if "ai" in source and isinstance(source["ai"], dict):
        data["ai"] = source["ai"]
    # Carry the host's providers block (incl. real API keys) into the dev
    # container's global.json via docker cp, so the container runs on the
    # operator's own keys. The Go backend no longer hardcodes any default key;
    # with providers already present, ensureDefaultProviders() is a no-op.
    if isinstance(source.get("providers"), dict):
        data["providers"] = source["providers"]
    return data


def load_proxy_json():
    for path in (PROXY_JSON_PATH, LEGACY_PROXY_JSON_PATH):
        try:
            with open(path, "r", encoding="utf-8") as f:
                data = json.load(f)
            return data if isinstance(data, dict) else {}
        except Exception:
            continue
    return {}


def build_runtime_proxy_json(shared_host="host.docker.internal"):
    source = load_proxy_json()
    profiles = source.get("ssh_proxies", [])
    if not isinstance(profiles, list) or not profiles:
        return {}

    runtime_profiles = []
    existing_names = set()
    for item in profiles:
        if not isinstance(item, dict):
            continue
        copied = dict(item)
        runtime_profiles.append(copied)
        name = str(copied.get("name", "") or "").strip()
        if name:
            existing_names.add(name)

    for item in profiles:
        if not isinstance(item, dict):
            continue
        name = str(item.get("name", "") or "").strip()
        local_port = item.get("local_port")
        if not name or not local_port:
            continue
        kind = str(item.get("kind", "") or "").strip()
        source_mode = str((item.get("source") or {}).get("mode", "") or "").strip()
        if kind in ("shared", "shared_only") or source_mode in (
            "shared",
            "shared_only",
        ):
            continue
        shared_name = f"{name}-shared"
        if shared_name in existing_names:
            continue
        scheme = str(item.get("scheme", "") or "socks5").strip() or "socks5"
        runtime_profiles.append(
            {
                "name": shared_name,
                "kind": "shared_only",
                "scheme": scheme,
                "local_host": shared_host,
                "local_port": local_port,
                "proxy_url": f"{scheme}://{shared_host}:{local_port}",
                "source": {
                    "mode": "shared",
                    "from": name,
                },
            }
        )
        existing_names.add(shared_name)

    if not runtime_profiles:
        return {}
    return {"ssh_proxies": runtime_profiles}


def build_dev_runtime_home(container_name, home_dir=""):
    if home_dir:
        home_dir = os.path.abspath(os.path.expanduser(home_dir))
    else:
        os.makedirs(CICY_DOCKER_HOMES_DIR, exist_ok=True)
        safe_name = (
            "".join(
                ch if ch.isalnum() or ch in ("-", "_") else "-"
                for ch in str(container_name or "").strip()
            ).strip("-")
            or "cicy-code-dev"
        )
        home_dir = os.path.join(CICY_DOCKER_HOMES_DIR, safe_name)
    os.makedirs(home_dir, exist_ok=True)
    for source_name in (".tmux.conf", ".cicy_tmux.conf"):
        source_path = os.path.join(API_DIR, "mgr", source_name)
        target_path = os.path.join(home_dir, source_name)
        if os.path.isfile(source_path):
            shutil.copy2(source_path, target_path)
    bashrc_path = os.path.join(home_dir, ".bashrc")
    bashrc_line = '[ -f "$HOME/.cicy_tmux.conf" ] && source "$HOME/.cicy_tmux.conf"'
    bashrc = ""
    if os.path.isfile(bashrc_path):
        try:
            with open(bashrc_path, "r", encoding="utf-8") as f:
                bashrc = f.read()
        except Exception:
            bashrc = ""
    if bashrc_line not in bashrc:
        with open(bashrc_path, "a", encoding="utf-8") as f:
            if bashrc and not bashrc.endswith("\n"):
                f.write("\n")
            f.write(bashrc_line + "\n")
    runtime_root_dir = os.path.join(home_dir, "cicy-ai")
    os.makedirs(runtime_root_dir, exist_ok=True)
    global_json_path = os.path.join(runtime_root_dir, "global.json")
    with open(global_json_path, "w", encoding="utf-8") as f:
        json.dump(build_minimal_runtime_global_json(), f, ensure_ascii=False, indent=2)
        f.write("\n")
    os.chmod(global_json_path, 0o644)
    proxy_json_path = os.path.join(runtime_root_dir, "proxy.json")
    runtime_proxy_json = build_runtime_proxy_json()
    if runtime_proxy_json:
        with open(proxy_json_path, "w", encoding="utf-8") as f:
            json.dump(runtime_proxy_json, f, ensure_ascii=False, indent=2)
            f.write("\n")
        os.chmod(proxy_json_path, 0o644)
    else:
        proxy_json_path = ""
    return home_dir, global_json_path, proxy_json_path


def ensure_docker_home_writable(home_dir, runtime_image):
    os.makedirs(home_dir, exist_ok=True)
    for root, dirs, files in os.walk(home_dir):
        for name in dirs:
            try:
                os.chmod(os.path.join(root, name), 0o777)
            except OSError:
                pass
        for name in files:
            try:
                os.chmod(os.path.join(root, name), 0o666)
            except OSError:
                pass
        try:
            os.chmod(root, 0o777)
        except OSError:
            pass
    subprocess.run(
        [
            "docker",
            "run",
            "--rm",
            "--user",
            "root",
            "--entrypoint",
            "sh",
            "-v",
            f"{os.path.abspath(home_dir)}:/target",
            runtime_image,
            "-lc",
            "chmod -R a+rwX /target",
        ],
        cwd=ROOT_DIR,
        capture_output=True,
    )


def seed_runtime_home_from_image(image_ref, home_dir):
    openclaw_dir = os.path.join(home_dir, ".openclaw")
    plugin_dir = os.path.join(openclaw_dir, "extensions", "openclaw-weixin")
    if os.path.isdir(plugin_dir):
        return

    container_id = ""
    temp_dir = tempfile.mkdtemp(prefix="cicy-openclaw-seed-")
    try:
        result = subprocess.run(
            ["docker", "create", image_ref],
            capture_output=True,
            text=True,
            cwd=ROOT_DIR,
        )
        if result.returncode != 0 or not result.stdout.strip():
            err = (result.stderr or result.stdout or "").strip()
            print(f"[dev] failed to create seed container for {image_ref}: {err}")
            return
        container_id = result.stdout.strip()
        copy_result = subprocess.run(
            ["docker", "cp", f"{container_id}:/home/cicy/.openclaw", temp_dir],
            capture_output=True,
            text=True,
            cwd=ROOT_DIR,
        )
        if copy_result.returncode != 0:
            err = (copy_result.stderr or copy_result.stdout or "").strip()
            print(f"[dev] failed to seed runtime home from image: {err}")
            return

        seeded_dir = os.path.join(temp_dir, ".openclaw")
        if not os.path.isdir(seeded_dir):
            print("[dev] seed image missing /home/cicy/.openclaw")
            return

        if os.path.exists(openclaw_dir):
            shutil.rmtree(openclaw_dir)
        shutil.copytree(seeded_dir, openclaw_dir)
        print(f"[dev] Seeded runtime home with image OpenClaw assets")
    finally:
        if container_id:
            subprocess.run(
                ["docker", "rm", "-f", container_id], capture_output=True, cwd=ROOT_DIR
            )
        shutil.rmtree(temp_dir, ignore_errors=True)


def add_optional_file_mount(
    volume_args, host_path, container_path, label, read_only=True
):
    resolved = os.path.abspath(os.path.expanduser(host_path))
    if not os.path.isfile(resolved):
        print(f"[dev] Skip mount missing {label}: {resolved}")
        return
    suffix = ":ro" if read_only else ""
    volume_args.extend(["-v", f"{resolved}:{container_path}{suffix}"])
    mode_label = "ro" if read_only else "rw"
    print(f"[dev] Mount host {label} ({mode_label}): {resolved} -> {container_path}")


def read_api_token_from_file(path):
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
        return str(data.get("api_token", "") or "").strip()
    except Exception:
        return ""


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
    print(f"[dev] Open URL: {open_url}")
    if service_url and service_url.rstrip("/") != base_url.rstrip("/"):
        print(f"[dev] Service URL: {service_url.rstrip('/')}/?token={token}")


def detect_public_ip():
    # Bypass any HTTP(S)_PROXY env (e.g. 家宽 proxy) so we get this host's real
    # public IP rather than the proxy's egress IP.
    for cmd in (
        ["curl", "-fsS", "--max-time", "5", "--noproxy", "*", "ifconfig.me"],
        ["curl", "-fsS", "--max-time", "5", "--noproxy", "*", "https://api.ipify.org"],
    ):
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=8)
            if result.returncode == 0 and result.stdout.strip():
                return result.stdout.strip()
        except Exception:
            pass
    return ""


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
        return image_ref[colon + 1 :]
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


def prefer_local_base_image(image_ref):
    value = str(image_ref or "").strip()
    if value.startswith("ghcr.io/cicy-ai/cicy-code-base:"):
        return f"cicy-code-base:{get_image_tag(value) or local_default_base_tag()}"
    return value


def local_default_base_image():
    data = load_global_json()
    images = data.get("images", {}) if isinstance(data, dict) else {}
    if isinstance(images, dict):
        explicit = prefer_local_base_image(images.get("base", ""))
        if explicit:
            return explicit
        repo = str(images.get("base_repository", "") or "").strip()
        tag = str(images.get("base_tag", "") or "").strip()
        if repo and tag:
            if repo == "ghcr.io/cicy-ai/cicy-code-base":
                return f"cicy-code-base:{tag}"
            return f"{repo}:{tag}"
    cluster = data.get("cicy-cluster", {}) if isinstance(data, dict) else {}
    if isinstance(cluster, dict):
        explicit = prefer_local_base_image(cluster.get("base_image", ""))
        if explicit:
            return explicit
    return f"cicy-code-base:{local_default_base_tag()}"


def ensure_local_base_image_available():
    base_image = os.environ.get("BASE_IMAGE", "").strip() or local_default_base_image()
    if not base_image.startswith("cicy-code-base:"):
        return
    inspect = subprocess.run(
        ["docker", "image", "inspect", base_image], capture_output=True, text=True
    )
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
    auth = ((data.get("auths") or {}).get("https://index.docker.io/v1/") or {}).get(
        "auth", ""
    )
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
    runtime_repository = (
        strip_image_tag(runtime_image)
        or get_images_config().get("runtime_repository", "")
        or get_docker_image_repository()
    )
    runtime_tag = get_image_tag(runtime_image) or get_images_config().get(
        "runtime_tag", ""
    )
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
        print(
            f"[dev] set DOCKER_IMAGE_REPOSITORY or configure Docker Hub login in ~/.docker/config.json"
        )
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
    # Try lsof first (macOS + Linux with lsof installed)
    try:
        result = subprocess.run(
            ["lsof", "-ti", f"TCP:{port}", "-sTCP:LISTEN"],
            capture_output=True,
            text=True,
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip().split("\n")[0]
    except FileNotFoundError:
        pass
    except Exception:
        return None
    # Fallback: ss (Linux without lsof)
    try:
        import re
        result = subprocess.run(
            ["ss", "-tlnp", f"sport = :{port}"],
            capture_output=True,
            text=True,
        )
        if result.returncode == 0:
            for line in result.stdout.splitlines():
                m = re.search(r"pid=(\d+)", line)
                if m:
                    return m.group(1)
    except (FileNotFoundError, Exception):
        pass
    return None


def is_pid_alive(pid):
    try:
        os.kill(int(pid), 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except Exception:
        return False


def wait_for_process_exit(pid, timeout=6.0, interval=0.2):
    start = time.time()
    while time.time() - start < timeout:
        if not is_pid_alive(pid):
            return True
        time.sleep(interval)
    return not is_pid_alive(pid)


def kill_process(pid):
    pid = str(pid or "").strip()
    if not pid:
        return True
    try:
        os.kill(int(pid), signal.SIGTERM)
    except ProcessLookupError:
        return True
    except Exception:
        return False
    if wait_for_process_exit(pid, timeout=6.0, interval=0.2):
        return True
    try:
        os.kill(int(pid), signal.SIGKILL)
    except ProcessLookupError:
        return True
    except Exception:
        return False
    return wait_for_process_exit(pid, timeout=2.0, interval=0.1)


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
                "gcloud",
                "run",
                "services",
                "describe",
                service,
                "--project",
                project,
                "--region",
                region,
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
                "gcloud",
                "run",
                "services",
                "list",
                "--project",
                project,
                "--region",
                region,
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
        service_account = (
            spec.get("template", {}).get("spec", {}).get("serviceAccountName", "")
        )
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


def _r2_upload_docker_image(image_ref, version):
    """Save the docker image as a gzip tar and upload it to Cloudflare R2.

    R2 key: images/cicy-code-latest.tar.gz — a dev-only "whatever was built
    last" snapshot, SEPARATE from the canonical docker/ base image. The base
    runtime image on docker/cicy-code-latest.tar.gz (managed occasionally by
    npm/publish-docker.sh, pulled by load.sh / cicy-desktop / 团队助手 charter)
    is a slowly-changing base env and is NOT re-uploaded on every version bump —
    the per-version binary is installed via `npx cicy-code` at container start.
    So --dockerBuild must NOT clobber docker/; it writes its own images/ key.
    Upload goes through the `cicy-r2` CLI (wraps `wrangler r2 object put`,
    reads ~/cicy-ai/db/r2.json).

    wrangler's single-PUT cap is ~300 MiB. The current image gzips to ~240 MB,
    so it fits; the size guard below aborts early with guidance if a future
    image exceeds that (R2 multipart would then need S3 credentials added to
    r2.json — the Bearer token alone can't do S3 multipart).
    """
    import shutil
    if not shutil.which("cicy-r2"):
        print("[dev] cicy-r2 not on PATH — skipping docker image upload")
        return False
    try:
        r2conf = json.load(open(os.path.expanduser("~/cicy-ai/db/r2.json")))
        public_url = str(r2conf.get("public_url", "https://r2.deepfetch.de5.net")).rstrip("/")
    except Exception as e:
        print(f"[dev] r2.json config error: {e} — skipping docker image upload")
        return False

    key = "images/cicy-code-latest.tar.gz"  # dev snapshot; NOT the canonical docker/ base image
    WRANGLER_PUT_LIMIT = 300 * 1024 * 1024  # wrangler r2 object put single-PUT cap

    tmp = os.path.join(tempfile.gettempdir(), f"cicy-code-{version}.tar.gz")
    try:
        print(f"[dev] Saving docker image {image_ref} → {tmp}")
        # Use shell pipeline: docker save | gzip > file.  Simpler and avoids
        # Python gzip-stream buffering issues that can corrupt the output.
        ret = subprocess.run(
            f'docker save {image_ref} | gzip -6 > {tmp}',
            shell=True, check=False,
        )
        if ret.returncode != 0:
            print(f"[dev] docker save failed (exit {ret.returncode})")
            return False

        size = os.path.getsize(tmp)
        print(f"[dev] Saved {size // 1024 // 1024}MB → uploading to R2 {key}")
        if size > WRANGLER_PUT_LIMIT:
            print(f"[dev] image is {size // 1024 // 1024}MB > 300MB wrangler single-PUT limit;")
            print("[dev] R2 multipart needs S3 credentials in ~/cicy-ai/db/r2.json — aborting upload")
            return False

        # Upload via cicy-r2 (wrangler r2 object put), 3 retries on transient failure.
        last_err = ""
        for attempt in range(3):
            r = subprocess.run(["cicy-r2", "put", key, tmp], capture_output=True, text=True)
            if r.returncode == 0:
                r2_url = f"{public_url}/{key}"
                print(f"[dev] R2 upload done → {r2_url}")
                return r2_url
            lines = (r.stderr or r.stdout or "").strip().splitlines()
            last_err = lines[-1] if lines else ""
            if attempt < 2:
                print(f"[dev] R2 upload attempt {attempt+1} failed: {last_err}, retrying...")
                time.sleep(3)
        print(f"[dev] R2 upload failed: {last_err}")
        return False
    except Exception as e:
        print(f"[dev] R2 upload error: {e}")
        return False
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)
            print(f"[dev] Cleaned up {tmp}")


def run_docker_build(version_override=""):
    info = run_version_sync(version_override)
    version = (
        str(info.get("version", "")).strip()
        or str(version_override or "").strip()
        or get_binary_version()
    )
    repository = get_docker_image_repository()
    if not repository:
        print("[dev] missing Docker Hub target repository")
        print(
            f"[dev] set DOCKER_IMAGE_REPOSITORY or configure Docker Hub login in ~/.docker/config.json"
        )
        sys.exit(1)
    target_image = f"{repository}:{version}"
    latest_image = f"{repository}:latest"

    print(f"[dev] Docker build version: {version}")
    print(f"[dev] Build local image tag: cicy-code:{version}")
    print(f"[dev] Push target image: {target_image}")
    print(f"[dev] Push latest image: {latest_image}")

    build_env = os.environ.copy()
    build_env["CDN"] = "1"
    build_env["SKIP_NPM"] = "0"
    build_env["SKIP_TTYD_ASSET"] = "0"
    run_checked(["./build.sh", "assets"], cwd=ROOT_DIR, env=build_env)
    run_checked(
        ["python3", "./scripts/r2-upload.py", "app"], cwd=ROOT_DIR, env=build_env
    )
    run_checked(
        ["python3", "./scripts/r2-upload.py", "ttyd"], cwd=ROOT_DIR, env=build_env
    )
    build_env["SKIP_NPM"] = "1"
    build_env["SKIP_TTYD_ASSET"] = "1"
    run_checked(["./build.sh", "docker", version], cwd=ROOT_DIR, env=build_env)
    run_checked(["docker", "tag", f"cicy-code:{version}", target_image], cwd=ROOT_DIR)
    run_checked(["docker", "tag", f"cicy-code:{version}", latest_image], cwd=ROOT_DIR)
    run_checked(["docker", "push", target_image], cwd=ROOT_DIR)
    run_checked(["docker", "push", latest_image], cwd=ROOT_DIR)

    # Save the tagged image (cicybot/cicy-code:X.Y.Z) to R2 as a global fallback
    # tarball.  Must save from target_image — NOT from cicy-code:version — so the
    # `docker load` on the client produces the right repo name and `docker run`
    # finds it without an extra `docker tag` step.
    _r2_upload_docker_image(target_image, version)

    write_docker_image_to_global_json(target_image, version, repository)
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime={target_image}")
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime_repository={repository}")
    print(f"[dev] Updated {GLOBAL_JSON_PATH} -> images.runtime_tag={version}")
    print(f"[dev] Next deploy: use {CICY_GLOBAL_JSON_PATH} images.runtime")
    sys.exit(0)


def _configured_public_url():
    """Public URL for in-app links, read from global.json ("public_url").

    The box-specific domain lives in config, never hardcoded in this launcher:
    different deployments (and re-provisioned spot boxes) carry different domains.
    Returns "" when unset so the caller can fall back to whatever is in the env.
    """
    try:
        with open(GLOBAL_JSON_PATH) as f:
            return str(json.load(f).get("public_url", "") or "").strip()
    except Exception:
        return ""


def _public_url_with_port(url, port):
    """Rewrite CICY_PUBLIC_URL to the externally-published host port.

    The dev container listens on 8008 internally but is published on --port
    (default 8026). CICY_PUBLIC_URL gets baked into in-app links (e.g. the
    incident-email ack URL); keeping the internal :8008 makes those links 404
    from outside. Only rewrites when the URL has an explicit port — a bare
    domain (production reverse-proxy) is left untouched.
    """
    try:
        from urllib.parse import urlsplit, urlunsplit

        p = urlsplit(url)
        if not p.hostname or p.port is None:
            return url
        return urlunsplit(
            (p.scheme or "http", f"{p.hostname}:{port}", p.path, p.query, p.fragment)
        )
    except Exception:
        return url


def run_docker(
    ports,
    container_name="cicy-code-dev",
    agents="",
    mount_projects=True,
    projects_dir="",
    mount_home=False,
    home_dir="",
):
    run_version_sync()

    # Do not bind-mount any host directories into the dev container.
    mount_projects = False
    mount_home = False

    runtime_image = get_current_runtime_image()
    if not runtime_image:
        print(
            f"[dev] ERROR: No runtime image configured. Set images.runtime in {CICY_GLOBAL_JSON_PATH}"
        )
        sys.exit(1)
    print(f"[dev] Runtime image: {runtime_image}")

    print("[dev] Building and running Docker...")
    os.environ.pop("SKIP_NPM", None)
    ensure_local_base_image_available()
    # Extract tag from runtime_image for build
    build_tag = runtime_image.rsplit(":", 1)[-1] if ":" in runtime_image else "dev"
    result = subprocess.run(["./build.sh", "docker", build_tag], cwd=ROOT_DIR)
    if result.returncode != 0:
        print("[dev] docker build failed")
        sys.exit(1)

    # Tag local build as the configured runtime image
    subprocess.run(
        ["docker", "tag", f"cicy-code:{build_tag}", runtime_image], capture_output=True
    )
    subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)
    host_home_path = os.path.abspath(os.path.expanduser(home_dir)) if home_dir else ""
    dev_home_dir, dev_global_json_path, dev_proxy_json_path = build_dev_runtime_home(
        container_name,
        host_home_path if mount_home and host_home_path else "",
    )
    if mount_home:
        ensure_docker_home_writable(dev_home_dir, runtime_image)

    # Pull if not available locally
    result = subprocess.run(
        ["docker", "image", "inspect", runtime_image], capture_output=True
    )
    if result.returncode != 0:
        print(f"[dev] Pulling {runtime_image}...")
        subprocess.run(["docker", "pull", runtime_image])

    env_vars = []
    passthrough_env_keys = [
        "CICY_TEAM_TOKEN",
        "CICY_TEAMCENTER_URL",
        "CICY_TEAMCENTER_BOOTSTRAP_PATH",
        "CICY_MASTER_URL",
        "CICY_MASTER_TOKEN",
        "CICY_PUBLIC_URL",
        "CICY_CLOUDFLARED_TOKEN",
        "CF_TUNNEL_TOKEN",
        "CLOUDFLARED_TOKEN",
        "CICY_INSTANCE_KEY",
        "CICY_INSTANCE_LABEL",
    ]
    for key in passthrough_env_keys:
        value = os.environ.get(key, "").strip()
        if not value:
            continue
        if key == "CICY_PUBLIC_URL":
            value = _public_url_with_port(value, ports)
        env_vars.extend(["-e", f"{key}={value}"])
    trial_ttl_seconds = int(
        (os.environ.get("CLOUD_TRIAL_RUNTIME_TTL_SECONDS", "3600") or "3600").strip()
        or "3600"
    )
    trial_expires_at = str(int(time.time()) + trial_ttl_seconds)
    env_vars.extend(["-e", f"CLOUD_TRIAL_RUNTIME_EXPIRES_AT={trial_expires_at}"])
    env_vars.extend(["-e", "CICY_IS_PRO=true"])
    local_token = get_local_api_token()
    if local_token:
        env_vars.extend(["-e", f"CICY_API_TOKEN={local_token}"])
    volume_args = []
    if mount_home:
        os.makedirs(dev_home_dir, exist_ok=True)
        volume_args.extend(["-v", f"{dev_home_dir}:{DOCKER_HOME_DIR}"])
        print(f"[dev] Mount runtime home: {dev_home_dir} -> {DOCKER_HOME_DIR}")
    if mount_projects:
        host_projects_dir = os.path.abspath(
            os.path.expanduser(projects_dir or HOST_PROJECTS_DIR)
        )
        os.makedirs(host_projects_dir, exist_ok=True)
        volume_args.extend(["-v", f"{host_projects_dir}:{DOCKER_PROJECTS_DIR}"])
        print(
            f"[dev] Mount host projects: {host_projects_dir} -> {DOCKER_PROJECTS_DIR}"
        )
    # When no --agents is given, omit the flag entirely so the server preinstalls
    # the official role roster (w-1001 项目经理 + team), exactly like a real fresh
    # install. Passing --agents forces the legacy per-type layout (dev override).
    # --public / --agents 已废弃:容器模式自动绑 0.0.0.0,且默认安装官方角色名册。
    cicy_args = []
    run_cmd = (
        [
            "docker",
            "run",
            "-d",
            "--name",
            container_name,
            "--add-host",
            "host.docker.internal:host-gateway",
        ]
        + env_vars
        + volume_args
        + [
            "-p",
            f"{ports}:8008",
            runtime_image,
        ]
        + cicy_args
    )
    print(f"[dev] docker run: {' '.join(run_cmd)}")
    docker_run_started_at = time.time()
    run_result = subprocess.run(run_cmd, cwd=ROOT_DIR)
    if run_result.returncode != 0:
        print("[dev] docker run failed")
        sys.exit(run_result.returncode or 1)
    subprocess.run(
        [
            "docker",
            "cp",
            dev_global_json_path,
            f"{container_name}:{CICY_GLOBAL_JSON_PATH}",
        ],
        capture_output=True,
        cwd=ROOT_DIR,
    )
    if dev_proxy_json_path:
        subprocess.run(
            [
                "docker",
                "cp",
                dev_proxy_json_path,
                f"{container_name}:{CICY_PROXY_JSON_PATH}",
            ],
            capture_output=True,
            cwd=ROOT_DIR,
        )
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
        capture_output=True,
        text=True,
    )
    if version_result.returncode == 0 and version_result.stdout.strip():
        print(f"[dev] OpenClaw: {version_result.stdout.strip()}")
    else:
        version_err = (version_result.stderr or "").strip()
        if version_err:
            print(f"[dev] OpenClaw version check failed: {version_err}")

    token = read_api_token_from_file(dev_global_json_path)
    if token:
        # Verify token via /api/auth/verify
        verify_url = f"http://localhost:{ports}/api/auth/verify"
        try:
            req = urllib.request.Request(
                verify_url, headers={"Authorization": f"Bearer {token}"}
            )
            with urllib.request.urlopen(req, timeout=10) as resp:
                verify_data = json.loads(resp.read())
                if verify_data.get("valid"):
                    print(f"[dev] Auth verified: {verify_data}")
                else:
                    print(f"[dev] Auth verify failed: {verify_data}")
        except Exception as e:
            print(f"[dev] Auth verify error: {e}")

        public_ip = detect_public_ip()
        public_base_url = f"http://{public_ip}:{ports}" if public_ip else ""
        local_base_url = f"http://localhost:{ports}"
        if public_base_url:
            print_access_urls(public_base_url, token, local_base_url)
        else:
            print_access_urls(local_base_url, token)
        print(
            f"[dev] Token available in {time.time() - docker_run_started_at:.1f}s (since docker run)"
        )
        print(f"[dev] Runtime home: {dev_home_dir}")
        print("[dev] Testing agents...")
        test_agents(container_name, token, ports)

    sys.exit(0)


def test_agents(container_name, token, port):
    base_url = f"http://localhost:{port}"
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
                print(f"  Failed to get panes after 3 attempts: {e}")
                return

    if not panes:
        print("  No panes found")
        return

    print(f"\n[dev] Found {len(panes)} agents:")
    for pane in panes:
        title = pane.get("title", "")
        agent_type = pane.get("agent_type", "")
        ttyd_port = pane.get("ttyd_port", 0)
        active = pane.get("active", 0)
        status = "OK" if active == 1 else "DOWN"
        print(f"  {status} {title} (type: {agent_type}, port: {ttyd_port})")
        if ttyd_port > 0:
            try:
                req = urllib.request.Request(f"http://localhost:{ttyd_port}")
                urllib.request.urlopen(req, timeout=5)
                print(f"     ttyd port {ttyd_port} is accessible")
            except urllib.error.URLError:
                print(f"     ttyd port {ttyd_port} not accessible")
            except Exception as e:
                print(f"     ttyd port {ttyd_port} error: {e}")

    print("\n[dev] Agent test completed.")


def run_cloudrun():
    env = get_cloudrun_env()
    validate_cloudrun_env(env)
    env["PREBUILD"] = "0"
    env["BUILD_IMAGE"] = "0"
    started_at = time.time()
    print(
        f"[dev] Cloud Run trigger started at {time.strftime('%Y-%m-%d %H:%M:%S', time.gmtime(started_at))} UTC"
    )
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
        service_url = (
            get_cloudrun_service_url(project, region, service)
            if project and region and service
            else ""
        )
        public_url = env.get("CICY_PUBLIC_URL", "") or service_url
        if service_url:
            probe_url = f"{service_url.rstrip('/')}/api/health"
            print(f"[dev] Waiting for probe: {probe_url}")
            try:
                probe_delay = wait_for_probe(probe_url, timeout=180, interval=2)
                total_elapsed = time.time() - started_at
                print(
                    f"[dev] Cloud Run probe ready in {total_elapsed:.1f}s (since trigger)"
                )
                print(f"[dev] Probe check time after deploy: {probe_delay:.1f}s")
            except Exception as e:
                print(f"[dev] Probe failed: {e}")
                print(f"[dev] Elapsed since trigger: {time.time() - started_at:.1f}s")
        print_access_urls(public_url, token, service_url)
    sys.exit(result.returncode)


def npm_ensure_deps(pkg_dir: str) -> bool:
    """Run `npm install` in pkg_dir when node_modules is missing OR when
    package.json has been modified after the last install (newer mtime than
    node_modules/.package-lock.json). Catches the "dep added but never
    installed" case that bites every fresh clone / git pull.
    Returns True on success or no-op; False if npm install failed.
    """
    pkg_json = os.path.join(pkg_dir, "package.json")
    if not os.path.isfile(pkg_json):
        return True
    node_modules = os.path.join(pkg_dir, "node_modules")
    marker = os.path.join(node_modules, ".package-lock.json")
    reason = ""
    if not os.path.isdir(node_modules):
        reason = "node_modules missing"
    elif not os.path.isfile(marker):
        reason = "no .package-lock.json marker in node_modules"
    else:
        try:
            if os.path.getmtime(pkg_json) > os.path.getmtime(marker):
                reason = "package.json newer than installed node_modules"
        except OSError:
            pass
    if not reason:
        return True
    rel = os.path.relpath(pkg_dir, ROOT_DIR)
    print(f"[dev] npm install in {rel} ({reason})...")
    return subprocess.run(["npm", "install"], cwd=pkg_dir).returncode == 0


def run_ttyd_assets():
    os.environ["GOPROXY"] = "https://goproxy.cn,direct"
    print("[dev] Rebuilding ttyd embedded assets via `make asset`...")
    if not npm_ensure_deps(os.path.join(API_DIR, "js")):
        print("[dev] npm install failed in api/js; aborting")
        sys.exit(1)
    run_checked(["make", "asset"], cwd=API_DIR)
    print("[dev] ttyd assets rebuilt.")
    sys.exit(0)


def rebuild_ttyd_assets_for_local_dev():
    os.environ["GOPROXY"] = "https://goproxy.cn,direct"
    print("[dev] Refreshing ttyd embedded assets for local dev...")
    if not npm_ensure_deps(os.path.join(API_DIR, "js")):
        print("[dev] npm install failed in api/js; aborting")
        sys.exit(1)
    run_checked(["make", "asset"], cwd=API_DIR)
    print("[dev] ttyd embedded assets refreshed.")


APP_DEV_PORT = 8022


def build_app_dist():
    app_dir = os.path.join(ROOT_DIR, "app")
    if not npm_ensure_deps(app_dir):
        return False
    print("[dev] building app/dist (npm run build)...")
    return subprocess.run(["npm", "run", "build"], cwd=app_dir).returncode == 0


def ensure_app_dev_server():
    """Start the Vite dev server (app/, port 8022) in the background if it is not already up."""
    existing = get_pid_on_port(APP_DEV_PORT)
    if existing:
        print(f"[dev] app dev server already running on :{APP_DEV_PORT} (pid={existing})")
        return
    app_dir = os.path.join(ROOT_DIR, "app")
    if not npm_ensure_deps(app_dir):
        print("[dev] npm install failed; skipping app dev server")
        return
    logs_dir = os.path.expanduser("~/logs/.dev-logs")
    os.makedirs(logs_dir, exist_ok=True)
    log_path = os.path.join(logs_dir, "app-dev.log")
    Path(log_path).touch(exist_ok=True)
    with open(log_path, "ab", buffering=0) as log_file:
        process = subprocess.Popen(
            ["npm", "run", "dev"],
            cwd=app_dir,
            stdout=log_file,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
    print(f"[dev] app dev server (vite :{APP_DEV_PORT}) started in background (pid={process.pid})")
    print(f"[dev] App dev log: {log_path}")


def start_local_dev_detached(cicy_bin, extra=None, reply_mirror=False):
    logs_dir = os.path.expanduser("~/logs/.dev-logs")
    os.makedirs(logs_dir, exist_ok=True)
    log_path = os.path.join(logs_dir, "cicy-code.log")
    Path(log_path).touch(exist_ok=True)

    run_env = os.environ.copy()
    run_env["PATH"] = f"{API_DIR}:{run_env.get('PATH', '')}"
    # reply 镜像收集默认关闭：它把每次 AI gateway 请求/响应完整快照写到
    # ~/cicy-ai/workers/<agent>/.cicy/history/reply_mirror/<turn>_<req>_<ts>.json，
    # 约 6MB/轮、无轮转，会无界占满磁盘（曾撑到 48G）。
    # HARD override（不是 setdefault）：否则一次诊断时 `export CICY_GATEWAY_REPLY_MIRROR=1`
    # 忘了 unset，就会被 os.environ.copy() 继承、悄悄泄漏进之后每个 dev server。
    # 要开镜像走显式的 `dev.py --reply-mirror`，env 不再是开关。
    run_env["CICY_GATEWAY_REPLY_MIRROR"] = "1" if reply_mirror else "0"

    with open(log_path, "ab", buffering=0) as log_file:
        process = subprocess.Popen(
            [cicy_bin, "--dev", "--lab", *(extra or [])],
            env=run_env,
            cwd=ROOT_DIR,
            stdout=log_file,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )

    print(f"[dev] cicy-code started in background (pid={process.pid})")
    print(f"[dev] Log file: {log_path}")
    print(f"[dev] Tailing logs. Press Ctrl+C to stop tailing; cicy-code keeps running.")
    tail_proc = subprocess.Popen(["tail", "-f", log_path], cwd=ROOT_DIR)
    try:
        tail_proc.wait()
    except KeyboardInterrupt:
        print("\n[dev] Stopped tail. cicy-code is still running.")
        tail_proc.terminate()
        try:
            tail_proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            tail_proc.kill()
            tail_proc.wait()


def main():
    parser = argparse.ArgumentParser(
        description=(
            "cicy-code development entrypoint.\n\n"
            "Default behavior with no flags:\n"
            "  Build local dev binary, start cicy-code in background, and tail its log.\n"
            "  Press Ctrl+C to stop tailing; cicy-code keeps running."
        ),
        epilog=(
            "Examples:\n"
            "  python3 dev.py\n"
            "      Start local dev server in background and tail logs.\n\n"
            "  python3 dev.py --docker --agents codex --port 8026\n"
            "      Build and run the configured runtime image in Docker.\n\n"
            "  python3 dev.py --dockerBuild\n"
            f"      Build runtime image, push it, and update {CICY_GLOBAL_JSON_PATH}.\n\n"
            "  python3 dev.py --dockerBuild --dockerBuildVersion 1.2.3\n"
            "      Build and push a specific runtime image tag.\n\n"
            "  python3 dev.py --dockerVersion\n"
            "      Show current package version and configured runtime image.\n\n"
            "  python3 dev.py --dockerSetVersion 1.2.3\n"
            "      Point images.runtime at an existing image tag.\n\n"
            "  python3 dev.py --bumpVersion 1.2.3\n"
            "      Sync all version targets to the given version.\n\n"
            "  python3 dev.py --cloudRun\n"
            "      Deploy the configured service to Cloud Run.\n\n"
            "  python3 dev.py --cloudRunList\n"
            "      List Cloud Run services in the configured project and region.\n\n"
            "  python3 dev.py --ttydAssets\n"
            "      Rebuild embedded ttyd static assets only; do not start cicy-code.\n"
        ),
        formatter_class=argparse.RawTextHelpFormatter,
    )

    local_group = parser.add_argument_group("local dev")
    local_group.add_argument(
        "--hot",
        action="store_true",
        help="Run the vite dev server on :8022 and proxy the UI to it (HMR). Overrides --preview.",
    )
    local_group.add_argument(
        "--preview",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="(default) build app/dist and serve it from disk via cicy-code --dev --preview; use --no-preview for the vite :8022 dev server.",
    )
    local_group.add_argument(
        "--quick",
        action="store_true",
        help="Quick restart: build binary, kill existing on :8008, start cicy-code. Skips ttyd/app/version-sync.",
    )
    local_group.add_argument(
        "--ttydAssets",
        "--ttyd-assets",
        dest="ttydAssets",
        action="store_true",
        help="Rebuild embedded ttyd/goTTY static assets only; do not start cicy-code.",
    )
    local_group.add_argument(
        "--reply-mirror",
        dest="reply_mirror",
        action="store_true",
        help="Enable the AI gateway reply mirror (full request/response snapshots under "
        ".cicy/history/reply_mirror/). OFF by default and HARD-forced off otherwise, so a "
        "stray `export CICY_GATEWAY_REPLY_MIRROR=1` in your shell can no longer leak in. "
        "Writes ~6MB/turn with no rotation — only use for short diagnostics.",
    )

    docker_group = parser.add_argument_group("docker runtime")
    docker_group.add_argument(
        "--docker",
        action="store_true",
        help="Build local runtime image, start Docker container, wait for health check, and print access URLs.",
    )
    docker_group.add_argument(
        "--agents",
        default="",
        help="Comma-separated agents for --docker (legacy per-type layout). Default: empty → official role roster (w-1001 项目经理 + team).",
    )
    docker_group.add_argument(
        "--port",
        type=int,
        default=8026,
        help="Host port mapped to container port 8008 when using --docker. Default: 8026.",
    )
    docker_group.add_argument(
        "--name",
        default="cicy-code-dev",
        help="Docker container name used by --docker. Default: cicy-code-dev.",
    )
    docker_group.add_argument(
        "--mountProjects",
        "--mount-projects",
        dest="mountProjects",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="Mount a host projects directory into ~/projects inside the container. Default: enabled.",
    )
    docker_group.add_argument(
        "--projectsDir",
        "--projects-dir",
        dest="projectsDir",
        default=HOST_PROJECTS_DIR,
        help=f"Host projects directory used with --mountProjects. Default: {HOST_PROJECTS_DIR}.",
    )
    docker_group.add_argument(
        "--mountHome",
        "--mount-home",
        dest="mountHome",
        action=argparse.BooleanOptionalAction,
        default=True,
        help=f"Mount a host runtime home into {DOCKER_HOME_DIR} inside the container. Default: enabled.",
    )
    docker_group.add_argument(
        "--homeDir",
        "--home-dir",
        dest="homeDir",
        default="",
        help="Host runtime home directory used with --mountHome. Default: ~/docker-homes/<container-name>.",
    )

    image_group = parser.add_argument_group("image and version management")
    image_group.add_argument(
        "--dockerBuild",
        "--docker-build",
        "--cloudRunBuild",
        "--cloudrun-build",
        dest="dockerBuild",
        action="store_true",
        help=f"Build runtime image, push it to Docker Hub, and update {CICY_GLOBAL_JSON_PATH} images.runtime.",
    )
    image_group.add_argument(
        "--dockerBuildVersion",
        "--docker-build-version",
        dest="dockerBuildVersion",
        default="",
        help="Override the image tag used by --dockerBuild.",
    )
    image_group.add_argument(
        "--dockerVersion",
        "--docker-version",
        dest="dockerVersion",
        action="store_true",
        help="Show current package version and the configured runtime image tag.",
    )
    image_group.add_argument(
        "--dockerSetVersion",
        "--docker-set-version",
        dest="dockerSetVersion",
        default="",
        help=f"Update {CICY_GLOBAL_JSON_PATH} images.runtime to an existing image tag without building.",
    )
    image_group.add_argument(
        "--bumpVersion",
        "--bump-version",
        dest="bumpVersion",
        default="",
        help="Set the project version and sync all version targets to that value.",
    )

    cloudrun_group = parser.add_argument_group("cloud run")
    cloudrun_group.add_argument(
        "--cloudRun",
        "--cloudrun",
        dest="cloudRun",
        action="store_true",
        help="Deploy to Cloud Run using scripts/deploy-cloudrun.sh and wait for the health check.",
    )
    cloudrun_group.add_argument(
        "--cloudRunList",
        "--cloudrun-list",
        dest="cloudRunList",
        action="store_true",
        help="List Cloud Run services for the configured project and region.",
    )

    args = parser.parse_args()

    if args.docker:
        run_docker(
            args.port,
            args.name,
            args.agents,
            args.mountProjects,
            args.projectsDir,
            args.mountHome,
            args.homeDir,
        )
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

    # --quick: build binary, kill :8008, start. No ttyd/app/version-sync.
    if args.quick:
        platform = "darwin" if sys.platform == "darwin" else "linux"
        os.environ["SKIP_TTYD_ASSET"] = "1"
        os.environ["SKIP_NPM"] = "1"
        os.environ["PORT"] = str(PORT)
        os.environ["SQLITE_PATH"] = SQLITE_PATH
        os.environ["CICY_PUBLIC_URL"] = (
            _configured_public_url() or os.environ.get("CICY_PUBLIC_URL", "")
        )
        for key, value in get_ai_env_defaults().items():
            os.environ[key] = value
        result = subprocess.run(["./build.sh", "build", platform], cwd=ROOT_DIR)
        if result.returncode != 0:
            print("[dev] build failed")
            sys.exit(1)
        existing_pid = get_pid_on_port(PORT)
        if existing_pid:
            print(f"[dev] kill :{PORT} (pid={existing_pid})")
            kill_process(existing_pid)
        cicy_bin = os.path.join(API_DIR, "cicy-code")
        extra = ["--preview"] if args.preview else []
        if args.hot:
            extra = ["--hot"]
        if args.preview:
            os.environ["CICY_PREVIEW_DIST"] = os.path.join(ROOT_DIR, "app", "dist")
        start_local_dev_detached(cicy_bin, extra=extra or None, reply_mirror=args.reply_mirror)
        sys.exit(0)

    existing_pid = get_pid_on_port(PORT)
    if existing_pid:
        try:
            cmd = subprocess.run(
                ["ps", "-p", existing_pid, "-o", "command="],
                capture_output=True,
                text=True,
            ).stdout.strip()
            if "cicy-code" in cmd:
                print(
                    f"[dev] stop existing cicy process on :{PORT} (pid={existing_pid})"
                )
                if not kill_process(existing_pid):
                    print(
                        f"[dev] failed to stop existing cicy process pid={existing_pid}"
                    )
                    sys.exit(1)
                still_running_pid = get_pid_on_port(PORT)
                if still_running_pid:
                    print(
                        f"[dev] port {PORT} is still in use after stop attempt (pid={still_running_pid})"
                    )
                    sys.exit(1)
            else:
                print(f"[dev] port {PORT} is in use by non-cicy process: {cmd}")
                sys.exit(1)
        except Exception:
            pass

    os.environ["SKIP_TTYD_ASSET"] = os.environ.get("SKIP_TTYD_ASSET", "1")
    platform = "darwin" if sys.platform == "darwin" else "linux"
    os.environ["PORT"] = str(PORT)
    os.environ["SKIP_NPM"] = "1"
    os.environ["SQLITE_PATH"] = SQLITE_PATH
    os.environ.setdefault("CICY_PUBLIC_URL", "https://app-1001.cicy-ai.com")
    for key, value in get_ai_env_defaults().items():
        os.environ[key] = value
    if not args.ttydAssets:
        rebuild_ttyd_assets_for_local_dev()
    run_version_sync()

    result = subprocess.run(["./build.sh", "build", platform], cwd=ROOT_DIR)
    if result.returncode != 0:
        print("[dev] build failed, not starting")
        sys.exit(1)

    cicy_bin = os.path.join(API_DIR, "cicy-code")
    if args.hot:
        ensure_app_dev_server()   # starts `npm run dev` on :8022 if not already up
        start_local_dev_detached(cicy_bin, extra=["--hot"], reply_mirror=args.reply_mirror)
    elif args.preview:
        build_app_dist()          # `npm run build` -> app/dist, served from disk
        os.environ["CICY_PREVIEW_DIST"] = os.path.join(ROOT_DIR, "app", "dist")
        start_local_dev_detached(cicy_bin, extra=["--preview"], reply_mirror=args.reply_mirror)
    else:
        start_local_dev_detached(cicy_bin, reply_mirror=args.reply_mirror)   # serve the binary-embedded assets
    sys.exit(0)


if __name__ == "__main__":
    main()
