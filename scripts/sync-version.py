#!/usr/bin/env python3
import argparse
import json
import os
import re
import sys

ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
NPM_PACKAGE_PATH = os.path.join(ROOT_DIR, "npm", "package.json")
APP_PACKAGE_PATH = os.path.join(ROOT_DIR, "app", "package.json")
APP_PACKAGE_LOCK_PATH = os.path.join(ROOT_DIR, "app", "package-lock.json")
APP_CONFIG_PATH = os.path.join(ROOT_DIR, "app", "src", "config.ts")
MAIN_GO_PATH = os.path.join(ROOT_DIR, "api", "mgr", "main.go")
TMUX_CONF_PATH = os.path.join(ROOT_DIR, ".cicy_tmux.conf")


def read_text(path):
    with open(path, "r", encoding="utf-8") as f:
        return f.read()


def write_text(path, content):
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)


def read_json(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def write_json(path, data):
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")


def get_npm_version():
    data = read_json(NPM_PACKAGE_PATH)
    version = str(data.get("version", "")).strip()
    if not version:
        raise RuntimeError(f"missing version in {NPM_PACKAGE_PATH}")
    return version


def set_npm_version(version):
    data = read_json(NPM_PACKAGE_PATH)
    data["version"] = version
    write_json(NPM_PACKAGE_PATH, data)


def get_app_version():
    data = read_json(APP_PACKAGE_PATH)
    version = str(data.get("version", "")).strip()
    if not version:
        raise RuntimeError(f"missing version in {APP_PACKAGE_PATH}")
    return version


def set_app_version(version):
    data = read_json(APP_PACKAGE_PATH)
    data["version"] = version
    write_json(APP_PACKAGE_PATH, data)


def set_app_lock_version(version):
    data = read_json(APP_PACKAGE_LOCK_PATH)
    data["version"] = version
    packages = data.get("packages")
    if isinstance(packages, dict):
        root = packages.get("")
        if isinstance(root, dict):
            root["version"] = version
    write_json(APP_PACKAGE_LOCK_PATH, data)


def extract_one(pattern, text, path):
    match = re.search(pattern, text, re.MULTILINE)
    if not match:
        raise RuntimeError(f"failed to find version pattern in {path}")
    return match.group(1)


def replace_one(pattern, repl, text, path):
    updated, count = re.subn(pattern, repl, text, count=1, flags=re.MULTILINE)
    if count != 1:
        raise RuntimeError(f"failed to update version pattern in {path}")
    return updated


def get_versions():
    return {
        "npm_package": get_npm_version(),
        "app_package": get_app_version(),
        "app_config": extract_one(r"^const APP_VERSION = '([^']+)';$", read_text(APP_CONFIG_PATH), APP_CONFIG_PATH),
        "mgr_main_go": extract_one(r'^const version = "([^"]+)"$', read_text(MAIN_GO_PATH), MAIN_GO_PATH),
        "cicy_tmux_conf": extract_one(r'^export CICY_VERSION="([^"]+)"$', read_text(TMUX_CONF_PATH), TMUX_CONF_PATH),
    }


def sync_targets(version):
    set_npm_version(version)
    set_app_version(version)
    set_app_lock_version(version)

    app_config = read_text(APP_CONFIG_PATH)
    app_config = replace_one(r"^const APP_VERSION = '([^']+)';$", f"const APP_VERSION = '{version}';", app_config, APP_CONFIG_PATH)
    write_text(APP_CONFIG_PATH, app_config)

    main_go = read_text(MAIN_GO_PATH)
    main_go = replace_one(r'^const version = "([^"]+)"$', f'const version = "{version}"', main_go, MAIN_GO_PATH)
    write_text(MAIN_GO_PATH, main_go)

    tmux_conf = read_text(TMUX_CONF_PATH)
    if re.search(r'^export CICY_VERSION="([^"]+)"$', tmux_conf, re.MULTILINE):
        tmux_conf = replace_one(
            r'^export CICY_VERSION="([^"]+)"$',
            f'export CICY_VERSION="{version}"',
            tmux_conf,
            TMUX_CONF_PATH,
        )
    else:
        anchor = 'export CICY_TMUX=1\n'
        insert = f'export CICY_VERSION="{version}"\n'
        if anchor in tmux_conf:
            tmux_conf = tmux_conf.replace(anchor, anchor + insert, 1)
        else:
            tmux_conf = insert + tmux_conf
    write_text(TMUX_CONF_PATH, tmux_conf)

    return {
        "version": version,
        "files": get_versions(),
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--set", dest="set_version", default="", help="Set version in npm/package.json, then sync all version targets")
    parser.add_argument("--print-json", dest="print_json", action="store_true", help="Print current synced version info as JSON")
    parser.add_argument("--print-version", dest="print_version", action="store_true", help="Print current npm/package.json version only")
    args = parser.parse_args()

    if args.print_json:
        info = {
            "version": get_npm_version(),
            "files": get_versions(),
        }
        json.dump(info, sys.stdout, ensure_ascii=False, indent=2)
        sys.stdout.write("\n")
        return

    if args.print_version:
        sys.stdout.write(get_npm_version() + "\n")
        return

    version = str(args.set_version or "").strip() or get_npm_version()
    info = sync_targets(version)
    print(f"[sync-version] version={info['version']}")
    for key, value in info["files"].items():
        print(f"[sync-version] {key}={value}")


if __name__ == "__main__":
    main()
