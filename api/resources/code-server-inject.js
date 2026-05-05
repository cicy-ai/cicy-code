if(navigator.serviceWorker){
  navigator.serviceWorker.getRegistrations().then(function(regs){
    regs.forEach(function(r){r.unregister()});
  });
}
(function(){
  var qs = new URLSearchParams(window.location.search || '');
  var token = String(qs.get('token') || '').trim();
  var folder = String(qs.get('folder') || '').trim();
  var pageClientId = String(qs.get('client_id') || '').trim();
  var pagePaneId = String(qs.get('page_pane') || '').trim();
  var menu = null;

  function closeNoise() {
    var t=setInterval(function(){
      try{
        var parts=document.querySelectorAll('.part.auxiliarybar,.part.sidebar.right');
        parts.forEach(function(p){
          if(p.offsetWidth>0){
            var btns=p.querySelectorAll('.codicon-close,.codicon-panel-close,.action-label');
            btns.forEach(function(b){b.click()});
          }
        });
        document.querySelectorAll('.tab').forEach(function(tab){
          var txt=tab.textContent||'';
          if(txt.includes('Welcome')||txt.includes('Getting Started')||txt.includes('Chat')){
            var close=tab.querySelector('.codicon-close');
            if(close)close.click();
          }
        });
      }catch(e){}
    },1000);
    setTimeout(function(){clearInterval(t)},15000);
  }

  function registerPageContext() {
    if (!token || !folder || !pageClientId || !pagePaneId) return;
    fetch('/api/code-server/page-context', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + token,
      },
      body: JSON.stringify({
        folder: folder,
        page_client_id: pageClientId,
        page_pane: pagePaneId,
      }),
    }).catch(function(){});
  }

  function ensureMenuStyle() {
    if (document.getElementById('cicy-code-server-menu-style')) return;
    var style = document.createElement('style');
    style.id = 'cicy-code-server-menu-style';
    style.textContent = '.cicy-code-server-menu{position:fixed;z-index:2147483647;min-width:180px;padding:6px;background:#111214;border:1px solid rgba(255,255,255,.08);border-radius:10px;box-shadow:0 18px 50px rgba(0,0,0,.45);font-family:ui-monospace,SFMono-Regular,Menlo,monospace}.cicy-code-server-menu button{width:100%;border:0;background:transparent;color:#f5f5f5;text-align:left;padding:8px 10px;border-radius:8px;cursor:pointer}.cicy-code-server-menu button:hover{background:rgba(255,255,255,.08)}';
    document.head.appendChild(style);
  }

  function getResourcePathFromNode(node) {
    var current = node;
    while (current && current !== document.body) {
      if (current.getAttribute) {
        var aria = current.getAttribute('aria-label') || '';
        if (aria && /\.[A-Za-z0-9_-]+$/.test(aria)) return aria;
        var title = current.getAttribute('title') || '';
        if (title && /\.[A-Za-z0-9_-]+$/.test(title)) return title;
        var dataResource = current.getAttribute('data-resource-name') || current.getAttribute('data-resource-path') || current.getAttribute('data-uri');
        if (dataResource) return dataResource;
      }
      current = current.parentElement;
    }
    return '';
  }

  function closeMenu() {
    if (menu && menu.parentNode) {
      menu.parentNode.removeChild(menu);
    }
    menu = null;
  }

  function sendPathToCurrentAgent(path) {
    if (!folder || !path) return;
    fetch('/api/code-server/send-path', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        folder: folder,
        path: path,
      }),
    }).catch(function(){});
  }

  function showMenu(x, y, path) {
    closeMenu();
    ensureMenuStyle();
    menu = document.createElement('div');
    menu.className = 'cicy-code-server-menu';
    menu.style.left = '24px';
    menu.style.top = y + 'px';
    var btn = document.createElement('button');
    btn.textContent = '发送给当前 agent';
    btn.addEventListener('click', function(ev){
      ev.preventDefault();
      ev.stopPropagation();
      closeMenu();
      sendPathToCurrentAgent(path);
    });
    menu.appendChild(btn);
    document.body.appendChild(menu);
    setTimeout(function(){
      document.addEventListener('pointerdown', closeMenu, { once: true, capture: true });
      document.addEventListener('keydown', function(e){ if (e.key === 'Escape') closeMenu(); }, { once: true, capture: true });
    }, 0);
  }

  function ensureContextAction() {
    document.addEventListener('contextmenu', function(event) {
      try {
        var path = getResourcePathFromNode(event.target);
        if (!path) {
          closeMenu();
          return;
        }
        event.preventDefault();
        event.stopPropagation();
        showMenu(event.clientX, event.clientY, path);
      } catch (e) {}
    }, true);
  }

  closeNoise();
  registerPageContext();
  ensureContextAction();
})();
