package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAwsAccountsCRUDKeepsSecretAndNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	do := func(method, url, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleAwsAccounts(rec, req)
		return rec
	}
	if rec := do(http.MethodPut, "/api/aws/accounts", `{"name":"prod","access_key_id":"AKIAEXAMPLE","secret_access_key":"secretWXYZ","region":"ap-east-1","note":"生产账号"}`); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	rec := do(http.MethodGet, "/api/aws/accounts", "")
	if strings.Contains(rec.Body.String(), "secretWXYZ") {
		t.Fatalf("list leaked the secret: %s", rec.Body)
	}
	var list struct {
		Accounts []struct {
			Name       string `json:"name"`
			Region     string `json:"region"`
			Note       string `json:"note"`
			SecretTail string `json:"secret_tail"`
			SecretSet  bool   `json:"secret_set"`
		} `json:"accounts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Accounts) != 1 {
		t.Fatalf("want 1 account, got %s", rec.Body)
	}
	got := list.Accounts[0]
	if got.Note != "生产账号" || got.Region != "ap-east-1" || !got.SecretSet {
		t.Fatalf("unexpected item: %+v (%s)", got, rec.Body)
	}
	// Editing with a blank secret keeps the stored one; note can change.
	if rec := do(http.MethodPut, "/api/aws/accounts", `{"name":"prod","old_name":"prod","note":"新备注"}`); rec.Code != 200 {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	acc, err := awsAccountByName("prod")
	if err != nil || acc.SecretAccessKey != "secretWXYZ" || acc.Note != "新备注" {
		t.Fatalf("edit lost data: %+v %v", acc, err)
	}
	if info, _ := os.Stat(filepath.Join(os.Getenv("HOME"), "cicy-ai", "db", "aws.json")); info == nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("aws.json must be 0600")
	}
	if rec := do(http.MethodDelete, "/api/aws/accounts?name=prod", ""); rec.Code != 200 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestWriteAwsCLIProfilePreservesOtherProfiles(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "credentials")
	cfg := filepath.Join(dir, "config")
	_ = os.WriteFile(cred, []byte("[other]\naws_access_key_id = OLD\naws_secret_access_key = OLDS\n"), 0o600)
	acc := awsAccountConfig{AccessKeyID: "AKIA1", SecretAccessKey: "S1", Region: "eu-west-1"}
	if err := writeAwsCLIProfile(cred, cfg, "prod", acc); err != nil {
		t.Fatal(err)
	}
	// Re-binding must update in place, not duplicate sections.
	acc.AccessKeyID = "AKIA2"
	if err := writeAwsCLIProfile(cred, cfg, "prod", acc); err != nil {
		t.Fatal(err)
	}
	c, _ := os.ReadFile(cred)
	s := string(c)
	for _, want := range []string{"[other]", "aws_access_key_id = OLD", "[prod]", "[default]", "aws_access_key_id = AKIA2"} {
		if !strings.Contains(s, want) {
			t.Fatalf("credentials missing %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "[prod]") != 1 || strings.Contains(s, "AKIA1") {
		t.Fatalf("profile duplicated or stale:\n%s", s)
	}
	k, _ := os.ReadFile(cfg)
	if !strings.Contains(string(k), "[profile prod]") || !strings.Contains(string(k), "region = eu-west-1") {
		t.Fatalf("config wrong:\n%s", k)
	}
}

// AWS's documented SigV4 signing-key example (secret/date/region/service →
// key) guards the derivation used for STS GetCallerIdentity.
func TestAwsSigningKeyMatchesAwsExample(t *testing.T) {
	key := awsSigningKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20150830", "us-east-1", "iam")
	if got := hex.EncodeToString(key); got != "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9" {
		t.Fatalf("signing key = %s", got)
	}
}
