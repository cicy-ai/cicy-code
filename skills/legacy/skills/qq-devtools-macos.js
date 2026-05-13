// macOS Electron DevTools Hook - ARM64/x64
const is64bit = Process.arch === "x64" || Process.arch === "arm64";
const isARM = Process.arch === "arm64";

console.log(`[+] Architecture: ${Process.arch}`);

// Find QQNT framework
let qqntModule = null;
Process.enumerateModules().forEach(m => {
    if (m.name.indexOf("QQNT") !== -1) {
        qqntModule = m;
        console.log(`[+] Found QQNT: ${m.name} at ${m.base}`);
    }
});

if (!qqntModule) {
    console.log("[-] QQNT framework not found!");
    qqntModule = Process.enumerateModules()[0];
}

let webContentsPtr = null;
let devToolsOpened = false;

// Search for "openDevTools" string
function findOpenDevToolsSymbol() {
    const pattern = "6f 70 65 6e 44 65 76 54 6f 6f 6c 73"; // "openDevTools"
    console.log("[+] Searching for openDevTools string...");
    
    const matches = Memory.scanSync(qqntModule.base, qqntModule.size, pattern);
    console.log(`[+] Found ${matches.length} string matches`);
    
    if (matches.length > 0) {
        matches.forEach((m, i) => {
            console.log(`[+] Match ${i}: ${m.address}`);
        });
    }
    
    return matches;
}

// Hook C++ vtable methods
function hookWebContentsVTable() {
    console.log("[+] Attempting to hook WebContents methods...");
    
    // Search for WebContents class name
    const wcPattern = "57 65 62 43 6f 6e 74 65 6e 74 73"; // "WebContents"
    const wcMatches = Memory.scanSync(qqntModule.base, qqntModule.size, wcPattern);
    
    console.log(`[+] Found ${wcMatches.length} WebContents references`);
}

// Hook Objective-C methods
if (ObjC.available) {
    console.log("[+] Hooking ObjC methods...");
    
    // Find all WKWebView instances
    ObjC.schedule(ObjC.mainQueue, function() {
        const NSApp = ObjC.classes.NSApplication.sharedApplication();
        const windows = NSApp.windows();
        
        console.log(`[+] Found ${windows.count()} windows`);
        
        for (let i = 0; i < windows.count(); i++) {
            const win = windows.objectAtIndex_(i);
            const contentView = win.contentView();
            
            // Recursively find WKWebView
            function findWebView(view, depth) {
                if (depth > 10) return null;
                
                const className = view.$className;
                if (className && className.indexOf("WKWebView") !== -1) {
                    return view;
                }
                
                const subviews = view.subviews();
                for (let j = 0; j < subviews.count(); j++) {
                    const result = findWebView(subviews.objectAtIndex_(j), depth + 1);
                    if (result) return result;
                }
                return null;
            }
            
            const webView = findWebView(contentView, 0);
            if (webView) {
                console.log(`[+] Found WKWebView in window ${i}`);
                
                // Try to access _page or configuration
                try {
                    const config = webView.configuration();
                    const prefs = config.preferences();
                    console.log(`[+] WebView preferences: ${prefs}`);
                    
                    // Enable developer extras
                    if (prefs.respondsToSelector_("_setDeveloperExtrasEnabled:")) {
                        prefs._setDeveloperExtrasEnabled_(true);
                        console.log("[+] Developer extras enabled!");
                    }
                } catch (e) {
                    console.log(`[-] Error accessing WebView: ${e.message}`);
                }
            }
        }
    });
    
    // Hook NSWindow makeKeyAndOrderFront to catch new windows
    const NSWindow = ObjC.classes.NSWindow;
    const makeKey = NSWindow["- makeKeyAndOrderFront:"];
    
    Interceptor.attach(makeKey.implementation, {
        onEnter: function(args) {
            const win = new ObjC.Object(args[0]);
            console.log("[+] Window becoming key");
            
            ObjC.schedule(ObjC.mainQueue, function() {
                try {
                    const contentView = win.contentView();
                    contentView.evaluateJavaScript_completionHandler_(
                        "if(typeof require !== 'undefined') { try { require('electron').remote.getCurrentWebContents().openDevTools(); } catch(e) { console.log(e); } }",
                        NULL
                    );
                } catch(e) {}
            });
        }
    });
}

// Search and dump relevant patterns
findOpenDevToolsSymbol();
hookWebContentsVTable();

console.log("[+] ===== Hook installed =====");
console.log("[+] Try opening a new window or reloading QQ");
