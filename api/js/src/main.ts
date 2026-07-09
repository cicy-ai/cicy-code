// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { Hterm } from "./hterm";
import { Xterm } from "./xterm";
import { Terminal, WebTTY, protocols } from "./webtty";
import { ConnectionFactory } from "./websocket";
import { mountCicyTTYUI } from "./cicy_ui";

// @TODO remove these
declare var gotty_auth_token: string;
declare var gotty_term: string;

const TTYD_BUNDLE_VERSION = "1.0.23-debug-ttyd-1";

const elem = document.getElementById("terminal")

if (elem !== null) {
    console.log("[ttyd-test] gotty bundle loaded", {
        path: window.location.pathname,
        ttyd_bundle_version: TTYD_BUNDLE_VERSION,
    });

    var term: Terminal;
    if (gotty_term == "hterm") {
        term = new Hterm(elem);
    } else {
        term = new Xterm(elem);
    }
    const httpsEnabled = window.location.protocol == "https:";
    // The terminal WS must carry the auth token IN ITS URL: the zero-trust gateway
    // authenticates the WS at the HTTP handshake, but gotty only sends its args
    // AFTER connecting (too late). Use this frame's ?token=, falling back to the
    // same-origin parent workspace's token when this frame was embedded without one
    // (the workspace opens as <tunnel>/?token= but the terminal iframe src omits it).
    var wsTok = "";
    var selfTokMatch = window.location.search.match(/[?&]token=([^&]+)/);
    if (selfTokMatch) {
        wsTok = selfTokMatch[1];
    } else {
        try {
            var parentSearch = (window.top && window.top !== window ? window.top.location.search : "") || (window.parent ? window.parent.location.search : "");
            var parentTokMatch = parentSearch.match(/[?&]token=([^&]+)/);
            if (parentTokMatch) wsTok = parentTokMatch[1];
        } catch (e) { /* cross-origin parent — leave empty */ }
    }
    const url = (httpsEnabled ? 'wss://' : 'ws://') + window.location.host + window.location.pathname + 'ws' + (wsTok ? '?token=' + wsTok : '');
    const args = window.location.search;
    const factory = new ConnectionFactory(url, protocols);
    const wt = new WebTTY(term, factory, args, gotty_auth_token);
    mountCicyTTYUI(term, wt);
    const closer = wt.open();

    window.addEventListener("unload", () => {
        closer();
        term.close();
    });
};
