// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// AWS account matrix — Access Key ID + Secret Access Key pairs kept in
// ~/cicy-ai/db/aws.json (0600). The list endpoint returns the Access Key ID
// (an identifier, not a secret) but never the secret: only whether it is set
// plus its last four characters, same contract as the aliyun matrix. Each
// account also carries a free-form `note`. "Bind" writes an AWS CLI profile
// into ~/.aws/credentials (+ region into ~/.aws/config) so `aws ...` on this
// host runs as that account; "test" calls STS GetCallerIdentity (SigV4).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type awsAccountConfig struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region"`
	Note            string `json:"note"`
}

const awsDefaultRegion = "us-east-1"

var awsAccountNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func readAwsAccounts() (map[string]awsAccountConfig, error) {
	return readAccountStore[awsAccountConfig]("aws.json")
}

func writeAwsAccounts(accounts map[string]awsAccountConfig) error {
	return writeAccountStore("aws.json", accounts)
}

func awsRegionOf(account awsAccountConfig) string {
	if region := strings.TrimSpace(account.Region); region != "" {
		return region
	}
	return awsDefaultRegion
}

func awsAccountByName(name string) (awsAccountConfig, error) {
	accounts, err := readAwsAccounts()
	if err != nil {
		return awsAccountConfig{}, err
	}
	account, ok := accounts[strings.TrimSpace(name)]
	if !ok {
		return awsAccountConfig{}, fmt.Errorf("aws account not found")
	}
	return account, nil
}

func handleAwsAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := readAwsAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_token")); name != "" {
			account, ok := accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "aws account not found")
				return
			}
			J(w, M{"access_key_id": account.AccessKeyID, "secret_access_key": account.SecretAccessKey, "region": account.Region, "note": account.Note})
			return
		}
		type item struct {
			Name        string `json:"name"`
			AccessKeyID string `json:"access_key_id"`
			SecretSet   bool   `json:"secret_set"`
			SecretTail  string `json:"secret_tail,omitempty"`
			Region      string `json:"region"`
			Note        string `json:"note"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{
				Name:        name,
				AccessKeyID: account.AccessKeyID,
				SecretSet:   strings.TrimSpace(account.SecretAccessKey) != "",
				SecretTail:  secretTail(account.SecretAccessKey),
				Region:      awsRegionOf(account),
				Note:        account.Note,
			})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Name            string `json:"name"`
			OldName         string `json:"old_name"`
			AccessKeyID     string `json:"access_key_id"`
			SecretAccessKey string `json:"secret_access_key"`
			Region          string `json:"region"`
			Note            string `json:"note"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.OldName = strings.TrimSpace(req.OldName)
		if !awsAccountNameRE.MatchString(req.Name) {
			httpErr(w, http.StatusBadRequest, "invalid aws account name")
			return
		}
		current := accounts[req.Name]
		if req.OldName != "" {
			if old, ok := accounts[req.OldName]; ok {
				current = old
				if req.OldName != req.Name {
					delete(accounts, req.OldName)
				}
			}
		}
		current.Region = strings.TrimSpace(req.Region)
		current.Note = strings.TrimSpace(req.Note)
		if strings.TrimSpace(req.AccessKeyID) != "" {
			current.AccessKeyID = strings.TrimSpace(req.AccessKeyID)
		}
		if strings.TrimSpace(req.SecretAccessKey) != "" {
			current.SecretAccessKey = strings.TrimSpace(req.SecretAccessKey)
		}
		if strings.TrimSpace(current.AccessKeyID) == "" || strings.TrimSpace(current.SecretAccessKey) == "" {
			httpErr(w, http.StatusBadRequest, "access_key_id and secret_access_key required")
			return
		}
		accounts[req.Name] = current
		if err := writeAwsAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "name": req.Name, "secret_set": true, "secret_tail": secretTail(current.SecretAccessKey)})
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := accounts[name]; !ok {
			httpErr(w, http.StatusNotFound, "aws account not found")
			return
		}
		delete(accounts, name)
		if err := writeAwsAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET, POST, PUT or DELETE required")
	}
}

// ─── SigV4 signing for STS GetCallerIdentity ──────────────────────────────
//
// GetCallerIdentity is the AWS whoami: any valid key can call it regardless of
// IAM policy, so it is the honest reachability test. We sign a minimal POST to
// the global STS endpoint rather than pull in the full AWS SDK.

func awsHmac(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func awsHashHex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// awsSigningKey derives the SigV4 signing key for a given date/region/service.
func awsSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := awsHmac([]byte("AWS4"+secret), dateStamp)
	kRegion := awsHmac(kDate, region)
	kService := awsHmac(kRegion, service)
	return awsHmac(kService, "aws4_request")
}

// awsCallerIdentity signs and sends STS GetCallerIdentity, returning the parsed
// identity or a human message when the key is rejected.
func awsCallerIdentity(account awsAccountConfig) (M, string, error) {
	const (
		service = "sts"
		host    = "sts.amazonaws.com"
		region  = "us-east-1" // STS global endpoint signs with us-east-1.
	)
	body := "Action=GetCallerIdentity&Version=2011-06-15"
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	payloadHash := awsHashHex(body)
	canonicalHeaders := fmt.Sprintf("content-type:application/x-www-form-urlencoded; charset=utf-8\nhost:%s\nx-amz-date:%s\n", host, amzDate)
	signedHeaders := "content-type;host;x-amz-date"
	canonicalRequest := strings.Join([]string{
		http.MethodPost, "/", "", canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, awsHashHex(canonicalRequest),
	}, "\n")

	signature := hex.EncodeToString(awsHmac(awsSigningKey(account.SecretAccessKey, dateStamp, region, service), stringToSign))
	authorization := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		account.AccessKeyID, scope, signedHeaders, signature,
	)

	req, err := http.NewRequest(http.MethodPost, "https://"+host+"/", strings.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Authorization", authorization)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// STS returns XML by default; parse the GetCallerIdentityResult.
		var parsed struct {
			Result struct {
				Account string `xml:"Account"`
				Arn     string `xml:"Arn"`
				UserID  string `xml:"UserId"`
			} `xml:"GetCallerIdentityResult"`
		}
		_ = xml.Unmarshal(raw, &parsed)
		return M{"account_id": parsed.Result.Account, "user_id": parsed.Result.UserID, "arn": parsed.Result.Arn}, "", nil
	}
	// Error body is XML: <ErrorResponse><Error><Code/><Message/></Error></ErrorResponse>.
	var apiErr struct {
		Error struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	_ = xml.Unmarshal(raw, &apiErr)
	message := strings.TrimSpace(apiErr.Error.Message)
	if message == "" {
		message = strings.TrimSpace(apiErr.Error.Code)
	}
	if message == "" {
		message = "AccessKey rejected"
	}
	return nil, message, nil
}

func handleAwsAccountTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := awsAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.SecretAccessKey) == "" {
		httpErr(w, http.StatusNotFound, "aws account not found")
		return
	}
	identity, message, err := awsCallerIdentity(account)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "AWS request failed")
		return
	}
	if identity == nil {
		httpErr(w, http.StatusUnauthorized, message)
		return
	}
	identity["success"] = true
	identity["region"] = awsRegionOf(account)
	J(w, identity)
}

// handleAwsAccountBind upserts an AWS CLI profile into ~/.aws/credentials and
// its region into ~/.aws/config, then points [default] at it, so `aws ...` on
// this host runs as that account.
func handleAwsAccountBind(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := awsAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.SecretAccessKey) == "" {
		httpErr(w, http.StatusNotFound, "aws account not found")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	profile := strings.TrimSpace(req.Name)
	credPath := filepath.Join(home, ".aws", "credentials")
	cfgPath := filepath.Join(home, ".aws", "config")
	if err := writeAwsCLIProfile(credPath, cfgPath, profile, account); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"success": true, "credentials": credPath, "config": cfgPath, "profile": profile, "region": awsRegionOf(account)})
}

// writeAwsCLIProfile rewrites the named profile in ~/.aws/credentials and the
// region in ~/.aws/config, and mirrors both into the [default] profile so a
// bare `aws` command (no --profile) uses this account. INI is edited in place
// so other profiles the user set up by hand are preserved.
func writeAwsCLIProfile(credPath, cfgPath, profile string, account awsAccountConfig) error {
	region := awsRegionOf(account)
	credSections := map[string]map[string]string{
		profile: {
			"aws_access_key_id":     account.AccessKeyID,
			"aws_secret_access_key": account.SecretAccessKey,
		},
		"default": {
			"aws_access_key_id":     account.AccessKeyID,
			"aws_secret_access_key": account.SecretAccessKey,
		},
	}
	if err := upsertINISections(credPath, credSections, nil); err != nil {
		return err
	}
	// ~/.aws/config uses "[profile name]" sections, except [default] is bare.
	cfgSections := map[string]map[string]string{
		"profile " + profile: {"region": region, "output": "json"},
		"default":            {"region": region, "output": "json"},
	}
	return upsertINISections(cfgPath, cfgSections, nil)
}

// upsertINISections edits an AWS-style INI file in place: for each section name
// it replaces the listed keys (adding the section if missing) while leaving
// every other section and key untouched. Written atomically at 0600 because
// ~/.aws/credentials holds secrets.
func upsertINISections(path string, sections map[string]map[string]string, _ any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return err
	}

	headerRE := regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
	existing := reindexINI(lines, headerRE)

	setKeyInSection := func(secLines []string, key, value string) []string {
		kvRE := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
		for i := 1; i < len(secLines); i++ {
			if kvRE.MatchString(secLines[i]) {
				secLines[i] = key + " = " + value
				return secLines
			}
		}
		return append(secLines, key+" = "+value)
	}

	// Rebuild section by section to keep edits localized.
	for name, kv := range sections {
		sp, ok := existing[name]
		if ok {
			sec := append([]string{}, lines[sp.start:sp.end]...)
			for key, value := range kv {
				sec = setKeyInSection(sec, key, value)
			}
			newLines := append([]string{}, lines[:sp.start]...)
			newLines = append(newLines, sec...)
			newLines = append(newLines, lines[sp.end:]...)
			lines = newLines
			// Re-index positions since lengths may have shifted.
			existing = reindexINI(lines, headerRE)
		} else {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "["+name+"]")
			for key, value := range kv {
				lines = append(lines, key+" = "+value)
			}
			existing = reindexINI(lines, headerRE)
		}
	}
	out := strings.Join(lines, "\n")
	return writeFileAtomic0600(path, []byte(strings.TrimLeft(out, "\n")+"\n"))
}

type iniSpan struct{ start, end int } // [start,end): header line up to the next header

func reindexINI(lines []string, headerRE *regexp.Regexp) map[string]iniSpan {
	existing := map[string]iniSpan{}
	curr := ""
	for i, line := range lines {
		if m := headerRE.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if curr != "" {
				s := existing[curr]
				s.end = i
				existing[curr] = s
			}
			existing[name] = iniSpan{start: i, end: len(lines)}
			curr = name
		}
	}
	return existing
}

// awsInspectResult is what one AccessKey can tell us about itself (STS whoami),
// so the matrix can be filled from a paste instead of hand-typed fields.
type awsInspectResult struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	Arn       string `json:"arn"`
	UserName  string `json:"user_name"`
	Region    string `json:"region"`
}

// awsArnName pulls the IAM user/role name out of an ARN such as
// "arn:aws:iam::123456789012:user/cicy-dev".
func awsArnName(arn string) string {
	if idx := strings.LastIndex(arn, "/"); idx >= 0 && idx+1 < len(arn) {
		return arn[idx+1:]
	}
	return ""
}

func handleAwsAccountInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		Region          string `json:"region"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	id := strings.TrimSpace(req.AccessKeyID)
	secret := strings.TrimSpace(req.SecretAccessKey)
	region := strings.TrimSpace(req.Region)
	if id == "" || secret == "" {
		account, err := awsAccountByName(req.Name)
		if err != nil || strings.TrimSpace(account.SecretAccessKey) == "" {
			httpErr(w, http.StatusNotFound, "aws account not found")
			return
		}
		if id == "" {
			id = account.AccessKeyID
		}
		if secret == "" {
			secret = account.SecretAccessKey
		}
		if region == "" {
			region = account.Region
		}
	}
	account := awsAccountConfig{AccessKeyID: id, SecretAccessKey: secret, Region: region}
	identity, message, err := awsCallerIdentity(account)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "AWS request failed")
		return
	}
	if identity == nil {
		httpErr(w, http.StatusUnauthorized, message)
		return
	}
	result := awsInspectResult{
		AccountID: fmt.Sprint(identity["account_id"]),
		UserID:    fmt.Sprint(identity["user_id"]),
		Arn:       fmt.Sprint(identity["arn"]),
		Region:    awsRegionOf(account),
	}
	result.UserName = awsArnName(result.Arn)
	J(w, result)
}
