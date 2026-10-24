package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseKiroImportLine(t *testing.T) {
	cases := []struct {
		line                  string
		ok                    bool
		typ, email, pass, tfa string
	}{
		{"githhub:a@example.com:pw:ABCD EFGH", false, "", "", "", ""},
		{"github:a@example.com:pw:ABCDEFGH", true, "github", "a@example.com", "pw", "ABCDEFGH"},
		{"awsBuildId:b@example.com:@x:y:SEED", true, "builder_id", "b@example.com", "@x:y", "SEED"},
		{"google:c@example.com:only-pass", true, "google", "c@example.com", "only-pass", ""},
		{"# comment", false, "", "", "", ""},
		{"no-email-here:pw:x", false, "", "", "", ""},
	}
	// Typo'd type is still GitHub; spaces in the 2FA seed are dropped.
	cases[0].ok, cases[0].typ, cases[0].email, cases[0].pass, cases[0].tfa = true, "github", "a@example.com", "pw", "ABCDEFGH"
	for _, c := range cases {
		got, ok := parseKiroImportLine(c.line)
		if ok != c.ok || (ok && (got.LoginType != c.typ || got.Email != c.email || got.Password != c.pass || got.TwoFA != c.tfa)) {
			t.Errorf("%q → %+v ok=%v", c.line, got, ok)
		}
	}
}

func TestKiroAccountsImportListNeverLeaksSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	call := func(h http.HandlerFunc, method, url, body string) string {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, url, strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("%s %s → %d %s", method, url, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	out := call(handleKiroAccountImport, http.MethodPost, "/api/kiro/accounts/import", `{"text":"github:a@example.com:Pw1:JBSWY3DPEHPK3PXP\nawsBuildId:b@example.com:Pw2:JBSWY3DPEHPK3PXQ\nbad line"}`)
	if !strings.Contains(out, `"skipped_lines":[3]`) || strings.Contains(out, "Pw1") {
		t.Fatalf("import response: %s", out)
	}
	list := call(handleKiroAccounts, http.MethodGet, "/api/kiro/accounts", "")
	for _, secret := range []string{"Pw1", "Pw2", "JBSWY3DPEHPK3PXP"} {
		if strings.Contains(list, secret) {
			t.Fatalf("list leaked %q: %s", secret, list)
		}
	}
	if !strings.Contains(list, `"login_type":"builder_id"`) || !strings.Contains(list, `"2fa_set":true`) {
		t.Fatalf("list: %s", list)
	}
	// Re-import keeps the note set in the UI.
	call(handleKiroAccounts, http.MethodPut, "/api/kiro/accounts", `{"name":"a@example.com","old_name":"a@example.com","login_type":"github","email":"a@example.com","note":"主号"}`)
	call(handleKiroAccountImport, http.MethodPost, "/api/kiro/accounts/import", `{"text":"github:a@example.com:Pw9:JBSWY3DPEHPK3PXP"}`)
	accounts, _ := readKiroAccounts()
	if a := accounts["a@example.com"]; a.Note != "主号" || a.Password != "Pw9" {
		t.Fatalf("re-import: %+v", a)
	}
	if !strings.Contains(call(handleKiroAccountTOTP, http.MethodPost, "/api/kiro/accounts/totp", `{"name":"a@example.com"}`), "code") {
		t.Fatal("totp missing code")
	}
}
