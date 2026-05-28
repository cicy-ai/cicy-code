package main

import (
	"fmt"
	"log"

	"ttyd-go/mgr/audit"
)

// Wire the audit pipeline's optional IM (WeChat) notification channel to the
// existing IM send path. SMTP stays the default; WeChat is additive and only
// fires when an account is bound (via the IM dashboard QR-scan flow).
// securityOfficerPaneID lives in audit_security_officer_worker.go (the file
// that owns the agent's identity + bootstrap).

func init() {
	audit.SetIMNotifier(auditWeChatNotify)
	audit.SetIMBoundCheck(auditWeChatBound)
	audit.SetSecurityOfficerNotifier(notifySecurityOfficerAgent)
}

// notifySecurityOfficerAgent delivers an incident escalation to the w-9501
// security-officer agent's pane (same cross-agent path cicy-agent msg uses).
func notifySecurityOfficerAgent(text string) error {
	return sendTextToPane(securityOfficerPaneID, text, true)
}

// connectedWeChatAccounts returns enabled, connected WeChat IM accounts.
func connectedWeChatAccounts() []*imAccount {
	accs, err := imListAccounts()
	if err != nil {
		return nil
	}
	out := make([]*imAccount, 0, len(accs))
	for _, a := range accs {
		if a != nil && a.Platform == imPlatformWeChat && a.Enabled && a.State == "connected" {
			out = append(out, a)
		}
	}
	return out
}

// auditWeChatBound reports whether at least one WeChat account is connected —
// drives the audit readiness "IM bound" flag.
func auditWeChatBound() bool {
	return len(connectedWeChatAccounts()) > 0
}

// auditWeChatNotify pushes an audit alert to every connected WeChat account's
// owner, reusing the IM transport. Additive to email — best-effort. Returns
// (delivered>0, firstError). A bound account with no known peer yet (the user
// hasn't messaged the bot since binding) yields an informative error.
func auditWeChatNotify(text string) (bool, error) {
	accs := connectedWeChatAccounts()
	if len(accs) == 0 {
		return false, nil
	}
	var firstErr error
	sent := 0
	for _, acc := range accs {
		peer := imPeerForAccount(acc)
		if peer.empty() {
			if firstErr == nil {
				firstErr = fmt.Errorf("wechat account %d has no known peer yet — message the bot once so audit alerts can reach you", acc.ID)
			}
			continue
		}
		tr, err := imBuildTransport(acc)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if _, err := imSendMessage(tr, acc.ID, peer, text); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sent++
	}
	if sent > 0 {
		log.Printf("[audit] wechat notify delivered to %d account(s)", sent)
	}
	return sent > 0, firstErr
}
