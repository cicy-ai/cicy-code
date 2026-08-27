// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

function getPlatformName(): string {
    var nav = window.navigator as Navigator & {
        userAgentData?: {
            platform?: string;
        };
    };
    if (nav.userAgentData && typeof nav.userAgentData.platform === "string") {
        return nav.userAgentData.platform;
    }
    if (typeof nav.platform === "string") {
        return nav.platform;
    }
    if (typeof nav.userAgent === "string") {
        return nav.userAgent;
    }
    return "";
}

export function isWindowsPlatform(): boolean {
    return /win/i.test(getPlatformName());
}

export function isMacPlatform(): boolean {
    return /(mac|iphone|ipad|ipod)/i.test(getPlatformName());
}

// Emoji fallback fonts so that wide emojis (✅ 👋 etc.) render at the full
// two-cell width xterm reserves for them. Without this, the system picks a
// 1-cell-wide fallback glyph and the selection highlight only covers half.
const EMOJI_FALLBACK = '"Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", "Noto Color Emoji", "EmojiOne Color"';

export function monoFontStack(): string {
    if (isWindowsPlatform()) {
        return `"JetBrains Mono Variable", "Cascadia Mono", "Cascadia Code", Consolas, "Sarasa Mono SC", "Sarasa Term SC", "Maple Mono NF CN", "Noto Sans Mono CJK SC", "Microsoft YaHei", ${EMOJI_FALLBACK}, monospace`;
    }
    return `"JetBrains Mono Variable", "SF Mono", Menlo, Consolas, "Sarasa Mono SC", "Noto Sans Mono CJK SC", "PingFang SC", "Noto Sans CJK SC", ${EMOJI_FALLBACK}, monospace`;
}

export function applyMonoFontVar(doc: Document): string {
    var font = monoFontStack();
    if (doc && doc.documentElement) {
        doc.documentElement.style.setProperty("--cp-mono-font", font);
    }
    return font;
}
