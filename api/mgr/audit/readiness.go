// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

// ResponseReadiness reports whether the incident-response chain is actually
// wired end to end. A finding is useless if it can't reach anyone — this is
// what w-10000 checks on startup before claiming the box is protected.
type ResponseReadiness struct {
	IncidentEnabled      bool     `json:"incident_enabled"`
	ResponsiblePersons   bool     `json:"responsible_persons_configured"`
	MailerKind           string   `json:"mailer_kind"`      // "resend" | "gmail" | "smtp" | "file"
	MailDeliverable      bool     `json:"mail_deliverable"` // true for resend/gmail/smtp (file = spool to disk, nobody reads it)
	EmailFrom            string   `json:"email_from"`
	AIRemediationEnabled bool     `json:"ai_remediation_enabled"`
	PreventiveEnabled    bool     `json:"preventive_enabled"`
	IMBound              bool     `json:"im_bound"`
	Gaps                 []string `json:"gaps"` // human-readable, ready for w-10000 to relay verbatim
}

// Configured reports whether any responsible-person tier has at least one
// recipient. Empty = severe findings would reach nobody.
func (r ResponsiblePersonsConfig) Configured() bool {
	return len(r.Default) > 0 || len(r.BySeverity) > 0 || len(r.ByAgent) > 0 ||
		len(r.ByUser) > 0 || len(r.ByRule) > 0
}

// GetResponseReadiness snapshots the current response-chain configuration and
// flags the gaps. Safe before Init (returns a report whose only gap says the
// audit subsystem is not initialized).
func GetResponseReadiness() ResponseReadiness {
	r := ResponseReadiness{
		MailerKind: responseMailerKind,
		IMBound:    imChannelBound(), // WeChat (optional, additive) bound?
	}
	if globalPipeline == nil {
		r.Gaps = []string{"审计子系统未初始化"}
		return r
	}
	pol := globalPipeline.CurrentPolicy()
	cfg := pol.IncidentResponse
	r.IncidentEnabled = cfg.Enabled
	r.ResponsiblePersons = pol.ResponsiblePersons.Configured()
	r.EmailFrom = cfg.EmailFrom
	r.AIRemediationEnabled = cfg.AIRemediation.Enabled
	r.PreventiveEnabled = pol.Preventive.Enabled
	r.MailDeliverable = r.MailerKind == "smtp" || r.MailerKind == "gmail"

	if !r.IncidentEnabled {
		r.Gaps = append(r.Gaps, "事件响应总开关未开 (incident_response.enabled) — 命中也不会触发任何响应")
	}
	if !r.ResponsiblePersons {
		r.Gaps = append(r.Gaps, "未配置责任人 (responsible_persons) — 严重事件无人接收")
	}
	if !r.MailDeliverable {
		r.Gaps = append(r.Gaps, "邮件仅落盘、未真正投递 (需 db/smtp.json SMTP 或 db/google.json Gmail OAuth) — 责任人收不到")
	}
	if !r.PreventiveEnabled {
		r.Gaps = append(r.Gaps, "实时拦截未开 (preventive.enabled) — 只审计、不阻断正在发生的泄露")
	}
	if !r.AIRemediationEnabled {
		r.Gaps = append(r.Gaps, "AI 研判未开 (ai_remediation) — 无自动处置建议")
	}
	if !r.IMBound {
		r.Gaps = append(r.Gaps, "微信未绑定 — SMTP 仍默认发;在 IM 面板扫码绑微信可加一路实时告警(可选)")
	}
	return r
}
