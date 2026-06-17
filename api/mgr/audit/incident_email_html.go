package audit

import (
	"fmt"
	"html"
	"strings"
)

// renderIncidentEmailHTML renders the product-grade HTML body for an incident
// alert. Email-client-safe by construction: table layout, fully inline styles,
// no external resources. The brand mark is a unicode ✦ (the cicy four-point
// star) tinted brand-blue, so a logo shows in every client without an image
// (Gmail/Outlook strip <svg> and block remote/CID images by default). The plain
// renderIncidentEmail() output remains the text/plain alternative.
func renderIncidentEmailHTML(e Event, ruleIDs []string, ackURL string) string {
	top := topSeverity(e.Findings)
	sevColor := severityColor(top)
	sevText := strings.ToUpper(string(top))
	actText, actColor, actBg, actBorder := actionStyle(e.Decision.Action)
	esc := html.EscapeString

	agent := esc(e.Identity.AgentID)
	if e.Identity.AgentType != "" {
		agent += ` <span style="color:#94a3b8;font-weight:400;">(` + esc(e.Identity.AgentType) + `)</span>`
	}
	target := strings.TrimSpace(e.Subject.Provider)
	if e.Subject.Model != "" {
		if target != "" {
			target += " / "
		}
		target += e.Subject.Model
	}
	if target == "" {
		target = "—"
	}
	dir := directionLabel(e.Subject.Direction)
	headline := incidentHeadline(e.Decision.Action)

	// One-line human summary.
	topRuleName := ""
	if len(ruleIDs) > 0 {
		topRuleName = ruleIDs[0]
	}

	// Hit-rule cards.
	var rules strings.Builder
	for _, f := range e.Findings {
		preview, path := "", ""
		if len(f.Spans) > 0 {
			preview = f.Spans[0].Preview
			path = f.Spans[0].Path
		}
		fSev := strings.ToUpper(string(f.Severity))
		fColor := severityColor(f.Severity)
		rules.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="background:#fbfcfe;border:1px solid #e8ebf1;border-radius:10px;margin-bottom:8px;"><tr><td style="padding:14px 16px;">`)
		rules.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" width="100%"><tr><td style="vertical-align:middle;"><span style="color:#0f172a;font-size:14px;font-weight:600;">` + esc(f.RuleID) + `</span>`)
		rules.WriteString(`<span style="display:inline-block;margin-left:8px;background:` + fColor + `1a;color:` + fColor + `;font-size:11px;font-weight:700;padding:2px 8px;border-radius:999px;vertical-align:middle;">` + esc(fSev) + `</span></td>`)
		rules.WriteString(`<td style="text-align:right;color:#94a3b8;font-size:12px;">匹配 ` + fmt.Sprintf("%d", f.MatchCount) + ` 次</td></tr></table>`)
		if preview != "" || path != "" {
			rules.WriteString(`<div style="margin-top:9px;background:#0f172a;border-radius:8px;padding:10px 12px;">`)
			if path != "" {
				rules.WriteString(`<span style="color:#64748b;font-size:11px;font-family:ui-monospace,Menlo,monospace;">` + esc(path) + `</span><br>`)
			}
			if preview != "" {
				rules.WriteString(`<span style="color:#fca5a5;font-size:13px;font-family:ui-monospace,Menlo,Consolas,monospace;letter-spacing:.3px;">` + esc(preview) + `</span>`)
			}
			rules.WriteString(`</div>`)
		}
		rules.WriteString(`</td></tr></table>`)
	}

	// CTA — only when a public ack URL exists.
	cta := ""
	if ackURL != "" {
		cta = `<tr><td style="padding:22px 28px 6px 28px;">
		<table role="presentation" cellpadding="0" cellspacing="0" width="100%"><tr><td align="center" style="background:#2563eb;border-radius:10px;">
		<a href="` + esc(ackURL) + `" style="display:block;padding:14px 24px;color:#ffffff;font-size:15px;font-weight:700;text-decoration:none;letter-spacing:.3px;">✓ 我已确认并处置此告警</a>
		</td></tr></table>
		<p style="margin:10px 2px 0;color:#94a3b8;font-size:11px;text-align:center;">点击后此告警将在审计台标记为「已处理」,链接 30 天内有效。</p>
		</td></tr>`
	}

	row := func(k, v, bg string) string {
		return `<tr><td style="padding:11px 16px;background:` + bg + `;border-bottom:1px solid #eef0f4;width:96px;color:#64748b;font-size:12px;">` + k +
			`</td><td style="padding:11px 16px;background:` + bg + `;border-bottom:1px solid #eef0f4;color:#0f172a;font-size:13px;">` + v + `</td></tr>`
	}

	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>`)
	b.WriteString(`<body style="margin:0;padding:28px 12px;background:#eef1f6;font-family:-apple-system,'Segoe UI',Roboto,'PingFang SC','Microsoft YaHei',Arial,sans-serif;">`)
	b.WriteString(`<table role="presentation" align="center" cellpadding="0" cellspacing="0" width="600" style="width:600px;max-width:100%;margin:0 auto;background:#fff;border-radius:14px;overflow:hidden;box-shadow:0 8px 28px rgba(15,23,42,.10);">`)

	// Header with ✦ brand mark.
	b.WriteString(`<tr><td style="background:#0f172a;padding:20px 28px;"><table role="presentation" cellpadding="0" cellspacing="0" width="100%"><tr>`)
	b.WriteString(`<td style="vertical-align:middle;"><span style="font-size:22px;font-weight:800;background:linear-gradient(90deg,#60A5FA,#2563EB);-webkit-background-clip:text;-webkit-text-fill-color:transparent;color:#60A5FA;vertical-align:middle;">✦</span>`)
	b.WriteString(`<span style="color:#fff;font-size:16px;font-weight:700;letter-spacing:.2px;vertical-align:middle;margin-left:8px;">CiCy Code</span>`)
	b.WriteString(`<div style="color:#94a3b8;font-size:11px;font-weight:500;letter-spacing:1.5px;text-transform:uppercase;margin-top:3px;">Security Audit · 审计告警</div></td>`)
	b.WriteString(`<td style="text-align:right;vertical-align:middle;"><span style="display:inline-block;background:` + sevColor + `26;color:` + sevColor + `;font-size:11px;font-weight:700;padding:5px 11px;border-radius:999px;">● ` + sevText + `</span></td>`)
	b.WriteString(`</tr></table></td></tr>`)

	// Severity banner.
	b.WriteString(`<tr><td style="background:` + sevColor + `;padding:16px 28px;"><table role="presentation" cellpadding="0" cellspacing="0"><tr>`)
	b.WriteString(`<td style="vertical-align:middle;font-size:24px;line-height:1;">` + incidentEmoji(e.Decision.Action) + `</td>`)
	b.WriteString(`<td style="vertical-align:middle;padding-left:14px;"><div style="color:#fff;font-size:17px;font-weight:700;">` + headline + `</div>`)
	b.WriteString(`<div style="color:rgba(255,255,255,.85);font-size:13px;margin-top:3px;">级别 <b>` + sevText + `</b> · 规则 <b>` + esc(topRuleName) + `</b> · ` + dir + `</div></td></tr></table></td></tr>`)

	// Facts grid.
	b.WriteString(`<tr><td style="padding:18px 28px 4px 28px;"><table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="border:1px solid #eaecf1;border-radius:10px;border-collapse:separate;overflow:hidden;">`)
	b.WriteString(row("触发 Agent", agent, "#fafbfc"))
	b.WriteString(row("出站目标", esc(target), "#ffffff"))
	b.WriteString(row("命中动作", `<span style="display:inline-block;background:`+actBg+`;color:`+actColor+`;font-weight:700;font-size:12px;padding:3px 9px;border-radius:6px;border:1px solid `+actBorder+`;">`+actText+`</span>`, "#fafbfc"))
	b.WriteString(row("触发时间", esc(e.Timestamp), "#ffffff"))
	b.WriteString(`<tr><td style="padding:11px 16px;color:#64748b;font-size:12px;">事件 ID</td><td style="padding:11px 16px;color:#475569;font-size:12px;font-family:ui-monospace,Menlo,Consolas,monospace;">` + esc(e.ID) + `</td></tr>`)
	b.WriteString(`</table></td></tr>`)

	// Hit rules.
	if rules.Len() > 0 {
		b.WriteString(`<tr><td style="padding:20px 28px 4px 28px;"><div style="color:#0f172a;font-size:13px;font-weight:700;margin-bottom:10px;">命中规则</div>` + rules.String() + `</td></tr>`)
	}

	b.WriteString(cta)

	// Footer.
	b.WriteString(`<tr><td style="padding:18px 28px 24px;border-top:1px solid #eef0f4;"><span style="color:#94a3b8;font-size:11px;">由 <b style="color:#64748b;">cicy-code 审计</b> 自动发送 · 请勿直接回复</span></td></tr>`)
	b.WriteString(`</table></body></html>`)
	return b.String()
}

// severityColor maps a severity to its brand-consistent accent hex.
func severityColor(s Severity) string {
	switch s {
	case SeverityCritical:
		return "#dc2626"
	case SeverityHigh:
		return "#ea580c"
	case SeverityMedium:
		return "#d97706"
	default:
		return "#2563eb"
	}
}

// actionStyle returns (label, textColor, bg, border) for a decision action chip.
func actionStyle(a Action) (string, string, string, string) {
	switch a {
	case ActionBlock:
		return "已拦截并阻止发送", "#b91c1c", "#fef2f2", "#fecaca"
	case ActionNotify:
		return "已告警", "#1d4ed8", "#eff6ff", "#bfdbfe"
	default:
		return "已记录", "#475569", "#f1f5f9", "#e2e8f0"
	}
}

// incidentHeadline is the banner headline keyed off the action.
func incidentHeadline(a Action) string {
	switch a {
	case ActionBlock:
		return "敏感内容已被拦截,未发送给模型"
	default:
		return "检测到敏感内容"
	}
}

func incidentEmoji(a Action) string {
	switch a {
	case ActionBlock:
		return "⛔"
	default:
		return "⚠️"
	}
}

// directionLabel renders the audit direction in human terms.
func directionLabel(d string) string {
	switch d {
	case DirectionOutbound:
		return "出站方向(agent → 模型)"
	case DirectionInbound:
		return "入站方向(模型 → agent)"
	default:
		return "—"
	}
}
