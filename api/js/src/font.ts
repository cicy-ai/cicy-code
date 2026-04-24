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

export function monoFontStack(): string {
    if (isWindowsPlatform()) {
        return '"Cascadia Mono", "Cascadia Code", "Sarasa Mono SC", "Sarasa Term SC", Consolas, monospace';
    }
    return '"SF Mono", Menlo, Consolas, monospace';
}

export function applyMonoFontVar(doc: Document): string {
    var font = monoFontStack();
    if (doc && doc.documentElement) {
        doc.documentElement.style.setProperty("--cp-mono-font", font);
    }
    return font;
}
