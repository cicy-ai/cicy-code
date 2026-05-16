// Pure link-detection helpers shared by the xterm link provider and the unit
// tests. No DOM / xterm dependencies — only string scanning.

// URL detection: anything starting with `http://` or `https://` (no `ws://` /
// `wss://` — those are protocol-level connection schemes, not user-clickable
// resources). The character classes exclude non-ASCII code units (+)
// so a URL followed by CJK / emoji / accented text terminates correctly —
// e.g. `https://example.com/path然后…` stops at "然" rather than swallowing
// the trailing CJK. Trailing punctuation is further trimmed via the
// negative-set tail.
export const URL_RE = /\bhttps?:\/\/[^\s"'<>`-￿]+[^\s"'<>`!?,.;:)\]}-￿]/g;
export const LOCAL_PROTOCOL_RE = /(?:file|image):\/\/[^\s"'!*(){}|\\\^<>`]*[^\s"':,.!?{}|\\\^~\[\]`()<>]/g;
export const FILENAME_CANDIDATE_RE = /(?:\.{1,2}\/|~\/|\/)?(?:[A-Za-z0-9._-]+\/)*[A-Za-z0-9._-]+(?::\d+){0,2}/g;

const IPV4_RE = /^\d+\.\d+\.\d+\.\d+$/;

export const FILE_EXTENSIONS = new Set<string>([
    "ts", "tsx", "js", "jsx", "mjs", "cjs", "go", "rs", "py", "pyi", "rb", "java", "kt", "kts", "swift",
    "c", "h", "cpp", "hpp", "cc", "cxx", "hh", "m", "mm", "php", "pl", "sh", "bash", "zsh", "fish",
    "ps1", "lua", "dart", "scala", "sc", "ex", "exs", "erl", "hs", "sql", "vue", "svelte", "elm",
    "clj", "cljs", "cljc", "r", "jl", "nim", "zig", "v", "d", "gleam", "fs", "fsx",
    "json", "jsonc", "json5", "yaml", "yml", "toml", "ini", "cfg", "conf", "env", "xml", "plist", "properties",
    "md", "mdx", "rst", "txt", "log", "adoc", "tex",
    "html", "htm", "css", "scss", "sass", "less", "styl",
    "mod", "sum", "mk", "lock",
    "csv", "tsv", "jsonl", "ndjson",
    "png", "jpg", "jpeg", "gif", "svg", "ico", "webp", "bmp", "tiff", "avif",
    "pdf", "zip", "tar", "gz", "tgz", "bz2", "xz", "7z", "rar",
]);

export const EXACT_BASENAMES = new Set<string>([
    "Dockerfile", "Containerfile", "Makefile", "GNUmakefile", "Procfile", "Rakefile",
    "go.mod", "go.sum",
    "package.json", "package-lock.json", "tsconfig.json", "jsconfig.json",
    "yarn.lock", "pnpm-lock.yaml", "bun.lockb",
    "deno.json", "deno.jsonc",
    "requirements.txt", "poetry.lock", "pyproject.toml", "Pipfile", "Pipfile.lock",
    "Cargo.toml", "Cargo.lock",
    "Gemfile", "Gemfile.lock",
    "composer.json", "composer.lock",
    "CMakeLists.txt", "build.gradle", "build.gradle.kts", "settings.gradle", "pom.xml",
    "README", "README.md", "LICENSE", "LICENSE.md", "CHANGELOG", "CHANGELOG.md",
    ".gitignore", ".gitattributes", ".dockerignore", ".editorconfig",
    ".env", ".env.local", ".env.production", ".env.development", ".env.test",
    ".bashrc", ".zshrc", ".profile", ".vimrc", ".tmux.conf", ".npmrc", ".prettierrc", ".eslintrc",
]);

export function isFilenameCandidate(text: string): boolean {
    if (!text) return false;
    // Relative-anchored paths (./foo, ../foo) are too noisy in terminal output
    // — explicit rule: never treat these as clickable files. Users who want
    // to open the file should drop the leading ./ or use an absolute path.
    if (/^\.{1,2}\//.test(text)) return false;
    const portStrip = text.replace(/(?::\d+){1,2}$/, "");
    if (!portStrip) return false;
    if (IPV4_RE.test(portStrip)) return false;
    const firstSeg = portStrip.split("/", 1)[0];
    if (/^\d+$/.test(firstSeg) && portStrip.indexOf(".") !== -1) return false;
    const hasSlash = portStrip.indexOf("/") !== -1;
    const basename = portStrip.split("/").pop() || "";
    // Always allow well-known extension-less names (Dockerfile, Makefile, ...).
    if (EXACT_BASENAMES.has(basename)) return true;
    // Otherwise require a whitelisted extension on the basename. This rules
    // out HTTP API paths (/api/agents/bind), kernel/proc paths (/proc/cpuinfo),
    // and ad-hoc tokens like /tmp/foo that aren't actually files we can open.
    const dotIdx = basename.lastIndexOf(".");
    if (dotIdx <= 0) return false;
    const ext = basename.slice(dotIdx + 1).toLowerCase();
    if (!ext || !FILE_EXTENSIONS.has(ext)) return false;
    // No slash + multiple dots in the basename → looks like a domain
    // (foo.bar.baz). Reject unless an exact basename matched above.
    if (!hasSlash) {
        const stem = basename.slice(0, dotIdx);
        if (stem.indexOf(".") !== -1) return false;
    }
    return true;
}

export type LinkKind = "local" | "url" | "file";

export interface ScannedMatch {
    start: number;
    end: number;
    uri: string;
    kind: LinkKind;
}

const TRAILING_PUNCT = new Set([
    ".", ",", ";", ":", "!", "?",
    ")", "]", "}", ">", "”", "’",
    // Opening brackets at the end of a URL almost always mean the URL was
    // followed by parenthetical text (e.g. `https://x.com/path(中文…)`).
    // Strip them so the URL doesn't include the dangling opener.
    "(", "[", "{", "“", "‘",
]);

export function trimMatchTrailing(text: string, start: number, end: number): number {
    let e = end;
    while (e > start && TRAILING_PUNCT.has(text[e - 1])) e -= 1;
    return e;
}

export function scanLinksOnText(text: string): ScannedMatch[] {
    const out: ScannedMatch[] = [];
    if (!text) return out;
    const claimed: boolean[] = new Array(text.length).fill(false);
    const claim = (s: number, e: number) => { for (let i = s; i < e; i += 1) claimed[i] = true; };
    const overlaps = (s: number, e: number) => { for (let i = s; i < e; i += 1) if (claimed[i]) return true; return false; };

    const runPass = (source: string, kind: LinkKind, filter?: (m: string) => boolean) => {
        const re = new RegExp(source, "g");
        let m: RegExpExecArray | null;
        while ((m = re.exec(text)) !== null) {
            if (m[0] === "") { re.lastIndex += 1; continue; }
            const rawStart = m.index;
            const rawEnd = rawStart + m[0].length;
            const end = trimMatchTrailing(text, rawStart, rawEnd);
            if (end <= rawStart) continue;
            if (overlaps(rawStart, end)) continue;
            const uri = text.slice(rawStart, end);
            if (filter && !filter(uri)) continue;
            claim(rawStart, end);
            out.push({ start: rawStart, end, uri, kind });
        }
    };

    runPass(LOCAL_PROTOCOL_RE.source, "local");
    runPass(URL_RE.source, "url");
    runPass(FILENAME_CANDIDATE_RE.source, "file", isFilenameCandidate);

    out.sort((a, b) => a.start - b.start);
    return out;
}
