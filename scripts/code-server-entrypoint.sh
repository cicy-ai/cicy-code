#!/bin/sh
# Hook workbench.html if not already hooked
WB="/usr/lib/code-server/lib/vscode/out/vs/code/browser/workbench/workbench.html"
if ! grep -q "CICY-HOOK" "$WB" 2>/dev/null; then
  cat >> "$WB" << 'HOOK'
<!-- CICY-HOOK --><script>
(function(){
  var t=setInterval(function(){
    try{
      var btn=document.querySelector(".codicon-auxiliarybar-close");
      if(btn){btn.click();clearInterval(t);}
    }catch(e){}
  },500);
  setTimeout(function(){clearInterval(t)},15000);
})();
</script></html>
HOOK
fi
exec /usr/bin/entrypoint.sh "$@"
