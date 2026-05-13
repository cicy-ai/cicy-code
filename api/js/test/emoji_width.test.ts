import { test } from "node:test";
import assert from "node:assert/strict";
import { Unicode11Addon } from "@xterm/addon-unicode11";

type WidthProvider = { version: string; wcwidth(n: number): number };

function getProvider(): WidthProvider {
    let provider: WidthProvider | undefined;
    const fakeTerminal = {
        unicode: {
            register(p: WidthProvider) { provider = p; },
        },
    };
    new Unicode11Addon().activate(fakeTerminal as any);
    if (!provider) throw new Error("Unicode11 addon did not register a provider");
    return provider;
}

test("Unicode11 width: ASCII is single-cell", () => {
    const p = getProvider();
    assert.equal(p.wcwidth(0x41), 1);  // A
    assert.equal(p.wcwidth(0x20), 1);  // space
});

test("Unicode11 width: ✅ is double-cell", () => {
    const p = getProvider();
    const cp = "✅".codePointAt(0)!;  // 0x2705
    assert.equal(p.wcwidth(cp), 2);
});

test("Unicode11 width: 👋 wave is double-cell", () => {
    const p = getProvider();
    const cp = "👋".codePointAt(0)!;  // 0x1f44b
    assert.equal(p.wcwidth(cp), 2);
});

test("Unicode11 width: CJK 你 is double-cell", () => {
    const p = getProvider();
    const cp = "你".codePointAt(0)!;  // 0x4f60
    assert.equal(p.wcwidth(cp), 2);
});

test("Unicode11 width: zero-width joiner is 0", () => {
    const p = getProvider();
    assert.equal(p.wcwidth(0x200d), 0);  // ZWJ
});

test("Unicode11 width: variation selector-16 is 0", () => {
    const p = getProvider();
    assert.equal(p.wcwidth(0xfe0f), 0);  // VS16 (emoji presentation)
});

test("Unicode11 width: skin tone modifier is reported", () => {
    const p = getProvider();
    // Skin tone modifiers U+1F3FB..U+1F3FF are wide in Unicode 11
    // (combined with preceding emoji they should not add cells beyond the base).
    const cp = 0x1f3fd;  // medium skin tone
    const w = p.wcwidth(cp);
    // Implementations vary — accept 0 (combining) or 2 (wide).
    assert.ok(w === 0 || w === 2, `expected 0 or 2, got ${w}`);
});
