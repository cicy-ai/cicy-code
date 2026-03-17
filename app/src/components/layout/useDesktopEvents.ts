import { useEffect } from 'react';
import { openInElectron } from '../desktop/useDesktopApps';

export default function useDesktopEvents(addApp: (app: any) => void) {
  useEffect(() => {
    const handler = async (e: CustomEvent) => {
      const d = e.detail || {};
      console.log('[AgentPage] desktop event:', d.type, d);

      if (d.type === 'ping') {
        window.dispatchEvent(new CustomEvent('agent-pong', { detail: { requestId: d.requestId, pong: 'ok' } }));
        return;
      }

      if (d.type === 'ipc_ping') {
        const rpc = (window as any).electronRPC;
        if (typeof rpc !== 'function') return;
        rpc('ping', {}).then((result: any) => {
          window.dispatchEvent(new CustomEvent('ipc-pong', { detail: { requestId: d.requestId, result } }));
        }).catch((err: any) => {
          window.dispatchEvent(new CustomEvent('ipc-pong', { detail: { requestId: d.requestId, error: err.message } }));
        });
        return;
      }

      if (d.type === 'add_app') {
        addApp({ id: d.id || `app-${Date.now()}`, type: d.widget ? 'widget' : 'icon', label: d.label || 'App', emoji: d.emoji || '📦', url: d.url || '', size: d.size, srcdoc: d.srcdoc });
        if (!d.widget && d.autoOpen !== false) openInElectron(d.url, d.label);
      } else if (d.type === 'open_window' && d.url) {
        openInElectron(d.url, d.title, true, d.width, d.height);
      } else if (d.type === 'gemini_ask') {
        const rpc = (window as any).electronRPC;
        if (typeof rpc !== 'function') return;
        const wid = d.win_id || 2;
        try {
          await rpc('gemini_web_set_prompt', { win_id: wid, text: d.prompt });
          await rpc('gemini_web_click_send', { win_id: wid });
          let result = '';
          for (let i = 0; i < 30; i++) {
            await new Promise(r => setTimeout(r, 1000));
            const status = await rpc('gemini_web_status', { win_id: wid });
            const s = JSON.parse(status?.content?.[0]?.text || '{}');
            if (!s.isGenerating && i > 2) {
              const reply = await rpc('exec_js', { win_id: wid, code: `(()=>{const els=document.querySelectorAll(".response-container");return els.length?els[els.length-1].innerText.trim():"no reply"})()` });
              result = reply?.content?.[0]?.text || (typeof reply === 'string' ? reply : JSON.stringify(reply));
              break;
            }
          }
          window.dispatchEvent(new CustomEvent('gemini-ask-result', { detail: { requestId: d.requestId, result } }));
        } catch (err: any) {
          window.dispatchEvent(new CustomEvent('gemini-ask-result', { detail: { requestId: d.requestId, error: err.message } }));
        }
      } else if (d.type === 'gemini_vision_request') {
        const rpc = (window as any).electronRPC;
        if (typeof rpc !== 'function') return;
        const wid = d.win_id || 4, srcWid = d.src_win_id || 1;
        try {
          await rpc('webpage_screenshot_to_clipboard', { win_id: srcWid });
          await rpc('exec_js', { win_id: wid, code: 'var r=document.querySelector("rich-textarea");if(r){var e=r.querySelector("div.ql-editor");if(e)e.click()};return "ok"' });
          await rpc('control_electron_WebContents', { win_id: wid, code: 'webContents.paste()' });
          for (let i = 0; i < 20; i++) { await new Promise(r => setTimeout(r, 500)); const st = await rpc('gemini_web_status', { win_id: wid }); const s = JSON.parse(st?.content?.[0]?.text || '{}'); if (s.hasImage && !s.isUploading) break; }
          await rpc('gemini_web_set_prompt', { win_id: wid, text: d.prompt || 'Describe this image' });
          await rpc('gemini_web_click_send', { win_id: wid });
          let result = '';
          for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); const st = await rpc('gemini_web_status', { win_id: wid }); const s = JSON.parse(st?.content?.[0]?.text || '{}'); if (!s.isGenerating && i > 8) { const reply = await rpc('exec_js', { win_id: wid, code: '(()=>{const els=document.querySelectorAll(".response-container");return els.length?els[els.length-1].innerText.trim():"no reply"})()' }); result = reply?.content?.[0]?.text || (typeof reply === 'string' ? reply : JSON.stringify(reply)); break; } }
          window.dispatchEvent(new CustomEvent('gemini-vision-result', { detail: { requestId: d.requestId, result } }));
        } catch (err: any) {
          window.dispatchEvent(new CustomEvent('gemini-vision-result', { detail: { requestId: d.requestId, error: err.message } }));
        }
      }
    };
    window.addEventListener('agent-desktop-event', handler as EventListener);
    return () => window.removeEventListener('agent-desktop-event', handler as EventListener);
  }, [addApp]);
}
