# Opening DevTools in QQ Mac (Electron App)

## QQ Information
- **Electron Version**: 34.0.0
- **Chrome Version**: 132.0.6834.83
- **Framework**: QQNT.framework
- **App Path**: `/Applications/QQ.app`

## Methods to Open DevTools

### Method 1: Keyboard Shortcut (Simplest)
```bash
ssh ssh_mac_ton 'osascript -e "tell application \"System Events\" to tell process \"QQ\" to keystroke \"i\" using {command down, option down}"'
```

### Method 2: Restart with Debug Flag
```bash
ssh ssh_mac_ton 'killall QQ; /Applications/QQ.app/Contents/MacOS/QQ --remote-debugging-port=9222 &'
```
Then access via browser: `http://localhost:9222`

### Method 3: Frida Hook (Advanced)

**Install Frida:**
```bash
ssh ssh_mac_ton 'pip3 install frida-tools'
```

**Hook Script** (`qq-devtools.js`):
```javascript
if (ObjC.available) {
    ObjC.schedule(ObjC.mainQueue, function() {
        var script = ObjC.classes.NSAppleScript.alloc().initWithSource_(
            "tell application \"System Events\"\n" +
            "  tell process \"QQ\"\n" +
            "    keystroke \"i\" using {command down, option down}\n" +
            "  end tell\n" +
            "end tell"
        );
        script.executeAndReturnError_(NULL);
    });
}
```

**Run:**
```bash
ssh ssh_mac_ton 'sudo ~/Library/Python/3.9/bin/frida -n QQ -l qq-devtools.js -q'
```

## Notes
- QQ may have disabled remote debugging port
- Keyboard shortcut is most reliable
- Requires accessibility permissions for System Events
- Downloaded Electron 34.0.0 binary available at: `/tmp/electron-v34.0.0/`

## Troubleshooting
If DevTools doesn't open:
1. Check if QQ has disabled DevTools in production build
2. Try modifying `application.asar` to enable DevTools
3. Use Electron's `--inspect` flag if available
