#!/usr/bin/env python3
"""
Provision a Hong Kong (cn-hongkong) spot ECS instance with Docker + persistent data disk.

Usage:
  hk-spot-dev                          # provision spot + attach persistent disk + setup docker
  hk-spot-dev --destroy                # delete the instance (the data disk is always kept)
  hk-spot-dev --push-image             # rebuild the container image on the running box and push it
  hk-spot-dev --json                   # JSON output
"""

import json, os, subprocess, sys, time, urllib.request, urllib.error, base64, secrets, shlex as _shlex
from pathlib import Path

REGION = "cn-hongkong"
ZONE = "cn-hongkong-d"
INSTANCE_TYPE = "ecs.u1-c1m8.large"
IMAGE_ID = "ubuntu_24_04_x64_20G_alibase_20260506.vhd"
SPOT_STRATEGY = "SpotAsPriceGo"
SYSTEM_DISK_SIZE = 40
SYSTEM_DISK_CATEGORY = "cloud_essd"
DISK_SIZE = 100
DISK_CATEGORY = "cloud_essd"
DISK_NAME = "hk-spot-dev-data"
INSTANCE_NAME = "hk-spot-dev"
KEY_NAME = "cicy-dev-key"
SSH_KEY = os.path.expanduser("~/.ssh/id_ed25519")
GITHUB_KEY = os.path.expanduser("~/.ssh/id_ed25519_cicy_ai")
RSA_KEY = os.path.expanduser("~/.ssh/id_rsa")
CICY_AI_DIR = os.path.expanduser("~/cicy-ai")
CICY_CODE_REPO = "git@github-cicy-ai:cicy-ai/cicy-code.git"
# cicy-skills source is now vendored under cicy-code/skills/. The
# standalone cicy-ai/cicy-skills.git repo is frozen — we no longer clone it.
MIHOMO_LINUX_BIN = os.path.expanduser("~/projects/cicy-mihomo/bin/mihomo-linux-amd64")
IMAGE_REF = "cicybot/hk-spot-dev:latest"
VPC_CIDR = "172.20.0.0/16"
VSW_CIDR = "172.20.0.0/24"
VPC_NAME = "hk-spot-dev-vpc"
SG_NAME = "hk-spot-dev-sg"
SSH_PORT_HOST = 22
SSH_PORT_CONTAINER = 2022

HOST_NAME = "hk-spot-dev"
HOST_CICY_NAME = "hk-spot-dev-cicy"

GLOBAL_JSON = Path(os.path.expanduser("~/cicy-ai/global.json"))
CF_TUNNEL_JSON = Path(os.path.expanduser("~/cicy-ai/db/cf-tunnel.json"))  # Cloudflare config (set via `cf-tunnel config`)
CF_HOST_SUB = "hk-spot-dev"
CF_DEV_SUB = "dev-hk"
CF_TUNNEL_NAME = "hk-spot-dev"

LOCAL_SSH_CONFIG = Path.home() / ".ssh" / "config"
PROXY_SSH_JSON = Path(os.path.expanduser("~/cicy-ai/db/proxy_ssh.json"))


def cf_conf():
    try:
        return json.loads(CF_TUNNEL_JSON.read_text())["prod"]
    except FileNotFoundError:
        print(f"  Cloudflare config missing — run `cf-tunnel config` to set it up")
        return None
    except (KeyError, ValueError) as e:
        print(f"  Cloudflare config invalid ({CF_TUNNEL_JSON}): {e} — run `cf-tunnel config`")
        return None


def cf_api(method, path, token, body=None):
    url = "https://api.cloudflare.com/client/v4" + path
    cmd = ["curl", "-sS", "--max-time", "20", "-X", method, url,
           "-H", f"Authorization: Bearer {token}", "-H", "Content-Type: application/json"]
    if body is not None:
        cmd += ["--data", json.dumps(body)]
    try:
        p = subprocess.run(cmd, capture_output=True, text=True)
        return json.loads(p.stdout) if p.stdout.strip() else {"success": False, "errors": [p.stderr.strip()]}
    except Exception as e:
        return {"success": False, "errors": [str(e)]}


def cf_dns_host():
    cf = cf_conf()
    return f"{CF_HOST_SUB}.{cf['domain']}" if cf else None


def cf_dev_host():
    cf = cf_conf()
    return f"{CF_DEV_SUB}.{cf['domain']}" if cf else None


def cf_upsert_record(token, zone_id, name, rtype, content, proxied, ttl=1):
    res = cf_api("GET", f"/zones/{zone_id}/dns_records?type={rtype}&name={name}", token)
    recs = res.get("result") or []
    payload = {"type": rtype, "name": name, "content": content, "proxied": proxied, "ttl": ttl}
    if recs:
        rid = recs[0]["id"]
        cf_api("PUT", f"/zones/{zone_id}/dns_records/{rid}", token, payload)
    else:
        cf_api("POST", f"/zones/{zone_id}/dns_records", token, payload)


def update_cf_dns(ip):
    cf = cf_conf()
    if not cf:
        return
    name = f"{CF_HOST_SUB}.{cf['domain']}"
    cf_upsert_record(cf["api_token"], cf["zone_id"], name, "A", ip, proxied=False, ttl=60)
    print(f"  Cloudflare DNS: {name} -> {ip}")


def ensure_cf_tunnel():
    cf = cf_conf()
    if not cf:
        return None
    token, acct = cf["api_token"], cf["account_id"]
    res = cf_api("GET", f"/accounts/{acct}/cfd_tunnel?name={CF_TUNNEL_NAME}&is_deleted=false", token)
    tunnels = res.get("result") or []
    if tunnels:
        tid = tunnels[0]["id"]
    else:
        secret = base64.b64encode(secrets.token_bytes(32)).decode()
        res = cf_api("POST", f"/accounts/{acct}/cfd_tunnel", token,
                     {"name": CF_TUNNEL_NAME, "tunnel_secret": secret, "config_src": "cloudflare"})
        if not res.get("success"):
            print(f"  (cloudflare tunnel create failed: {res.get('errors')})")
            return None
        tid = res["result"]["id"]
        print(f"  Cloudflare tunnel created: {CF_TUNNEL_NAME} ({tid})")
    dev_host = f"{CF_DEV_SUB}.{cf['domain']}"
    cf_api("PUT", f"/accounts/{acct}/cfd_tunnel/{tid}/configurations", token,
           {"config": {"ingress": [
               {"hostname": dev_host, "service": "http://localhost:8008"},
               {"service": "http_status:404"},
           ]}})
    cf_upsert_record(token, cf["zone_id"], dev_host, "CNAME", f"{tid}.cfargotunnel.com", proxied=True)
    tk = cf_api("GET", f"/accounts/{acct}/cfd_tunnel/{tid}/token", token).get("result")
    print(f"  Cloudflare tunnel ready: {dev_host} -> :8008 (via tunnel {tid})")
    return tk


def run(cmd, **kw):
    kw.setdefault("check", True)
    kw.setdefault("capture_output", True)
    kw.setdefault("text", True)
    return subprocess.run(cmd, **kw)


def run_aliyun(args, retries=5):
    last = None
    for attempt in range(retries):
        p = subprocess.run(["aliyun"] + args, capture_output=True, text=True, timeout=40)
        if p.returncode == 0:
            return json.loads(p.stdout)
        last = (p.stdout or "") + (p.stderr or "")
        time.sleep(4 + attempt * 3)
    raise RuntimeError(f"aliyun {' '.join(args[:3])} failed after {retries} tries: {last.strip()[:300]}")


def run_ssh(host, cmd, **kw):
    kw.setdefault("timeout", 120)
    kw.setdefault("check", True)
    if "@" not in host:
        host = f"root@{host}"
    return subprocess.run(
        ["ssh", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new",
         "-o", "UserKnownHostsFile=/dev/null", "-o", "PasswordAuthentication=no",
         "-i", SSH_KEY, host, cmd], **kw)


def image_build_cmd(tag="hk-spot-dev:latest"):
    return (
        f'docker build -t {tag} - << \'DOCKERFILE\'\n'
        'FROM ghcr.io/cicy-ai/cicy-code-base:1.0.8\n'
        'USER root\n'
        # tuna mirrors return 403 from HK; switch to deb.debian.org
        "RUN cat /etc/apt/sources.list.d/debian.sources | sed 's|mirrors.tuna.tsinghua.edu.cn/debian|deb.debian.org/debian|g' > /tmp/debian.sources && \\\n"
        '    cp /tmp/debian.sources /etc/apt/sources.list.d/debian.sources && \\\n'
        '    apt-get update && apt-get install -y --no-install-recommends \\\n'
        '    openssh-server docker.io git make build-essential ca-certificates curl \\\n'
        '    rsync autossh iproute2 net-tools jq vim less unzip wget gnupg \\\n'
        '    && rm -rf /var/lib/apt/lists/*\n'
        'RUN curl -fsSL https://github.com/cicy-ai/cicy-mihomo/releases/latest/download/mihomo-linux-amd64 -o /usr/local/bin/mihomo && chmod 0755 /usr/local/bin/mihomo || echo "[image] mihomo not pre-installed"\n'
        'RUN curl -fsSL https://go.dev/dl/go1.25.3.linux-amd64.tar.gz | tar -C /usr/local -xz && \\\n'
        '    ln -sf /usr/local/go/bin/go /usr/local/bin/go && \\\n'
        '    ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt\n'
        "RUN { echo 'export PATH=/usr/local/go/bin:$PATH'; echo 'export GOPROXY=https://goproxy.cn,direct'; } > /etc/profile.d/go.sh\n"
        'ENV PATH=/usr/local/go/bin:$PATH\n'
        'ENV GOPROXY=https://goproxy.cn,direct\n'
        'RUN sed -i "s/^#\\?Port.*/Port 2022/" /etc/ssh/sshd_config && \\\n'
        '    sed -i "s/^#\\?PermitRootLogin.*/PermitRootLogin prohibit-password/" /etc/ssh/sshd_config && \\\n'
        '    sed -i "s/^#\\?PubkeyAuthentication.*/PubkeyAuthentication yes/" /etc/ssh/sshd_config && \\\n'
        '    sed -i "s/^#\\?PasswordAuthentication.*/PasswordAuthentication no/" /etc/ssh/sshd_config && \\\n'
        '    mkdir -p /run/sshd\n'
        'RUN groupdel docker 2>/dev/null; true\n'
        'COPY <<\'ENTRYPOINT\' /usr/local/bin/spot-entrypoint.sh\n'
        '#!/bin/bash\n'
        'set -e\n'
        'if [ -e /var/run/docker.sock ]; then\n'
        '    DOCKER_GID=$(stat -c "%g" /var/run/docker.sock)\n'
        '    if ! getent group $DOCKER_GID >/dev/null 2>&1; then\n'
        '        groupadd -g $DOCKER_GID docker-host\n'
        '    fi\n'
        '    usermod -aG $DOCKER_GID cicy\n'
        'fi\n'
        'if [ -d /home/cicy/.ssh ]; then\n'
        '    chown -R cicy:cicy /home/cicy/.ssh\n'
        '    chmod 700 /home/cicy/.ssh\n'
        '    chmod 600 /home/cicy/.ssh/authorized_keys 2>/dev/null || true\n'
        'fi\n'
        'chown cicy:cicy /home/cicy 2>/dev/null || true\n'
        'service ssh start\n'
        'echo "[spot-dev] SSHD running on port 2022"\n'
        'tail -f /dev/null\n'
        'ENTRYPOINT\n'
        'RUN chmod +x /usr/local/bin/spot-entrypoint.sh\n'
        'EXPOSE 2022\n'
        'ENTRYPOINT ["/usr/local/bin/spot-entrypoint.sh"]\n'
        'DOCKERFILE'
    )


def _registry_creds():
    u, p = os.environ.get("DOCKER_REGISTRY_USER", ""), os.environ.get("DOCKER_REGISTRY_PASS", "")
    if u and p:
        return u, p
    try:
        out = subprocess.run(["docker-credential-desktop", "get"], input="https://index.docker.io/v1/",
                             capture_output=True, text=True, timeout=10).stdout
        j = json.loads(out)
        return j.get("Username", ""), j.get("Secret", "")
    except Exception:
        return "", ""


def push_image():
    inst = find_running_instance()
    if not inst:
        print("ERROR: no running hk-spot-dev instance to build the image on"); sys.exit(1)
    ip = inst.get("PublicIpAddress", {}).get("IpAddress", [None])[0]
    user, passwd = _registry_creds()
    if not user or not passwd:
        print("ERROR: set DOCKER_REGISTRY_USER and DOCKER_REGISTRY_PASS (or have Docker Desktop logged in)"); sys.exit(1)
    print(f"=== Building {IMAGE_REF} on {ip} ===")
    run_ssh(ip, image_build_cmd(IMAGE_REF), timeout=900, check=False)
    print(f"=== Pushing {IMAGE_REF} ===")
    run_ssh(ip, f"echo {_shlex.quote(passwd)} | docker login -u {_shlex.quote(user)} --password-stdin "
                f"&& docker push {IMAGE_REF}", timeout=900, check=False)
    print(f"  Done. Pre-built image: {IMAGE_REF}")


def find_persistent_disk():
    try:
        r = run_aliyun(["ecs", "DescribeDisks", "--region", REGION,
                        "--DiskName", DISK_NAME, "--Status", "Available"])
        disks = r.get("Disks", {}).get("Disk", [])
        if disks:
            return disks[0]
    except Exception:
        pass
    return None


def find_any_instance():
    try:
        r = run_aliyun(["ecs", "DescribeInstances", "--region", REGION,
                        "--InstanceName", INSTANCE_NAME])
        insts = r.get("Instances", {}).get("Instance", [])
        for inst in insts:
            if inst.get("Status") == "Running":
                return inst
        return insts[0] if insts else None
    except Exception:
        return None


def find_running_instance():
    inst = find_any_instance()
    return inst if inst and inst.get("Status") == "Running" else None


def find_sg(vpc_id):
    try:
        r = run_aliyun(["ecs", "DescribeSecurityGroups", "--region", REGION,
                        "--VpcId", vpc_id])
        for sg in r.get("SecurityGroups", {}).get("SecurityGroup", []):
            if sg.get("SecurityGroupName") == SG_NAME:
                return sg
    except Exception:
        pass
    return None


def ensure_vpc():
    try:
        vpcs = run_aliyun(["vpc", "DescribeVpcs", "--region", REGION])
        for v in vpcs.get("Vpcs", {}).get("Vpc", []):
            if v.get("VpcName") == VPC_NAME:
                return v["VpcId"]
    except Exception:
        pass
    vpc = run_aliyun(["vpc", "CreateVpc", "--region", REGION,
                      "--CidrBlock", VPC_CIDR, "--VpcName", VPC_NAME])
    time.sleep(3)
    return vpc["VpcId"]


def ensure_vswitch(vpc_id):
    try:
        vsws = run_aliyun(["vpc", "DescribeVSwitches", "--region", REGION,
                           "--VpcId", vpc_id])
        for v in vsws.get("VSwitches", {}).get("VSwitch", []):
            if v["ZoneId"] == ZONE and v.get("CidrBlock") == VSW_CIDR:
                return v["VSwitchId"]
    except Exception:
        pass
    vsw = run_aliyun(["vpc", "CreateVSwitch", "--region", REGION, "--ZoneId", ZONE,
                      "--CidrBlock", VSW_CIDR, "--VpcId", vpc_id,
                      "--VSwitchName", "hk-spot-dev-vsw"])
    return vsw["VSwitchId"]


def ensure_sg(vpc_id):
    existing = find_sg(vpc_id)
    sg_id = existing["SecurityGroupId"] if existing else None
    if not sg_id:
        sg = run_aliyun(["ecs", "CreateSecurityGroup", "--region", REGION,
                         "--SecurityGroupName", SG_NAME, "--VpcId", vpc_id])
        sg_id = sg["SecurityGroupId"]
    # Get existing port ranges
    existing_rules = set()
    try:
        attr = run_aliyun(["ecs", "DescribeSecurityGroupAttribute", "--region", REGION,
                           "--SecurityGroupId", sg_id])
        for p in attr.get("Permissions", {}).get("Permission", []):
            existing_rules.add(p["PortRange"])
    except Exception:
        pass
    rules = [
        ("22/22", "SSH host"),
        (f"{SSH_PORT_CONTAINER}/{SSH_PORT_CONTAINER}", "SSH container"),
        ("8000/9999", "app ports"),
    ]
    for port, desc in rules:
        if port in existing_rules:
            continue
        print(f"  add SG rule: {port} ({desc})")
        run_aliyun(["ecs", "AuthorizeSecurityGroup", "--region", REGION,
                    "--SecurityGroupId", sg_id, "--IpProtocol", "tcp",
                    "--PortRange", port, "--SourceCidrIp", "0.0.0.0/0",
                    "--Description", desc])
    return sg_id


def purge_known_hosts(ip):
    names = [ip]
    h = cf_dns_host()
    if h:
        names.append(h)
    for n in names:
        for target in (n, f"[{n}]:{SSH_PORT_HOST}", f"[{n}]:{SSH_PORT_CONTAINER}"):
            subprocess.run(["ssh-keygen", "-R", target], check=False, capture_output=True, text=True)


def _strip_ssh_block(content, host):
    out, lines, skip = [], content.splitlines(), False
    for ln in lines:
        if ln.strip().startswith("Host "):
            skip = (ln.strip() == f"Host {host}")
        if not skip:
            out.append(ln)
    return "\n".join(out)


def update_ssh_config(ip):
    purge_known_hosts(ip)
    hostname = cf_dns_host() or ip
    entries = {
        HOST_NAME: f"Host {HOST_NAME}\n    HostName {hostname}\n    User root\n    Port {SSH_PORT_HOST}\n    IdentityFile {SSH_KEY}\n    StrictHostKeyChecking accept-new\n",
        HOST_CICY_NAME: f"Host {HOST_CICY_NAME}\n    HostName {hostname}\n    User cicy\n    Port {SSH_PORT_CONTAINER}\n    IdentityFile {SSH_KEY}\n    StrictHostKeyChecking accept-new\n",
    }
    content = LOCAL_SSH_CONFIG.read_text() if LOCAL_SSH_CONFIG.exists() else ""
    for host, entry in entries.items():
        content = _strip_ssh_block(content, host).rstrip() + "\n" + entry
    LOCAL_SSH_CONFIG.write_text(content.lstrip("\n"))
    print(f"  Updated ~/.ssh/config (HostName {hostname})")


def update_proxy_ssh_json(ip):
    entry = {
        "name": HOST_NAME,
        "proxy_url": f"ssh://cicy@{ip}:{SSH_PORT_CONTAINER}",
        "start_cmd": f"ssh -N -f -L {SSH_PORT_CONTAINER}:localhost:{SSH_PORT_CONTAINER} -i {SSH_KEY} -p {SSH_PORT_CONTAINER} -o BatchMode=yes -o ConnectTimeout=8 -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=accept-new -o TCPKeepAlive=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 cicy@{ip}",
    }
    entries = []
    if PROXY_SSH_JSON.exists():
        try:
            entries = json.loads(PROXY_SSH_JSON.read_text())
        except Exception:
            entries = []
    entries = [e for e in entries if e.get("name") != HOST_NAME]
    entries.append(entry)
    PROXY_SSH_JSON.write_text(json.dumps(entries, indent=2) + "\n")
    print(f"  Updated {PROXY_SSH_JSON}")


def _ssh_opts():
    return ["-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=/dev/null",
            "-o", "BatchMode=yes", "-o", "ConnectTimeout=15"]


def scp_to_cicy(ip, src, dst):
    return subprocess.run(["scp"] + _ssh_opts() + ["-P", str(SSH_PORT_CONTAINER),
                          "-i", SSH_KEY, src, f"cicy@{ip}:{dst}"],
                          check=False, capture_output=True, text=True)


def ssh_cicy(ip, cmd, timeout=120, check=False):
    return subprocess.run(["ssh"] + _ssh_opts() + ["-p", str(SSH_PORT_CONTAINER),
                          "-i", SSH_KEY, f"cicy@{ip}", cmd],
                          check=check, capture_output=True, text=True, timeout=timeout)


def bootstrap_container(ip):
    """Bring the container up. On a fresh persistent disk: seed SSH keys, ~/cicy-ai,
    and clone repos. If the disk already has data, leave it alone — just start the
    services (the user's edits on the box stay; we don't rsync over them)."""
    print("=== Bootstrapping container ===")
    id_rsa_pub = Path(os.path.expanduser("~/.ssh/id_rsa.pub"))
    if id_rsa_pub.exists():
        pub = id_rsa_pub.read_text().strip()
        run_ssh(ip, f'echo {_shlex.quote(pub)} >> /root/.ssh/authorized_keys', timeout=30)
        ssh_cicy(ip, f'echo {_shlex.quote(pub)} >> ~/.ssh/authorized_keys', timeout=30)
    ssh_cicy(ip, "mkdir -p ~/.ssh ~/cicy-ai/db ~/projects ~/.local/bin && chmod 700 ~/.ssh")
    populated = ssh_cicy(ip, "test -d ~/projects/cicy-code/.git && test -f ~/cicy-ai/global.json "
                             "&& echo yes || echo no").stdout.strip().endswith("yes")
    if populated:
        print("  persistent disk already populated — skipping data seed")
    else:
        print("  fresh disk — seeding SSH keys / ~/cicy-ai / repos ...")
        for src, name, dst_path in [(GLOBAL_JSON, "global.json", "~/cicy-ai/db/"),
                                    (PROXY_SSH_JSON, "proxy_ssh.json", "~/cicy-ai/db/"),
                                    (Path(RSA_KEY), "id_rsa", "~/.ssh/")]:
            if not os.path.exists(src):
                continue
            scp_to_cicy(ip, str(src), dst_path + name)
        if os.path.exists(RSA_KEY):
            ssh_cicy(ip, "chmod 600 ~/.ssh/id_rsa")
        ssh_cicy(ip, "grep -q 'Host github-cicy-ai' ~/.ssh/config 2>/dev/null || "
                     "printf 'Host github-cicy-ai\\n  HostName github.com\\n  User git\\n  "
                     "IdentityFile ~/.ssh/id_rsa\\n  IdentitiesOnly yes\\n  "
                     "StrictHostKeyChecking accept-new\\n' >> ~/.ssh/config; "
                     "touch ~/.ssh/config; chmod 600 ~/.ssh/config")
        ssh_cicy(ip, f"git clone {CICY_CODE_REPO} ~/projects/cicy-code 2>/dev/null || true", timeout=300)
    # always: ensure cicy-mihomo wrapper is built, mihomo binary exists, gen-config if missing
    ssh_cicy(ip, "[ -x ~/.local/bin/cicy-hosttools ] || { cd ~/projects/cicy-code/skills 2>/dev/null && "
                 "/usr/local/go/bin/go build -o ~/.local/bin/cicy-hosttools ./cmd/cicy-hosttools 2>/dev/null; } || true; "
                 "ln -sf cicy-hosttools ~/.local/bin/cicy-mihomo 2>/dev/null; true", timeout=240)
    ssh_cicy(ip, "[ -x /usr/local/bin/mihomo ] || (curl -fsSL https://github.com/cicy-ai/cicy-mihomo/releases/latest/download/mihomo-linux-amd64 -o /tmp/mihomo && install -m 0755 /tmp/mihomo /usr/local/bin/mihomo) 2>/dev/null || true", timeout=60)
    ssh_cicy(ip, "rm -f ~/.local/bin/mihomo; true", timeout=10)
    ssh_cicy(ip, "[ -f ~/cicy-ai/db/mihomo.yaml ] || ~/.local/bin/cicy-mihomo gen-config 2>/dev/null; true", timeout=30)
    # always: start proxy_ssh profiles and mihomo
    ssh_cicy(ip, "[ -f ~/cicy-ai/db/proxy_ssh.json ] && command -v python3 >/dev/null && "
                 "python3 ~/projects/cicy-code/skills/proxy_ssh list --json 2>/dev/null "
                 "| python3 -c 'import json,sys; [print(p[\"name\"]) for p in json.load(sys.stdin)]' 2>/dev/null "
                 "| while read n; do python3 ~/projects/cicy-code/skills/proxy_ssh start \"$n\" 2>/dev/null; done; true", timeout=90)
    ssh_cicy(ip, "[ -f ~/cicy-ai/db/mihomo.yaml ] && [ -x /usr/local/bin/mihomo ] && "
                 "rm -f ~/.local/bin/mihomo; "
                 "MIHOMO_BIN=/usr/local/bin/mihomo ~/.local/bin/cicy-mihomo stop >/dev/null 2>&1; "
                 "mkdir -p ~/.local/state/cicy-skills/mihomo; "
                 "MIHOMO_BIN=/usr/local/bin/mihomo ~/.local/bin/cicy-mihomo start || true", timeout=60)
    print("  Container bootstrapped — cicy-code cloned, mihomo+proxy_ssh started")


def clean_local_configs():
    for host in [HOST_NAME, HOST_CICY_NAME]:
        if LOCAL_SSH_CONFIG.exists():
            lines = LOCAL_SSH_CONFIG.read_text().splitlines()
            kept, skip = [], False
            for line in lines:
                if line.strip().startswith(f"Host {host}"):
                    skip = True; continue
                if skip and line.startswith("Host "):
                    skip = False
                if not skip:
                    kept.append(line)
            LOCAL_SSH_CONFIG.write_text("\n".join(kept) + "\n")
    if PROXY_SSH_JSON.exists():
        try:
            entries = json.loads(PROXY_SSH_JSON.read_text())
            entries = [e for e in entries if e.get("name") != HOST_NAME]
            PROXY_SSH_JSON.write_text(json.dumps(entries, indent=2) + "\n")
        except Exception:
            pass


def destroy():
    while True:
        inst = find_any_instance()
        if not inst:
            break
        inst_id = inst["InstanceId"]
        ip = inst.get("PublicIpAddress", {}).get("IpAddress", [None])[0]
        print(f"  Deleting instance {inst_id} ({ip}) [status={inst.get('Status')}]...")
        try:
            run_aliyun(["ecs", "DeleteInstance", "--region", REGION,
                        "--InstanceId", inst_id, "--Force", "true"])
        except Exception as e:
            print(f"  (delete failed, retrying: {e})"); time.sleep(8)
        else:
            print("  Deleted."); time.sleep(3)
    disk = find_persistent_disk()
    if disk:
        print(f"  Persistent disk {disk['DiskId']} kept (available)")
    clean_local_configs()
    print("\nDone.")


def main():
    if "--destroy" in sys.argv:
        destroy(); return
    if "--push-image" in sys.argv:
        push_image(); return

    existing = find_running_instance()
    if existing:
        ip = existing.get("PublicIpAddress", {}).get("IpAddress", [None])[0]
        print(f"  {INSTANCE_NAME} already running at {ip}")
        update_cf_dns(ip)
        bootstrap_container(ip)
        return

    output_json = "--json" in sys.argv

    disk = find_persistent_disk()
    if disk:
        disk_id = disk["DiskId"]
        print(f"=== Using existing persistent disk: {disk_id} ===")
    else:
        print(f"=== Creating persistent disk ({DISK_SIZE}GB {DISK_CATEGORY})... ===")
        r = run_aliyun(["ecs", "CreateDisk", "--region", REGION, "--ZoneId", ZONE,
                        "--DiskName", DISK_NAME, "--Size", str(DISK_SIZE),
                        "--DiskCategory", DISK_CATEGORY])
        disk_id = r["DiskId"]
        print(f"  Created disk: {disk_id}")
        time.sleep(5)

    print("=== Ensuring network... ===")
    vpc_id = ensure_vpc()
    vsw_id = ensure_vswitch(vpc_id)
    sg_id = ensure_sg(vpc_id)
    print(f"  VPC: {vpc_id}  VSwitch: {vsw_id}  SG: {sg_id}")

    print("=== Importing SSH key... ===")
    try:
        run_aliyun(["ecs", "ImportKeyPair", "--region", REGION,
                    "--KeyPairName", KEY_NAME,
                    "--PublicKeyBody", Path(SSH_KEY).with_suffix(".pub").read_text().strip()])
    except Exception:
        pass

    print("=== Creating spot instance... ===")
    inst = run_aliyun(["ecs", "RunInstances", "--region", REGION, "--ZoneId", ZONE,
                       "--InstanceType", INSTANCE_TYPE, "--ImageId", IMAGE_ID,
                       "--SecurityGroupId", sg_id, "--VSwitchId", vsw_id,
                       "--InstanceChargeType", "PostPaid",
                       "--SpotStrategy", SPOT_STRATEGY,
                       "--InternetChargeType", "PayByTraffic",
                       "--InternetMaxBandwidthOut", "100",
                       "--SystemDisk.Category", SYSTEM_DISK_CATEGORY,
                       "--SystemDisk.Size", str(SYSTEM_DISK_SIZE),
                       "--InstanceName", INSTANCE_NAME, "--HostName", INSTANCE_NAME,
                       "--KeyPairName", KEY_NAME, "--Amount", "1"])
    inst_id = inst["InstanceIdSets"]["InstanceIdSet"][0]
    print(f"  Instance: {inst_id}")

    print("=== Waiting for IP... ===")
    ip = None
    for _ in range(15):
        time.sleep(10)
        desc = run_aliyun(["ecs", "DescribeInstances", "--region", REGION,
                           "--InstanceIds", json.dumps([inst_id])])
        info = desc["Instances"]["Instance"][0]
        ips = info.get("PublicIpAddress", {}).get("IpAddress", [])
        if ips:
            ip = ips[0]
            print(f"  IP: {ip}")
            break
    if not ip:
        print("ERROR: timeout waiting for IP"); sys.exit(1)

    update_cf_dns(ip)

    print("=== Attaching persistent disk... ===")
    run_aliyun(["ecs", "AttachDisk", "--region", REGION,
                "--InstanceId", inst_id, "--DiskId", disk_id])
    time.sleep(8)

    print("=== Waiting for SSH... ===")
    for _ in range(15):
        time.sleep(5)
        p = subprocess.run(["ssh", "-o", "ConnectTimeout=5",
                            "-o", "StrictHostKeyChecking=accept-new",
                            "-o", "UserKnownHostsFile=/dev/null",
                            "-o", "PasswordAuthentication=no",
                            "-i", SSH_KEY, f"root@{ip}", "echo ready"],
                           check=False, capture_output=True)
        if p.returncode == 0:
            break
    else:
        print("ERROR: SSH timeout"); sys.exit(1)

    print("=== Setting up instance... ===")
    run_ssh(ip, 'set -e; '
           'DISK=$(lsblk -ndo NAME | grep "^vd" | grep -v "^vda" | head -1); '
           'if [ -n "$DISK" ]; then '
           '  DEV=/dev/$DISK; '
           '  blkid "$DEV" >/dev/null 2>&1 || mkfs.ext4 -F "$DEV"; '
           '  mkdir -p /data; '
           '  mountpoint -q /data || mount "$DEV" /data; '
           '  grep -q " /data " /etc/fstab || echo "$DEV /data ext4 defaults 0 0" >> /etc/fstab; '
           'fi', timeout=60)

    setup_commands = [
        'curl -fsSL https://get.docker.com | sh',
        'systemctl stop docker 2>/dev/null; mkdir -p /data/docker',
        'cat > /etc/docker/daemon.json << \'EOF\'\n{"data-root": "/data/docker", "storage-driver": "overlay2"}\nEOF',
        'systemctl start docker && systemctl enable docker',
        'docker info | grep "Docker Root Dir"',
    ]
    for cmd in setup_commands:
        run_ssh(ip, cmd, timeout=180, check=False)
    print("  Docker ready")

    print("=== Preparing hk-spot-dev image... ===")
    run_ssh(ip, f'docker pull {IMAGE_REF} 2>/dev/null && docker tag {IMAGE_REF} hk-spot-dev:latest '
                f'&& echo "[image] pulled pre-built {IMAGE_REF}" || ( {image_build_cmd()} )',
            timeout=600, check=False)
    print("  Image ready")

    id_rsa_pub = Path(os.path.expanduser("~/.ssh/id_rsa.pub"))
    pub_extra = id_rsa_pub.read_text().strip() if id_rsa_pub.exists() else ""
    run_ssh(ip, 'mkdir -p /data/cicy/.ssh; '
           'cp /root/.ssh/authorized_keys /data/cicy/.ssh/authorized_keys 2>/dev/null; '
           'cat /root/.ssh/id_ed25519.pub >> /data/cicy/.ssh/authorized_keys 2>/dev/null; '
           f'echo {_shlex.quote(pub_extra)} >> /root/.ssh/authorized_keys; '
           f'echo {_shlex.quote(pub_extra)} >> /data/cicy/.ssh/authorized_keys; '
           'sort -u /root/.ssh/authorized_keys -o /root/.ssh/authorized_keys; '
           'sort -u /data/cicy/.ssh/authorized_keys -o /data/cicy/.ssh/authorized_keys; '
           'chmod 700 /root/.ssh; chmod 600 /root/.ssh/authorized_keys; '
           'chmod 700 /data/cicy/.ssh; chmod 600 /data/cicy/.ssh/authorized_keys; '
           'chown -R 1001:1001 /data/cicy; echo done', timeout=15)

    print("=== Starting container... ===")
    run_ssh(ip, 'docker rm -f hk-spot-dev 2>/dev/null; '
           'docker run -d --name hk-spot-dev '
           '--restart unless-stopped --network host --privileged '
           '-v /data/cicy:/home/cicy '
           '-v /var/run/docker.sock:/var/run/docker.sock '
           'hk-spot-dev:latest', timeout=30)
    time.sleep(5)

    print("=== Testing ===")
    run_ssh(ip, 'docker ps | grep hk-spot-dev', check=False)
    test = run(["ssh", "-o", "StrictHostKeyChecking=accept-new",
                "-o", "UserKnownHostsFile=/dev/null", "-o", "BatchMode=yes",
                "-o", "ConnectTimeout=10", "-p", str(SSH_PORT_CONTAINER),
                "-i", SSH_KEY, f"cicy@{ip}",
                "echo SSH_OK && docker ps -q | wc -l"], check=False)
    print(f"  Container test: {test.stdout.strip() if test.returncode == 0 else 'FAILED'}")

    bootstrap_container(ip)

    dns_host = cf_dns_host()
    if output_json:
        print(json.dumps({
            "instance_id": inst_id, "ip": ip, "disk_id": disk_id,
            "region": REGION, "zone": ZONE, "type": INSTANCE_TYPE,
            "ssh_host": HOST_NAME, "dns": dns_host,
            "ssh_cicy": f"ssh -p {SSH_PORT_CONTAINER} -i {SSH_KEY} cicy@{ip}",
        }, indent=2))
    else:
        print(f"\n=== Done ===")
        print(f"  IP: {ip}" + (f"  DNS: {dns_host}" if dns_host else ""))
        print(f"  SSH to host: ssh {HOST_NAME}")
        print(f"  SSH to container: ssh {HOST_CICY_NAME}  (port {SSH_PORT_CONTAINER})")
        print(f"  Disk {disk_id} persists across instance reclaims")
        print(f"  Rebuild: hk-spot-dev   |   Delete instance (disk kept): hk-spot-dev --destroy")


if __name__ == "__main__":
    main()
