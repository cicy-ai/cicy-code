(function(){const n=document.createElement("link").relList;if(n&&n.supports&&n.supports("modulepreload"))return;for(const o of document.querySelectorAll('link[rel="modulepreload"]'))s(o);new MutationObserver(o=>{for(const c of o)if(c.type==="childList")for(const r of c.addedNodes)r.tagName==="LINK"&&r.rel==="modulepreload"&&s(r)}).observe(document,{childList:!0,subtree:!0});function t(o){const c={};return o.integrity&&(c.integrity=o.integrity),o.referrerPolicy&&(c.referrerPolicy=o.referrerPolicy),o.crossOrigin==="use-credentials"?c.credentials="include":o.crossOrigin==="anonymous"?c.credentials="omit":c.credentials="same-origin",c}function s(o){if(o.ep)return;o.ep=!0;const c=t(o);fetch(o.href,c)}})();/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const b=([e,n,t])=>{const s=document.createElementNS("http://www.w3.org/2000/svg",e);return Object.keys(n).forEach(o=>{s.setAttribute(o,String(n[o]))}),t!=null&&t.length&&t.forEach(o=>{const c=b(o);s.appendChild(c)}),s};/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const L={xmlns:"http://www.w3.org/2000/svg",width:24,height:24,viewBox:"0 0 24 24",fill:"none",stroke:"currentColor","stroke-width":2,"stroke-linecap":"round","stroke-linejoin":"round"};/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const A=e=>Array.from(e.attributes).reduce((n,t)=>(n[t.name]=t.value,n),{}),M=e=>typeof e=="string"?e:!e||!e.class?"":e.class&&typeof e.class=="string"?e.class.split(" "):e.class&&Array.isArray(e.class)?e.class:"",I=e=>e.flatMap(M).map(t=>t.trim()).filter(Boolean).filter((t,s,o)=>o.indexOf(t)===s).join(" "),N=e=>e.replace(/(\w)(\w*)(_|-|\s*)/g,(n,t,s)=>t.toUpperCase()+s.toLowerCase()),w=(e,{nameAttr:n,icons:t,attrs:s})=>{var g;const o=e.getAttribute(n);if(o==null)return;const c=N(o),r=t[c];if(!r)return console.warn(`${e.outerHTML} icon name was not found in the provided icons object.`);const f=A(e),p={...L,"data-lucide":o,...s,...f},y=I(["lucide",`lucide-${o}`,f,s]);y&&Object.assign(p,{class:y});const v=b(["svg",p,r]);return(g=e.parentNode)==null?void 0:g.replaceChild(v,e)};/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const T=[["path",{d:"M20 6 9 17l-5-5"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const B=[["path",{d:"m9 18 6-6-6-6"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const O=[["rect",{width:"14",height:"14",x:"8",y:"8",rx:"2",ry:"2"}],["path",{d:"M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const S=[["circle",{cx:"12",cy:"12",r:"10"}],["circle",{cx:"12",cy:"12",r:"2"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const P=[["path",{d:"M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36-.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4"}],["path",{d:"M9 18c-4.51 2-5-2-7-2"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const j=[["circle",{cx:"12",cy:"12",r:"10"}],["path",{d:"M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"}],["path",{d:"M2 12h20"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const k=[["path",{d:"M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"}],["path",{d:"M19 10v2a7 7 0 0 1-14 0v-2"}],["line",{x1:"12",x2:"12",y1:"19",y2:"22"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const q=[["path",{d:"M4.037 4.688a.495.495 0 0 1 .651-.651l16 6.5a.5.5 0 0 1-.063.947l-6.124 1.58a2 2 0 0 0-1.438 1.435l-1.579 6.126a.5.5 0 0 1-.947.063z"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const z=[["path",{d:"M22 4s-.7 2.1-2 3.4c1.6 10-9.4 17.3-18 11.6 2.2.1 4.4-.6 6-2C3 15.5.5 9.6 3 5c2.2 2.6 5.6 4.1 9 4-.9-4.2 4-6.6 7-3.8 1.1 0 3-1.2 3-1.2z"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const R=[["path",{d:"M18 6 6 18"}],["path",{d:"m6 6 12 12"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const V=[["path",{d:"M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"}]];/**
 * @license lucide v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const X=({icons:e={},nameAttr:n="data-lucide",attrs:t={}}={})=>{if(!Object.values(e).length)throw new Error(`Please provide an icons object.
If you want to use all the icons you can import it like:
 \`import { createIcons, icons } from 'lucide';
lucide.createIcons({icons});\``);if(typeof document>"u")throw new Error("`createIcons()` only works in a browser environment.");const s=document.querySelectorAll(`[${n}]`);if(Array.from(s).forEach(o=>w(o,{nameAttr:n,icons:e,attrs:t})),n==="data-lucide"){const o=document.querySelectorAll("[icon-name]");o.length>0&&(console.warn("[Lucide] Some icons were found with the now deprecated icon-name attribute. These will still be replaced for backwards compatibility, but will no longer be supported in v1.0 and you should switch to data-lucide"),Array.from(o).forEach(c=>w(c,{nameAttr:"icon-name",icons:e,attrs:t})))}};X({icons:{Github:P,Twitter:z,Disc:S,ChevronRight:B,Mic:k,Zap:V,MousePointer2:q,Check:T,X:R,Globe:j,Copy:O}});const u=document.getElementById("lang-toggle");u==null||u.addEventListener("click",()=>{const e=document.documentElement.lang;document.documentElement.lang=e==="en"?"zh":"en"});const a=document.getElementById("toggle-monthly"),d=document.getElementById("toggle-yearly"),i=document.getElementById("toggle-bg"),x=document.querySelectorAll(".price-value"),E=document.querySelectorAll(".price-period"),C=document.querySelectorAll(".yearly-badge");a&&d&&i&&(a.addEventListener("click",()=>{a.classList.replace("text-white/60","text-white"),d.classList.replace("text-white","text-white/60"),i.style.transform="translateX(0)",i.style.width="100px",x.forEach(e=>{e.textContent=e.getAttribute("data-monthly")}),E.forEach(e=>{var n,t;e.classList.contains("en")?e.textContent=(n=e.textContent)!=null&&n.includes("seat")?"/month per seat":"/month":e.textContent=(t=e.textContent)!=null&&t.includes("席位")?"/月/席位":"/月"}),C.forEach(e=>e.classList.add("hidden"))}),d.addEventListener("click",()=>{d.classList.replace("text-white/60","text-white"),a.classList.replace("text-white","text-white/60"),i.style.transform="translateX(100px)",i.style.width="140px",x.forEach(e=>{e.textContent=e.getAttribute("data-yearly")}),E.forEach(e=>{var n,t;e.classList.contains("en")?e.textContent=(n=e.textContent)!=null&&n.includes("seat")?"/year per seat":"/year":e.textContent=(t=e.textContent)!=null&&t.includes("席位")?"/年/席位":"/年"}),C.forEach(e=>e.classList.remove("hidden"))}));const m=document.getElementById("typing-text");if(m){const e="帮我做一个能看比特币价格的工具";let n=0;const t=()=>{n<e.length?(m.textContent+=e[n],n++,setTimeout(t,150)):setTimeout(()=>{m.textContent="",n=0,t()},3e3)};t()}const h=document.getElementById("terminal-text"),l=document.getElementById("terminal-result");if(h&&l){const e="Create a weather dashboard...";let n=0;const t=()=>{n<e.length?(h.textContent+=e[n],n++,setTimeout(t,100)):(setTimeout(()=>{l.classList.remove("hidden"),l.classList.add("flex")},500),setTimeout(()=>{h.textContent="",l.classList.add("hidden"),l.classList.remove("flex"),n=0,t()},4e3))};t()}
