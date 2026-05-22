import { execShell } from './rpc';
import type { Category, MirrorCandidate } from './mirrors';
import { CN_MIRRORS } from './mirrors';
import type { OS, Region } from './detect';

export interface Env {
  os: OS;
  wslDistro?: string; // when set + os==='windows', steps run inside this WSL distro
}

export interface ApplyStep {
  id: string;            // unique key, e.g. "npm:npmmirror"
  category: Category;
  pickId: string;
  label: string;         // human-readable, e.g. "npm registry → npmmirror.com"
  run: () => Promise<{ ok: boolean; message: string }>;
}

// Encode a bash command as base64 and decode on the other side so we don't
// have to wrestle with Windows-cmd→wsl.exe→bash double-quoting (we hit this
// during the WSL install session — base64 is the only reliably portable
// transport for arbitrary shell text).
function pipeBase64(env: Env, script: string): string {
  const b64 = btoa(unescape(encodeURIComponent(script)));
  if (env.os === 'windows' && env.wslDistro) {
    return `wsl -d ${env.wslDistro} -- bash -lc "echo ${b64} | base64 -d | bash"`;
  }
  return `bash -lc "echo ${b64} | base64 -d | bash"`;
}

async function shellRun(env: Env, script: string): Promise<string> {
  return execShell(pipeBase64(env, script));
}

function makeStep(env: Env, cat: Category, c: MirrorCandidate): ApplyStep | null {
  const id = `${cat}:${c.id}`;
  const baseLabel = (extra: string) => `${extra} → ${c.label}`;

  switch (cat) {
    case 'npm':
      return {
        id, category: cat, pickId: c.id, label: baseLabel('npm registry'),
        run: async () => {
          const script = `set -e
if command -v npm >/dev/null 2>&1; then
  npm config set registry ${c.config.registry}
  npm config get registry
else
  mkdir -p "$HOME"
  printf 'registry=%s\\n' ${c.config.registry} > "$HOME/.npmrc"
  cat "$HOME/.npmrc"
fi`;
          const out = await shellRun(env, script);
          return { ok: out.includes(c.config.registry), message: out.trim() };
        },
      };

    case 'pypi':
      return {
        id, category: cat, pickId: c.id, label: baseLabel('pip index'),
        run: async () => {
          const script = `set -e
mkdir -p "$HOME/.config/pip" "$HOME/.pip"
cat > "$HOME/.config/pip/pip.conf" <<'EOF'
[global]
index-url = ${c.config.index}
trusted-host = ${c.config.host}
EOF
cp "$HOME/.config/pip/pip.conf" "$HOME/.pip/pip.conf"
cat "$HOME/.config/pip/pip.conf"`;
          const out = await shellRun(env, script);
          return { ok: out.includes(c.config.index), message: out.trim() };
        },
      };

    case 'go':
      return {
        id, category: cat, pickId: c.id, label: baseLabel('goproxy'),
        run: async () => {
          const script = `set -e
if command -v go >/dev/null 2>&1; then
  go env -w GOPROXY=${c.config.goproxy} GOSUMDB=off
  go env GOPROXY
else
  mkdir -p "$HOME/.config/go"
  cat > "$HOME/.config/go/env" <<EOF
GOPROXY=${c.config.goproxy}
GOSUMDB=off
EOF
  cat "$HOME/.config/go/env"
fi`;
          const out = await shellRun(env, script);
          return { ok: out.includes(c.config.goproxy.split(',')[0]), message: out.trim() };
        },
      };

    case 'apt': {
      const host = c.config.host;
      return {
        id, category: cat, pickId: c.id, label: baseLabel('apt sources'),
        run: async () => {
          // Both layouts: legacy /etc/apt/sources.list (pre-noble) and the
          // deb822 /etc/apt/sources.list.d/ubuntu.sources (noble+).
          const script = `set -e
if [ -f /etc/apt/sources.list ]; then
  sudo cp -n /etc/apt/sources.list /etc/apt/sources.list.cicy.bak 2>/dev/null || true
  sudo sed -i -E 's|https?://[a-z]*\\.?archive\\.ubuntu\\.com|https://${host}|g; s|https?://security\\.ubuntu\\.com|https://${host}|g; s|https?://ports\\.ubuntu\\.com|https://${host}|g' /etc/apt/sources.list
fi
if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  sudo cp -n /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list.d/ubuntu.sources.cicy.bak 2>/dev/null || true
  sudo sed -i -E 's|https?://[a-z]*\\.?archive\\.ubuntu\\.com|https://${host}|g; s|https?://security\\.ubuntu\\.com|https://${host}|g; s|https?://ports\\.ubuntu\\.com|https://${host}|g' /etc/apt/sources.list.d/ubuntu.sources
fi
grep -h '${host}' /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null | head -3 || true`;
          const out = await shellRun(env, script);
          return { ok: out.includes(host), message: out.trim() };
        },
      };
    }

    case 'brew':
      return {
        id, category: cat, pickId: c.id, label: baseLabel('Homebrew'),
        run: async () => {
          const script = `set -e
if ! command -v brew >/dev/null 2>&1; then echo "brew not installed; skipping"; exit 0; fi
REPO="$(brew --repo)"
git -C "$REPO" remote set-url origin ${c.config.brew} || true
if [ -d "$REPO/Library/Taps/homebrew/homebrew-core" ]; then
  git -C "$REPO/Library/Taps/homebrew/homebrew-core" remote set-url origin ${c.config.core} || true
fi
# Persist bottle domain into shell rc files so future shells pick it up.
for rc in "$HOME/.zshrc" "$HOME/.bash_profile"; do
  [ -f "$rc" ] || continue
  grep -q HOMEBREW_BOTTLE_DOMAIN "$rc" || echo 'export HOMEBREW_BOTTLE_DOMAIN=${c.config.bottle}' >> "$rc"
done
git -C "$REPO" remote -v | head -1`;
          const out = await shellRun(env, script);
          return { ok: out.includes(c.config.brew) || out.includes('skipping'), message: out.trim() };
        },
      };

    case 'gh':
      return {
        id, category: cat, pickId: c.id, label: baseLabel('GitHub proxy'),
        run: async () => {
          const script = `set -e
mkdir -p "$HOME/.cicy"
printf '%s' '${c.config.prefix.replace(/'/g, "'\\''")}' > "$HOME/.cicy/gh_proxy"
cat "$HOME/.cicy/gh_proxy"
echo
echo " (other cicy tools read ~/.cicy/gh_proxy and prepend it to github.com URLs)"`;
          const out = await shellRun(env, script);
          return { ok: out.startsWith(c.config.prefix) || c.config.prefix === '', message: out.trim() };
        },
      };

    case 'rootfs':
      // Heavy. Handled by the WSL install card, not the regular apply loop.
      return null;
  }
  return null;
}

export function buildPlan(env: Env, picks: Partial<Record<Category, string>>): ApplyStep[] {
  const steps: ApplyStep[] = [];
  (Object.keys(picks) as Category[]).forEach(cat => {
    const pickId = picks[cat];
    if (!pickId) return;
    const candidate = CN_MIRRORS[cat].find(c => c.id === pickId);
    if (!candidate) return;
    const s = makeStep(env, cat, candidate);
    if (s) steps.push(s);
  });
  return steps;
}

// ---------- Persistence ----------

export interface PersistedConfig {
  os: OS;
  region: Region;
  picks: Partial<Record<Category, string>>;
  ts: string;
  // schema version so we can migrate later without bricking
  v: 1;
}

export async function readPersisted(): Promise<PersistedConfig | null> {
  try {
    const out = await execShell(`cat "$HOME/.cicy/speedup.json" 2>/dev/null || echo ''`);
    const trimmed = out.trim();
    if (!trimmed) return null;
    return JSON.parse(trimmed) as PersistedConfig;
  } catch {
    return null;
  }
}

export async function writePersisted(cfg: PersistedConfig): Promise<void> {
  const body = JSON.stringify(cfg, null, 2);
  const b64 = btoa(unescape(encodeURIComponent(body)));
  await execShell(`bash -lc "mkdir -p \\"$HOME/.cicy\\" && echo ${b64} | base64 -d > \\"$HOME/.cicy/speedup.json\\""`);
}
