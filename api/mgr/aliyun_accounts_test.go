package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The vector from Aliyun's own RPC signature documentation. If percent-encoding
// or parameter sorting ever drifts, every signed call would 400 at runtime —
// this catches it at build time.
func TestAliyunSignedQueryMatchesOfficialVector(t *testing.T) {
	query := aliyunSignedQuery("GET", "testid", "testsecret", map[string]string{
		"Action":         "DescribeRegions",
		"Format":         "XML",
		"SignatureNonce": "3ee8c1b8-83d3-44af-a94f-4e0ad82fd6cf",
		"Timestamp":      "2016-02-23T12:46:24Z",
		"Version":        "2014-05-26",
	})
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	if got := values.Get("Signature"); got != "OLeaidS1JvxuMvnyHOwuJ+uX5qY=" {
		t.Fatalf("signature=%q", got)
	}
	if !strings.HasPrefix(query, "AccessKeyId=testid&Action=DescribeRegions&Format=XML&SignatureMethod=HMAC-SHA1&") {
		t.Fatalf("params not canonically sorted: %s", query)
	}
	if !strings.Contains(query, "Timestamp=2016-02-23T12%3A46%3A24Z") {
		t.Fatalf("timestamp not encoded: %s", query)
	}
}

func TestAliyunPercentEncode(t *testing.T) {
	cases := map[string]string{
		"a b":  "a%20b",
		"a*b":  "a%2Ab",
		"a~b":  "a~b",
		"a/b":  "a%2Fb",
		"a:b":  "a%3Ab",
		"ab-1": "ab-1",
	}
	for in, want := range cases {
		if got := aliyunPercentEncode(in); got != want {
			t.Fatalf("encode(%q)=%q want %q", in, got, want)
		}
	}
}

func TestAliyunRegionDefault(t *testing.T) {
	if got := aliyunRegionOf(aliyunAccountConfig{}); got != aliyunDefaultRegion {
		t.Fatalf("region=%q", got)
	}
	if got := aliyunRegionOf(aliyunAccountConfig{Region: " cn-beijing "}); got != "cn-beijing" {
		t.Fatalf("region=%q", got)
	}
}

// Binding must upsert one profile and switch "current" to it, without dropping
// the profiles the user already configured with aliyun-cli.
func TestWriteAliyunCLIProfileUpserts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := `{"current":"other","profiles":[{"name":"other","mode":"AK","access_key_id":"keep"},{"name":"prod","mode":"AK","access_key_id":"old"}],"meta_path":"x"}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	account := aliyunAccountConfig{AccessKeyID: "LTAI-new", AccessKeySecret: "secret", Region: "cn-shanghai"}
	if err := writeAliyunCLIProfile(path, "prod", account); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"current": "prod"`, `"access_key_id": "LTAI-new"`, `"region_id": "cn-shanghai"`, `"access_key_id": "keep"`, `"meta_path": "x"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"old"`) {
		t.Fatalf("stale key survived:\n%s", got)
	}
	if strings.Count(got, `"name": "prod"`) != 1 {
		t.Fatalf("profile duplicated:\n%s", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}

func TestAliyunArnUserName(t *testing.T) {
	if got := aliyunArnUserName("acs:ram::1799:user/cicy-dev"); got != "cicy-dev" {
		t.Fatalf("user=%q", got)
	}
	if got := aliyunArnUserName("acs:ram::1799:root"); got != "" {
		t.Fatalf("root account should have no RAM user, got %q", got)
	}
}
