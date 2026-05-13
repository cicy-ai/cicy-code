import sys
import os
import platform
import time
import threading
import base64
import tempfile
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import subprocess

PLATFORM = platform.system()  # 'Linux' or 'Darwin' or 'Windows'
BASE_PORT = 13430

# --- Mac 辅助函数 ---
def mac_get_ui_tree(app_name, max_depth=3):
    """获取指定应用的 UI 元素树（通过调用外部脚本）"""
    script_path = "/Users/ai/Desktop/get_ui_tree.py"
    python_path = "/usr/bin/python3"  # 系统 python3
    try:
        r = subprocess.run([python_path, script_path, app_name, str(max_depth)], capture_output=True, text=True, timeout=10)
        if not r.stdout:
            return {"error": f"no stdout, stderr: {r.stderr}, code: {r.returncode}"}
        try:
            return json.loads(r.stdout)
        except Exception as e:
            return {"error": f"json parse failed: {str(e)}, stdout: {r.stdout[:500]}"}
    except Exception as e:
        return {"error": f"subprocess failed: {str(e)}"}

# --- Windows 辅助函数 ---
def win_get_ui_tree(app_name, max_depth=3):
    """获取指定应用的 UI 元素树（Windows）"""
    script_path = "C:/Users/Administrator/Desktop/get_ui_tree.py"
    python_path = "python"
    try:
        r = subprocess.run([python_path, script_path, app_name, str(max_depth)], capture_output=True, text=True, timeout=10)
        if not r.stdout:
            return {"error": f"no stdout, stderr: {r.stderr}, code: {r.returncode}"}
        try:
            return json.loads(r.stdout)
        except Exception as e:
            return {"error": f"json parse failed: {str(e)}, stdout: {r.stdout[:500]}"}
    except Exception as e:
        return {"error": f"subprocess failed: {str(e)}"}

def mac_screenshot(app_name=None):
    """截图，返回压缩后的 JPEG bytes（<50KB）。app_name 指定则只截该窗口，否则全屏"""
    with tempfile.NamedTemporaryFile(suffix='.png', delete=False) as f:
        png_path = f.name
    jpg_path = png_path.replace('.png', '.jpg')
    try:
        if app_name:
            wid = _mac_get_window_id(app_name)
            if wid:
                subprocess.run(['screencapture', '-x', f'-l{wid}', png_path], timeout=5)
            else:
                subprocess.run(['screencapture', '-x', png_path], timeout=5)
        else:
            subprocess.run(['screencapture', '-x', png_path], timeout=5)
        
        if not os.path.exists(png_path) or os.path.getsize(png_path) == 0:
            return None
        
        # 用 sips 压缩：缩放到宽度 1200px，转 JPEG 质量 40
        subprocess.run(['sips', '-Z', '1200', '--setProperty', 'format', 'jpeg', '--setProperty', 'formatOptions', '40', png_path, '--out', jpg_path], timeout=10, capture_output=True)
        
        if os.path.exists(jpg_path) and os.path.getsize(jpg_path) > 0:
            with open(jpg_path, 'rb') as f:
                return f.read()
        return None
    finally:
        for p in [png_path, jpg_path]:
            if os.path.exists(p):
                os.unlink(p)

def _mac_get_window_id(app_name):
    """用 swift 通过 CGWindowListCopyWindowInfo 获取指定 app 的主窗口 id"""
    swift_code = f'''
import CoreGraphics
if let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] {{
    for w in list {{
        if let owner = w["kCGWindowOwnerName"] as? String, owner.lowercased().contains("{app_name.lower()}"),
           let bounds = w["kCGWindowBounds"] as? [String: Any],
           let width = bounds["Width"] as? Int, width > 50 {{
            print(w["kCGWindowNumber"]!)
            break
        }}
    }}
}}'''
    try:
        r = subprocess.run(['swift', '-e', swift_code], capture_output=True, text=True, timeout=10)
        wid = r.stdout.strip()
        return int(wid) if wid.isdigit() else None
    except:
        return None

def mac_list_windows():
    """列出所有可见窗口"""
    swift_code = '''
import CoreGraphics
import Foundation
var result: [[String: Any]] = []
if let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] {
    for w in list {
        guard let owner = w["kCGWindowOwnerName"] as? String,
              let bounds = w["kCGWindowBounds"] as? [String: Any],
              let width = bounds["Width"] as? Int, width > 50 else { continue }
        let d: [String: Any] = ["id": w["kCGWindowNumber"]!, "app": owner, "title": w["kCGWindowName"] ?? "", "bounds": bounds]
        if let data = try? JSONSerialization.data(withJSONObject: d), let s = String(data: data, encoding: .utf8) { print(s) }
    }
}'''
    try:
        r = subprocess.run(['swift', '-e', swift_code], capture_output=True, text=True, timeout=10)
        windows = []
        for line in r.stdout.strip().split('\n'):
            if line.strip():
                try: windows.append(json.loads(line))
                except: pass
        return windows
    except:
        return []

def mac_type_text(text):
    """Mac 上输入文字：写入剪贴板 + Cmd+V 粘贴"""
    subprocess.run(['pbcopy'], input=text.encode(), timeout=5)
    time.sleep(0.1)
    # 用 osascript 发送 Cmd+V
    subprocess.run(['osascript', '-e', 'tell application "System Events" to keystroke "v" using command down'], timeout=5)

def mac_open_app(app_name):
    """打开 Mac 应用"""
    subprocess.run(['open', '-a', app_name], timeout=10)

# --- Linux 辅助函数 ---
def linux_type_text(text, target=':0'):
    """Linux 上输入文字（原有逻辑）"""
    env = os.environ.copy()
    env['DISPLAY'] = target
    if 'HOME' not in env:
        env['HOME'] = os.path.expanduser('~')
    # 获取当前窗口名字
    result = subprocess.run(['xdotool', 'getactivewindow', 'getwindowname'], timeout=3, env=env, capture_output=True, text=True)
    winname = result.stdout.strip().lower() if result.stdout else ''
    is_terminal = any(x in winname for x in ['terminal', 'term', 'console', 'shell', 'bash', 'zsh', 'vscode', 'code'])

    # 复制到剪贴板
    subprocess.run(['bash', '-c', 'echo -n "$1" | xsel --clipboard --input', '_', text], timeout=5, env=env, capture_output=True)

    if is_terminal:
        print(f'[proxy] terminal/vscode detected: {winname}', flush=True)
        time.sleep(0.3)
        subprocess.run(['xdotool', 'getactivewindow', 'windowfocus'], timeout=5, env=env, capture_output=True)
        time.sleep(0.1)
        subprocess.run(['xdotool', 'key', '--clearmodifiers', 'ctrl+v'], timeout=5, env=env, capture_output=True)
        time.sleep(1)
        subprocess.run(['xdotool', 'key', 'Return'], timeout=5, env=env, capture_output=True)
    else:
        time.sleep(0.1)
        subprocess.run(['xdotool', 'getactivewindow', 'windowfocus'], timeout=5, env=env, capture_output=True)
        time.sleep(0.05)
        subprocess.run(['xdotool', 'key', '--clearmodifiers', 'ctrl+v'], timeout=5, env=env, capture_output=True)
        time.sleep(0.1)
        subprocess.run(['xdotool', 'key', 'Return'], timeout=5, env=env, capture_output=True)

class ProxyHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        try:
            if self.path == '/api/screenshot' or self.path.startswith('/api/screenshot?'):
                self._handle_screenshot_get()
            elif self.path == '/api/windows':
                self._handle_windows()
            elif self.path.startswith('/api/ui?'):
                self._handle_ui_tree()
            elif self.path.startswith('/api/chrome'):
                self._handle_chrome()
            elif self.path.startswith('/api/electron'):
                self._handle_electron()
            else:
                self.send_response(404)
                self.end_headers()
        except Exception as e:
            try: self._json_err(str(e))
            except: pass

    def do_POST(self):
        try:
            if self.path == '/api/type':
                self._handle_type()
            elif self.path == '/api/screenshot':
                self._handle_screenshot_post()
            elif self.path == '/api/open':
                self._handle_open()
            elif self.path == '/api/run_shell':
                self._handle_run_shell()
            else:
                self.send_response(404)
                self.end_headers()
        except Exception as e:
            try: self._json_err(str(e))
            except: pass

    def _handle_type(self):
        data = self._read_json()
        if data is None: return
        text = data.get('text', '').strip()
        target = data.get('target', ':0')
        print(f'[proxy] type "{text}" -> {target}', flush=True)
        try:
            if PLATFORM == 'Darwin':
                mac_type_text(text)
            else:
                linux_type_text(text, target)
            self._json_ok({'success': True})
        except Exception as e:
            self._json_err(str(e))

    def _handle_screenshot_get(self):
        """GET /api/screenshot?app=WeChat&loop=true — 返回 JPEG 图片或自动刷新页面"""
        from urllib.parse import urlparse, parse_qs
        qs = parse_qs(urlparse(self.path).query)
        app_name = qs.get('app', [None])[0]
        loop = qs.get('loop', ['false'])[0].lower() == 'true'
        
        if loop:
            # 返回自动刷新的 HTML
            url = f'/api/screenshot?app={app_name}' if app_name else '/api/screenshot'
            html = f'''<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Screenshot Loop</title></head>
<body style="margin:0;background:#000;display:flex;align-items:center;justify-content:center;height:100vh;">
<img id="img" style="max-width:100%;max-height:100vh;" src="{url}">
<script>setInterval(()=>{{document.getElementById('img').src='{url}?t='+Date.now()}}, 1000);</script>
</body></html>'''
            body = html.encode()
            self.send_response(200)
            self.send_header('Content-Type', 'text/html; charset=utf-8')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        
        try:
            if PLATFORM == 'Darwin':
                img = mac_screenshot(app_name)
            else:
                img = self._linux_screenshot()
            if img:
                self.send_response(200)
                self.send_header('Content-Type', 'image/jpeg' if PLATFORM == 'Darwin' else 'image/png')
                self.send_header('Content-Length', str(len(img)))
                self.end_headers()
                self.wfile.write(img)
            else:
                self._json_err('screenshot failed')
        except Exception as e:
            self._json_err(str(e))

    def _handle_screenshot_post(self):
        """POST /api/screenshot {app:"WeChat"} — 返回 base64"""
        data = self._read_json() or {}
        app_name = data.get('app')
        try:
            if PLATFORM == 'Darwin':
                img = mac_screenshot(app_name)
                fmt = 'jpeg'
            else:
                img = self._linux_screenshot()
                fmt = 'png'
            if img:
                self._json_ok({'success': True, 'image': base64.b64encode(img).decode(), 'format': fmt})
            else:
                self._json_err('screenshot failed')
        except Exception as e:
            self._json_err(str(e))

    def _handle_windows(self):
        """GET /api/windows — 列出可见窗口"""
        try:
            if PLATFORM == 'Darwin':
                self._json_ok({'windows': mac_list_windows()})
            else:
                self._json_ok({'windows': [], 'note': 'linux: not implemented'})
        except Exception as e:
            self._json_err(str(e))

    def _handle_ui_tree(self):
        """GET /api/ui?app=Chrome&depth=3 — 获取应用 UI 树"""
        from urllib.parse import urlparse, parse_qs
        qs = parse_qs(urlparse(self.path).query)
        app_name = qs.get('app', ['Chrome'])[0]
        depth = int(qs.get('depth', ['3'])[0])
        try:
            if PLATFORM == 'Darwin':
                tree = mac_get_ui_tree(app_name, depth)
            elif PLATFORM == 'Windows':
                tree = win_get_ui_tree(app_name, depth)
            else:
                self._json_err('only supported on macOS and Windows')
                return
            
            print(f"[DEBUG] tree type: {type(tree)}, has_error: {'error' in tree if isinstance(tree, dict) else 'N/A'}", flush=True)
            if tree and isinstance(tree, dict) and "error" not in tree:
                self._json_ok({'app': app_name, 'tree': tree})
            else:
                self._json_err(tree.get("error", "unknown error") if isinstance(tree, dict) else f"invalid result: {type(tree)}")
        except Exception as e:
            self._json_err(str(e))

    def _handle_open(self):
        """POST /api/open {app:"WeChat"}"""
        data = self._read_json()
        if data is None: return
        app_name = data.get('app', '')
        try:
            if PLATFORM == 'Darwin':
                mac_open_app(app_name)
            else:
                subprocess.Popen(['xdg-open', app_name] if '/' in app_name else [app_name],
                                 env={**os.environ, 'DISPLAY': data.get('target', ':0')})
            self._json_ok({'success': True})
        except Exception as e:
            self._json_err(str(e))

    def _handle_chrome(self):
        """GET /api/chrome?action=start|stop|restart|profiles&proxy=x&debug_port=x&profile=x"""
        from urllib.parse import urlparse, parse_qs
        qs = parse_qs(urlparse(self.path).query)
        action = qs.get('action', ['start'])[0]
        proxy = qs.get('proxy', [None])[0]
        debug_port = qs.get('debug_port', [None])[0]
        profile = qs.get('profile', [None])[0]
        script = os.path.expanduser('~/tools/chrome.sh')
        try:
            if action == 'profiles':
                state_file = os.path.expanduser('~/data/browser/chrome/Local State')
                state = json.loads(open(state_file).read())
                profiles = []
                for k, v in state.get('profile', {}).get('info_cache', {}).items():
                    profiles.append({'dir': k, 'name': v.get('name', ''), 'gaia_name': v.get('gaia_name', '')})
                self._json_ok({'profiles': profiles})
                return
            env = os.environ.copy()
            for k in ['http_proxy','https_proxy','HTTP_PROXY','HTTPS_PROXY']:
                env.pop(k, None)
            if debug_port: env['DEBUG_PORT'] = debug_port
            if profile: env['CHROME_PROFILE'] = profile
            if action == 'restart':
                subprocess.run([script, 'stop'], env=env, timeout=10)
                time.sleep(1)
                cmd = [script, 'start']
                if proxy: cmd.append('-p')
                subprocess.Popen(cmd, env=env)
                self._json_ok({'success': True, 'action': 'restart'})
            elif action == 'stop':
                r = subprocess.run([script, 'stop'], env=env, capture_output=True, text=True, timeout=10)
                self._json_ok({'success': True, 'action': 'stop', 'output': r.stdout})
            else:
                cmd = [script, 'start']
                if proxy: cmd.append('-p')
                subprocess.Popen(cmd, env=env)
                self._json_ok({'success': True, 'action': 'start'})
        except Exception as e:
            self._json_err(str(e))

    def _kill_electron(self):
        """强制杀掉所有 electron 相关进程（排除 xui 自身）"""
        if PLATFORM == 'Windows':
            subprocess.run('taskkill /F /IM electron.exe 2>nul', shell=True, timeout=10)
        else:
            my_pid = str(os.getpid())
            subprocess.run(f"ps aux | grep -i '[e]lectron' | grep -v xui | awk '{{print $2}}' | grep -v {my_pid} | xargs -r kill -9",
                           shell=True, timeout=10)

    def _handle_electron(self):
        """GET /api/electron?action=start|restart|kill&url=x&port=8101&proxy=x&no_cache=true"""
        from urllib.parse import urlparse, parse_qs
        qs = parse_qs(urlparse(self.path).query)
        action = qs.get('action', ['start'])[0]
        url = qs.get('url', [None])[0]
        port = qs.get('port', [None])[0]
        proxy = qs.get('proxy', [None])[0]
        no_cache = qs.get('no_cache', ['false'])[0].lower() == 'true'
        electron_dir = os.path.expanduser('~/projects/electron-mcp/main') if PLATFORM != 'Windows' else os.path.expanduser('~/projects/electron-mcp')
        npm_cmd = 'npm.cmd' if PLATFORM == 'Windows' else 'npm'
        def do():
            try:
                if action in ('kill', 'restart', 'start'):
                    self._kill_electron()
                    if action == 'kill':
                        return
                    time.sleep(2)
                env = os.environ.copy()
                if no_cache:
                    env['ELECTRON_DISABLE_HTTP_CACHE'] = '1'
                cmd = [npm_cmd, 'start', '--']
                if url: cmd += ['--url', url]
                if port: cmd += ['--port', port]
                if proxy: cmd += ['--proxy', proxy]
                subprocess.Popen(cmd, cwd=electron_dir, env=env)
            except Exception as e:
                print(f'[electron] error: {e}', flush=True)
        self._json_ok({'success': True, 'action': action})
        threading.Thread(target=do, daemon=True).start()

    def _handle_run_shell(self):
        """POST /api/run_shell {cmd:"ls -la", timeout:10}"""
        data = self._read_json()
        if data is None: return
        cmd = data.get('cmd', '')
        timeout = data.get('timeout', 30)
        try:
            r = subprocess.run(cmd, shell=True, capture_output=True, text=True, timeout=timeout, env=os.environ.copy())
            self._json_ok({'success': True, 'stdout': r.stdout, 'stderr': r.stderr, 'code': r.returncode})
        except subprocess.TimeoutExpired:
            self._json_err(f'timeout after {timeout}s')
        except Exception as e:
            self._json_err(str(e))

    def _linux_screenshot(self):
        """Linux 全屏截图"""
        with tempfile.NamedTemporaryFile(suffix='.png', delete=False) as f:
            path = f.name
        try:
            env = os.environ.copy()
            subprocess.run(['import', '-window', 'root', path], timeout=10, env=env)
            if os.path.exists(path) and os.path.getsize(path) > 0:
                with open(path, 'rb') as f:
                    return f.read()
            return None
        finally:
            if os.path.exists(path):
                os.unlink(path)

    def _read_json(self):
        try:
            length = int(self.headers.get('Content-Length', 0))
            return json.loads(self.rfile.read(length)) if length else {}
        except:
            self._json_err('invalid json')
            return None

    def _json_ok(self, data):
        body = json.dumps(data).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _json_err(self, msg, code=500):
        body = json.dumps({'success': False, 'error': msg}).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass

tk = None

class MeasureWindow:
    def __init__(self, display_num):
        self.display_num = display_num
        self.cmd_port = 8100 + display_num
        
        self.root = tk.Tk()
        self.root.title("")
        self.root.geometry("100x100+30+30")
        self.root.attributes("-topmost", True)
        self.root.overrideredirect(True)
        
        self.drag_x = 0
        self.drag_y = 0
        self.resize_w = 0
        self.resize_h = 0
        self.resize_x = 0
        self.resize_y = 0
        self.resize_corner = ""
        
        self.setup_ui()
        self.bind_events()
        self.root.update_idletasks()
        self.root.after(100, self.update_info)
        
    def setup_ui(self):
        self.main_frame = tk.Frame(self.root, bg="#333333", highlightthickness=1, highlightbackground="gray")
        self.main_frame.pack(fill="both", expand=True)
        
        style = {"bg": "#00ff00", "fg": "black", "font": ("Arial", 8, "bold")}
        h = 14
        
        self.lbl_tl = tk.Label(self.main_frame, **style)
        self.lbl_tl.place(relx=0, rely=0, anchor="nw", height=h)
        
        self.lbl_tr = tk.Label(self.main_frame, **style)
        self.lbl_tr.place(relx=1, rely=0, anchor="ne", height=h)
        
        self.lbl_bl = tk.Label(self.main_frame, **style)
        self.lbl_bl.place(relx=0, rely=1, anchor="sw", height=h)
        
        self.lbl_br = tk.Label(self.main_frame, **style)
        self.lbl_br.place(relx=1, rely=1, anchor="se", height=h)
        
        self.lbl_size = tk.Label(
            self.main_frame, 
            text="WxH", 
            bg="#333333", 
            fg="yellow", 
            font=("Arial", 10, "bold")
        )
        self.lbl_size.place(relx=0.5, rely=0.5, anchor="center")
        
    def update_info(self):
        w = self.root.winfo_width()
        h = self.root.winfo_height()
        x = self.root.winfo_x()
        y = self.root.winfo_y()
        
        self.lbl_tl.config(text=f"{x},{y}")
        self.lbl_tr.config(text=f"{x+w},{y}")
        self.lbl_bl.config(text=f"{x},{y+h}")
        self.lbl_br.config(text=f"{x+w},{y+h}")
        self.lbl_size.config(text=f"{w}x{h}")
        
    def bind_events(self):
        def start_drag(e):
            self.drag_x = e.x
            self.drag_y = e.y
        def do_drag(e):
            nx = self.root.winfo_x() + (e.x - self.drag_x)
            ny = self.root.winfo_y() + (e.y - self.drag_y)
            nx = max(0, nx)
            ny = max(0, ny)
            self.root.geometry(f"+{nx}+{ny}")
            self.update_info()
            
        self.main_frame.bind("<Button-1>", start_drag)
        self.main_frame.bind("<B1-Motion>", do_drag)
        self.lbl_size.bind("<Button-1>", start_drag)
        self.lbl_size.bind("<B1-Motion>", do_drag)
        
        def start_resize(corner):
            def handler(e):
                self.resize_w = self.root.winfo_width()
                self.resize_h = self.root.winfo_height()
                self.resize_x = e.x_root
                self.resize_y = e.y_root
                self.resize_corner = corner
            return handler
        def do_resize(e):
            corner = self.resize_corner
            nw = self.resize_w + (e.x_root - self.resize_x)
            nh = self.resize_h + (e.y_root - self.resize_y)
            nx = self.root.winfo_x()
            ny = self.root.winfo_y()
            
            if nw >= 50 and nh >= 50:
                if corner in ("tl", "bl"):
                    nx = max(0, self.root.winfo_x() - (nw - self.resize_w))
                if corner in ("tl", "tr"):
                    ny = max(0, self.root.winfo_y() - (nh - self.resize_h))
                self.root.geometry(f"{nw}x{nh}+{nx}+{ny}")
                self.update_info()
                
        self.lbl_tl.bind("<Button-1>", start_resize("tl"))
        self.lbl_tl.bind("<B1-Motion>", do_resize)
        self.lbl_tr.bind("<Button-1>", start_resize("tr"))
        self.lbl_tr.bind("<B1-Motion>", do_resize)
        self.lbl_bl.bind("<Button-1>", start_resize("bl"))
        self.lbl_bl.bind("<B1-Motion>", do_resize)
        self.lbl_br.bind("<Button-1>", start_resize("br"))
        self.lbl_br.bind("<B1-Motion>", do_resize)
        
    def update_positions(self):
        self.update_info()
        
    def run(self):
        self.root.mainloop()

def get_display():
    for i, arg in enumerate(sys.argv):
        if arg in ['--display', '-d'] and i + 1 < len(sys.argv):
            return sys.argv[i + 1]
        if arg.startswith('--display='):
            return arg.split('=')[1]
    return ':1'

def get_port():
    for i, arg in enumerate(sys.argv):
        if arg in ['--port', '-p'] and i + 1 < len(sys.argv):
            return int(sys.argv[i + 1])
        if arg.startswith('--port='):
            return int(arg.split('=')[1])
    return None

def start_proxy(port):
    while True:
        try:
            server = HTTPServer(('0.0.0.0', port), ProxyHandler)
            print(f'🚀 Proxy on :{port} ({PLATFORM})', flush=True)
            server.serve_forever()
        except Exception as e:
            print(f'⚠️ Proxy crashed: {e}, restarting in 2s...', flush=True)
            time.sleep(2)

def install_deps():
    """检查并安装依赖"""
    if PLATFORM == 'Linux':
        missing = []
        for pkg, cmd in [('xdotool','xdotool'), ('xsel','xsel'), ('imagemagick','import'), ('python3-tk','')]:
            if cmd and subprocess.run(['which', cmd], capture_output=True).returncode != 0:
                missing.append(pkg)
            elif pkg == 'python3-tk':
                try:
                    subprocess.run([sys.executable, '-c', 'import tkinter'], capture_output=True, check=True)
                except:
                    missing.append(pkg)
        if missing:
            print(f'📦 Installing: {" ".join(missing)}', flush=True)
            subprocess.run(['sudo', 'apt-get', 'install', '-y', '--no-install-recommends'] + missing, timeout=120)
    elif PLATFORM == 'Darwin':
        for name, cmd in [('screencapture','screencapture'), ('pbcopy','pbcopy'), ('osascript','osascript')]:
            if subprocess.run(['which', cmd], capture_output=True).returncode != 0:
                print(f'⚠️  Missing: {name} (should be built-in on macOS)', flush=True)
    elif PLATFORM == 'Windows':
        if subprocess.run(['where', 'python'], capture_output=True).returncode != 0:
            print('⚠️  Missing: python (install from python.org)', flush=True)

def kill_port(port):
    """杀掉占用指定端口的进程"""
    try:
        r = subprocess.run(['lsof', '-ti', f':{port}'], capture_output=True, text=True)
        pids = r.stdout.strip().split()
        for pid in pids:
            if pid and pid != str(os.getpid()):
                print(f'🔪 Killing PID {pid} on port {port}', flush=True)
                os.kill(int(pid), 9)
        if pids:
            time.sleep(0.5)
    except Exception:
        pass

if __name__ == "__main__":
    port_override = get_port()

    if PLATFORM == 'Darwin':
        port = port_override or BASE_PORT
        install_deps()
        kill_port(port)
        print(f'Starting Mac proxy on :{port}', flush=True)
        start_proxy(port)
    elif PLATFORM == 'Windows':
        port = port_override or BASE_PORT
        install_deps()
        kill_port(port)
        print(f'Starting Windows proxy on :{port}', flush=True)
        start_proxy(port)
    else:
        display = get_display()
        os.environ['DISPLAY'] = display
        if not os.environ.get('XAUTHORITY'):
            xauth_path = os.path.expanduser('~/.Xauthority')
            if os.path.exists(xauth_path):
                os.environ['XAUTHORITY'] = xauth_path
        num = int(display.split(':')[1] if ':' in display else '1')
        port = port_override or (BASE_PORT + num)
        install_deps()
        kill_port(port)
        # Import tkinter after DISPLAY and XAUTHORITY are set
        try:
            import tkinter
            tk = tkinter
        except Exception:
            tk = None
        print(f'Starting {display} -> port {port}')
        if tk is None:
            print('tkinter not available, running headless proxy only', flush=True)
            start_proxy(port)
        else:
            try:
                win = MeasureWindow(num)
            except Exception as e:
                print(f'tkinter UI unavailable ({e}), running headless proxy only', flush=True)
                start_proxy(port)
            else:
                threading.Thread(target=lambda: start_proxy(port), daemon=True).start()
                win.run()
