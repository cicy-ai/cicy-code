import { test } from "node:test";
import assert from "node:assert/strict";
import { isFilenameCandidate, scanLinksOnText } from "../src/link_detect.ts";

// ── isFilenameCandidate ────────────────────────────────────────────────────

test("isFilenameCandidate: rejects IPv4 addresses", () => {
    assert.equal(isFilenameCandidate("192.168.1.1"), false);
    assert.equal(isFilenameCandidate("10.0.0.1"), false);
    assert.equal(isFilenameCandidate("127.0.0.1"), false);
    assert.equal(isFilenameCandidate("127.0.0.1:8080"), false);
    assert.equal(isFilenameCandidate("8.8.8.8"), false);
});

test("isFilenameCandidate: rejects naked domains", () => {
    assert.equal(isFilenameCandidate("google.com"), false);
    assert.equal(isFilenameCandidate("example.org"), false);
    assert.equal(isFilenameCandidate("example.org/path"), false);
    assert.equal(isFilenameCandidate("sub.domain.io"), false);
    assert.equal(isFilenameCandidate("a.b.c.d"), false);
});

test("isFilenameCandidate: accepts path-anchored paths regardless of extension", () => {
    assert.equal(isFilenameCandidate("./src/foo.ts"), true);
    assert.equal(isFilenameCandidate("./README"), true);
    assert.equal(isFilenameCandidate("../docs/api.md"), true);
    assert.equal(isFilenameCandidate("/etc/passwd"), true);
    assert.equal(isFilenameCandidate("/usr/local/bin/node"), true);
    assert.equal(isFilenameCandidate("~/.bashrc"), true);
    assert.equal(isFilenameCandidate("~/notes/log.txt"), true);
});

test("isFilenameCandidate: accepts :line and :line:col suffixes", () => {
    assert.equal(isFilenameCandidate("./src/foo.ts:42"), true);
    assert.equal(isFilenameCandidate("./src/foo.ts:42:7"), true);
    assert.equal(isFilenameCandidate("src/foo.ts:10"), true);
});

test("isFilenameCandidate: accepts bare filenames with whitelisted extension", () => {
    assert.equal(isFilenameCandidate("foo.ts"), true);
    assert.equal(isFilenameCandidate("src/foo.ts"), true);
    assert.equal(isFilenameCandidate("a/b/c.go"), true);
    assert.equal(isFilenameCandidate("Dockerfile.dev"), false); // .dev not whitelisted
    assert.equal(isFilenameCandidate("config.yaml"), true);
});

test("isFilenameCandidate: accepts known exact basenames", () => {
    assert.equal(isFilenameCandidate("Dockerfile"), true);
    assert.equal(isFilenameCandidate("Makefile"), true);
    assert.equal(isFilenameCandidate("package.json"), true);
    assert.equal(isFilenameCandidate("go.mod"), true);
    assert.equal(isFilenameCandidate("Cargo.lock"), true);
    assert.equal(isFilenameCandidate(".bashrc"), true);
});

test("isFilenameCandidate: rejects bare basename with unknown extension", () => {
    assert.equal(isFilenameCandidate("foo.unknownext"), false);
    assert.equal(isFilenameCandidate("file.com"), false); // .com not in whitelist
    assert.equal(isFilenameCandidate("server.1"), false);
});

test("isFilenameCandidate: rejects when first segment is numeric and value has dots", () => {
    assert.equal(isFilenameCandidate("192.168/foo.com"), false);
    assert.equal(isFilenameCandidate("1.2.3-rc1"), false);
});

// ── scanLinksOnText ────────────────────────────────────────────────────────

test("scanLinksOnText: detects http(s) URLs", () => {
    const r = scanLinksOnText("see https://example.com/path for info");
    assert.equal(r.length, 1);
    assert.equal(r[0].kind, "url");
    assert.equal(r[0].uri, "https://example.com/path");
});

test("scanLinksOnText: URL inside text does not also produce filename match", () => {
    const r = scanLinksOnText("open https://github.com/owner/repo/blob/main/src/foo.ts now");
    const uris = r.map(m => m.uri);
    const kinds = r.map(m => m.kind);
    assert.deepEqual(kinds, ["url"]);
    assert.equal(uris[0].startsWith("https://"), true);
});

test("scanLinksOnText: detects file:// protocol over filename", () => {
    const r = scanLinksOnText("file:///home/user/notes.md uploaded");
    const kinds = r.map(m => m.kind);
    assert.deepEqual(kinds, ["local"]);
});

test("scanLinksOnText: detects filenames in plain text", () => {
    const r = scanLinksOnText("modified ./src/components/Workspace.tsx:1359 and package.json");
    const uris = r.map(m => m.uri);
    assert.deepEqual(uris, ["./src/components/Workspace.tsx:1359", "package.json"]);
});

test("scanLinksOnText: does NOT match IPs / domains as files", () => {
    const r = scanLinksOnText("ping 192.168.1.1 and visit google.com today");
    assert.equal(r.length, 0);
});

test("scanLinksOnText: trims trailing punctuation", () => {
    const r = scanLinksOnText("see https://example.com/path, ok?");
    assert.equal(r.length, 1);
    assert.equal(r[0].uri, "https://example.com/path");
});

test("scanLinksOnText: empty / whitespace text", () => {
    assert.deepEqual(scanLinksOnText(""), []);
    assert.deepEqual(scanLinksOnText("   "), []);
});

test("scanLinksOnText: multiple links in one line preserve order", () => {
    const r = scanLinksOnText("a https://a.com/x b ./foo.ts c file:///tmp/y");
    assert.deepEqual(r.map(m => m.kind), ["url", "file", "local"]);
});
