import React from "react";
import { createRoot } from "react-dom/client";
import App from "./App.jsx";
import "./styles/tokens.css";
import "./styles/base.css";

// Tag the document with the host platform so platform-specific CSS rules
// can fire (e.g. only macOS gets the traffic-light spacer in the topbar).
const platform = (window.cicy && window.cicy.platform)
  || (() => {
    const ua = navigator.userAgent || "";
    if (/Windows/i.test(ua)) return "win32";
    if (/Mac/i.test(ua))     return "darwin";
    return "linux";
  })();
document.documentElement.dataset.platform = platform;

createRoot(document.getElementById("root")).render(<App />);
