package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	ttydserver "ttyd-go/server"
)

var BuiltAppCDNPrefix string

var cosActiveFileNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type cosTencentConfig struct {
	SecretID  string
	SecretKey string
	Bucket    string
	Region    string
}

func loadTencentCOSConfig() cosTencentConfig {
	root := readGlobalJSONConfig()
	tencent := cfgMapValue(root, "tencent")
	return cosTencentConfig{
		SecretID:  cfgStringValue(tencent, "secret_id"),
		SecretKey: cfgStringValue(tencent, "secret_key"),
		Bucket:    cfgStringValue(tencent, "bucket"),
		Region:    cfgStringValue(tencent, "region"),
	}
}

func cdnVersionFromPrefix(prefix string) string {
	value := strings.TrimRight(strings.TrimSpace(prefix), "/")
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil {
		value = strings.TrimRight(parsed.Path, "/")
	}
	parts := strings.Split(value, "/")
	if len(parts) == 0 {
		return ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if strings.HasPrefix(strings.ToLower(last), "v") {
		return last
	}
	return ""
}

func cosActiveInstanceID() string {
	if value := strings.TrimSpace(os.Getenv("CICY_INSTANCE_KEY")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("CICY_INSTANCE_LABEL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("K_SERVICE")); value != "" {
		return value
	}
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown"
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8008"
	}
	return host + "-" + port
}

func cosActiveObjectKey(instanceID string) string {
	value := strings.TrimSpace(instanceID)
	if value == "" {
		value = "unknown"
	}
	value = cosActiveFileNameSanitizer.ReplaceAllString(value, "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		value = "unknown"
	}
	return "/runtime/active/" + value + ".json"
}

func cosAuthorization(secretID, secretKey, method, path string, now time.Time) string {
	unixNow := now.Unix()
	keyTime := fmt.Sprintf("%d;%d", unixNow, unixNow+3600)
	mac := hmac.New(sha1.New, []byte(secretKey))
	_, _ = mac.Write([]byte(keyTime))
	signKey := hex.EncodeToString(mac.Sum(nil))

	httpString := strings.ToLower(method) + "\n" + path + "\n\n\n"
	sha := sha1.Sum([]byte(httpString))
	stringToSign := "sha1\n" + keyTime + "\n" + hex.EncodeToString(sha[:]) + "\n"

	mac = hmac.New(sha1.New, []byte(signKey))
	_, _ = mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))

	return "q-sign-algorithm=sha1" +
		"&q-ak=" + url.QueryEscape(secretID) +
		"&q-sign-time=" + keyTime +
		"&q-key-time=" + keyTime +
		"&q-header-list=" +
		"&q-url-param-list=" +
		"&q-signature=" + signature
}

func putCOSActiveVersionFileOnce() error {
	appPrefix := strings.TrimRight(strings.TrimSpace(BuiltAppCDNPrefix), "/")
	ttydPrefix := strings.TrimRight(strings.TrimSpace(ttydserver.BuiltTTYDCDNPrefix), "/")
	if appPrefix == "" && ttydPrefix == "" {
		return nil
	}

	cfg := loadTencentCOSConfig()
	if cfg.SecretID == "" || cfg.SecretKey == "" || cfg.Bucket == "" || cfg.Region == "" {
		return nil
	}

	instanceID := cosActiveInstanceID()
	host, _ := os.Hostname()
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8008"
	}
	payload := M{
		"instance_id":      instanceID,
		"host":             strings.TrimSpace(host),
		"port":             port,
		"public_url":       strings.TrimSpace(os.Getenv("CICY_PUBLIC_URL")),
		"runtime_mode":     firstNonEmpty(strings.TrimSpace(os.Getenv("CICY_RUNTIME_MODE")), "local"),
		"app_cdn_prefix":   appPrefix,
		"app_version":      cdnVersionFromPrefix(appPrefix),
		"ttyd_cdn_prefix":  ttydPrefix,
		"ttyd_version":     cdnVersionFromPrefix(ttydPrefix),
		"updated_at":       time.Now().UTC().Format(time.RFC3339),
		"reported_region":  containerReportedRegionLabel(),
		"cicy_version":     version,
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')

	objectKey := cosActiveObjectKey(instanceID)
	hostName := fmt.Sprintf("%s.cos.%s.myqcloud.com", cfg.Bucket, cfg.Region)
	req, err := http.NewRequest(http.MethodPut, "https://"+hostName+objectKey, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Host", hostName)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", cosAuthorization(cfg.SecretID, cfg.SecretKey, http.MethodPut, objectKey, time.Now()))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cos put %s failed: %s", filepath.Base(objectKey), resp.Status)
	}
	return nil
}

func startCOSActiveVersionHeartbeat() {
	// The R2 prefixes are now baked into every build, so gate the heartbeat on
	// the runtime --cdn flag — only report active versions when actually serving
	// via CDN. Preserves the default (local) mode's no-heartbeat behavior.
	if !cdnMode {
		return
	}
	appPrefix := strings.TrimSpace(BuiltAppCDNPrefix)
	ttydPrefix := strings.TrimSpace(ttydserver.BuiltTTYDCDNPrefix)
	if appPrefix == "" && ttydPrefix == "" {
		return
	}
	if err := putCOSActiveVersionFileOnce(); err != nil {
		log.Printf("[cos-active] initial heartbeat error: %v", err)
	} else {
		log.Printf("[cos-active] heartbeat written app=%q ttyd=%q", cdnVersionFromPrefix(appPrefix), cdnVersionFromPrefix(ttydPrefix))
	}
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := putCOSActiveVersionFileOnce(); err != nil {
				log.Printf("[cos-active] heartbeat error: %v", err)
			}
		}
	}()
}
