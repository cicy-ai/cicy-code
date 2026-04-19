export function isWindowsPlatform(): boolean {
    var nav = window.navigator as Navigator & {
        userAgentData?: {
            platform?: string;
        };
    };
    var platform = "";
    if (nav.userAgentData && typeof nav.userAgentData.platform === "string") {
        platform = nav.userAgentData.platform;
    } else if (typeof nav.platform === "string") {
        platform = nav.platform;
    } else if (typeof nav.userAgent === "string") {
        platform = nav.userAgent;
    }
    return /win/i.test(platform);
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
