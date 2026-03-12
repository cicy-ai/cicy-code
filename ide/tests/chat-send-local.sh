#!/bin/bash
# E2E: Chat local send flow
# Clear IndexedDB → reload → send Q → verify bubble appears
set -e

RPC="ELECTRON_MCP_NODE=1 curl-rpc exec_js id=2"
PASS="\033[32mPASS\033[0m"
FAIL="\033[31mFAIL\033[0m"

run() { eval "ELECTRON_MCP_NODE=1 curl-rpc exec_js id=2 code='$1'" 2>&1 | sed -n '2p'; }

echo "=== Chat Local Send E2E ==="

# 1. Count current bubbles
echo -n "[1] Count current bubbles... "
BEFORE=$(run 'document.querySelectorAll("[style*=\"linear-gradient\"]").length')
echo "$BEFORE"

# 2. Set textarea + click send
echo -n "[2] Set text & click send... "
run 'var ta=document.querySelector("textarea");ta.focus();var nset=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,"value").set;nset.call(ta,"Hello E2E test");ta.dispatchEvent(new Event("input",{bubbles:true}));setTimeout(function(){document.getElementById("chat-send-btn").click()},1000);"ok"'
sleep 2
echo "done"

# 3. Verify bubble count +1
echo -n "[3] Verify Q bubble... "
AFTER=$(run 'document.querySelectorAll("[style*=\"linear-gradient\"]").length')
EXPECT=$((BEFORE + 1))
if [ "$AFTER" = "$EXPECT" ]; then echo -e "$PASS ($BEFORE -> $AFTER)"; else echo -e "$FAIL (expected $EXPECT, got: $AFTER)"; exit 1; fi

# 4. Verify text content
echo -n "[4] Verify text content... "
R=$(run 'document.querySelector("[style*=\"linear-gradient\"]:last-of-type")?.closest("[style*=\"margin\"]")?.innerText?.includes("Hello E2E test")+""')
if [ "$R" = "true" ]; then echo -e "$PASS"; else echo -e "$FAIL (got: $R)"; exit 1; fi

# 5. Verify IndexedDB
echo -n "[5] Verify IndexedDB... "
R=$(run 'var r=indexedDB.open("cicy_chat_w-10001",1);r.onsuccess=function(){var tx=r.result.transaction("turns","readonly");var g=tx.objectStore("turns").getAll();g.onsuccess=function(){document.title="idb:"+g.result.length}};void 0')
sleep 1
R=$(run 'document.title')
if [[ "$R" == *"idb:"* ]]; then
  COUNT=$(echo "$R" | grep -oP 'idb:\K[0-9]+')
  if [ "$COUNT" -ge "1" ]; then echo -e "$PASS ($COUNT turns in DB)"; else echo -e "$FAIL (got: $R)"; exit 1; fi
else echo -e "$FAIL (got: $R)"; exit 1; fi

echo ""
echo "=== All tests passed ==="
