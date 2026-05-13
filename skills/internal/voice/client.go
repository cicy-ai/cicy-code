package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSpeechBaseURL = "https://speech.googleapis.com/v1"
	DefaultTTSBaseURL    = "https://texttospeech.googleapis.com/v1"
	defaultTokenURL      = "https://oauth2.googleapis.com/token"
	defaultTokenInfoURL  = "https://oauth2.googleapis.com/tokeninfo"
	cloudPlatformScope   = "https://www.googleapis.com/auth/cloud-platform"
)

type Client struct {
	HTTPClient    *http.Client
	SpeechBaseURL string
	TTSBaseURL    string
	TokenURL      string
	TokenInfoURL  string
	Credentials   Credentials
	VoicePrefPath string
}

type Credentials struct {
	ClientID           string
	ClientSecret       string
	RefreshToken       string
	QuotaProject       string
	ClientIDSource     string
	ClientSecretSource string
	RefreshTokenSource string
}

type TokenInfo struct {
	Scope      string `json:"scope"`
	ExpiresIn  string `json:"expires_in"`
	AccessType string `json:"access_type"`
	Audience   string `json:"aud"`
}

type HealthStatus struct {
	OK                    bool     `json:"ok"`
	Provider              string   `json:"provider"`
	ClientIDSource        string   `json:"client_id_source"`
	ClientSecretSource    string   `json:"client_secret_source"`
	RefreshTokenSource    string   `json:"refresh_token_source"`
	QuotaProject          string   `json:"quota_project,omitempty"`
	HasCloudPlatformScope bool     `json:"has_cloud_platform_scope"`
	Scopes                []string `json:"scopes"`
	NextStep              string   `json:"next_step,omitempty"`
}

type STTRequest struct {
	Filename                   string
	Audio                      []byte
	LanguageCode               string
	Encoding                   string
	SampleRateHertz            int
	Model                      string
	EnableAutomaticPunctuation bool
	MaxAlternatives            int
}

type STTResponse struct {
	Results []STTResult `json:"results"`
}

type STTResult struct {
	Alternatives []STTAlternative `json:"alternatives"`
	LanguageCode string           `json:"languageCode,omitempty"`
}

type STTAlternative struct {
	Transcript string  `json:"transcript"`
	Confidence float64 `json:"confidence,omitempty"`
}

type TTSRequest struct {
	Text          string
	SSML          string
	LanguageCode  string
	VoiceName     string
	AudioEncoding string
	SpeakingRate  float64
	Pitch         float64
}

type VoicePreference struct {
	LanguageCode string `json:"language_code"`
	VoiceName    string `json:"voice_name"`
}

type TTSVoice struct {
	LanguageCodes    []string `json:"languageCodes"`
	Name             string   `json:"name"`
	SsmlGender       string   `json:"ssmlGender"`
	NaturalSampleHtz int      `json:"naturalSampleRateHertz"`
}

type scopeError struct {
	status HealthStatus
}

func (e *scopeError) Error() string {
	message := "google cloud token is missing required scope https://www.googleapis.com/auth/cloud-platform"
	if len(e.status.Scopes) > 0 {
		message += fmt.Sprintf("; current scopes: %s", strings.Join(e.status.Scopes, ", "))
	}
	if e.status.NextStep != "" {
		message += "; " + e.status.NextStep
	}
	return message
}

func NewClient(timeout time.Duration) (*Client, error) {
	creds, err := loadCredentials()
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{}
	if timeout > 0 {
		httpClient.Timeout = timeout
	}
	return &Client{
		HTTPClient:    httpClient,
		SpeechBaseURL: DefaultSpeechBaseURL,
		TTSBaseURL:    DefaultTTSBaseURL,
		TokenURL:      defaultTokenURL,
		TokenInfoURL:  defaultTokenInfoURL,
		Credentials:   creds,
		VoicePrefPath: defaultVoicePrefPath(),
	}, nil
}

func (c *Client) STTHealth(ctx context.Context) (HealthStatus, error) {
	status, _, err := c.authorize(ctx, "google-speech-to-text", false)
	if err != nil {
		return status, err
	}
	if !status.HasCloudPlatformScope {
		status.OK = false
		status.NextStep = "generate GOOGLE_CLOUD_REFRESH_TOKEN or re-authorize GMAIL_REFRESH_TOKEN with https://www.googleapis.com/auth/cloud-platform"
	}
	return status, nil
}

func (c *Client) TTSHealth(ctx context.Context) (HealthStatus, error) {
	status, token, err := c.authorize(ctx, "google-text-to-speech", false)
	if err != nil {
		return status, err
	}
	if !status.HasCloudPlatformScope {
		status.OK = false
		status.NextStep = "generate GOOGLE_CLOUD_REFRESH_TOKEN or re-authorize GMAIL_REFRESH_TOKEN with https://www.googleapis.com/auth/cloud-platform"
		return status, nil
	}
	_, err = c.doJSON(ctx, http.MethodGet, strings.TrimRight(c.TTSBaseURL, "/")+"/voices?languageCode=en-US", token, nil)
	if err != nil {
		return status, err
	}
	return status, nil
}

func (c *Client) STT(ctx context.Context, req STTRequest) (STTResponse, error) {
	status, token, err := c.authorize(ctx, "google-speech-to-text", true)
	if err != nil {
		return STTResponse{}, err
	}
	_ = status

	if len(req.Audio) == 0 {
		return STTResponse{}, fmt.Errorf("audio is required")
	}
	req.LanguageCode = strings.TrimSpace(req.LanguageCode)
	if req.LanguageCode == "" {
		req.LanguageCode = "en-US"
	}
	req.Encoding = strings.TrimSpace(req.Encoding)
	if req.Encoding == "" {
		req.Encoding = DetectEncoding(req.Filename)
	}
	if req.Encoding == "" {
		return STTResponse{}, fmt.Errorf("cannot detect audio encoding for %q; pass --encoding", req.Filename)
	}
	if req.SampleRateHertz == 0 {
		req.SampleRateHertz = GuessSampleRate(req.Filename, req.Audio, req.Encoding)
	}

	config := map[string]any{
		"languageCode": req.LanguageCode,
		"encoding":     req.Encoding,
	}
	if req.SampleRateHertz > 0 {
		config["sampleRateHertz"] = req.SampleRateHertz
	}
	if req.Model != "" {
		config["model"] = req.Model
	}
	if req.MaxAlternatives > 0 {
		config["maxAlternatives"] = req.MaxAlternatives
	}
	config["enableAutomaticPunctuation"] = req.EnableAutomaticPunctuation

	payload := map[string]any{
		"config": config,
		"audio": map[string]string{
			"content": base64.StdEncoding.EncodeToString(req.Audio),
		},
	}

	body, err := c.doJSON(ctx, http.MethodPost, strings.TrimRight(c.SpeechBaseURL, "/")+"/speech:recognize", token, payload)
	if err != nil {
		return STTResponse{}, err
	}

	var out STTResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return STTResponse{}, fmt.Errorf("decode google speech response: %w", err)
	}
	return out, nil
}

func (c *Client) TTS(ctx context.Context, req TTSRequest) ([]byte, error) {
	_, token, err := c.authorize(ctx, "google-text-to-speech", true)
	if err != nil {
		return nil, err
	}

	text := strings.TrimSpace(req.Text)
	ssml := strings.TrimSpace(req.SSML)
	if text == "" && ssml == "" {
		return nil, fmt.Errorf("text or ssml is required")
	}

	languageCode := strings.TrimSpace(req.LanguageCode)
	voiceName := strings.TrimSpace(req.VoiceName)
	if languageCode == "" && voiceName != "" {
		languageCode = DeriveLanguageCode(voiceName)
	}
	if languageCode == "" {
		languageCode = "en-US"
	}

	audioEncoding := strings.TrimSpace(req.AudioEncoding)
	if audioEncoding == "" {
		audioEncoding = "MP3"
	}

	input := map[string]string{}
	if ssml != "" {
		input["ssml"] = ssml
	} else {
		input["text"] = text
	}
	voiceConfig := map[string]string{
		"languageCode": languageCode,
	}
	if voiceName != "" {
		voiceConfig["name"] = voiceName
	}
	audioConfig := map[string]any{
		"audioEncoding": audioEncoding,
	}
	if req.SpeakingRate != 0 {
		audioConfig["speakingRate"] = req.SpeakingRate
	}
	if req.Pitch != 0 {
		audioConfig["pitch"] = req.Pitch
	}

	payload := map[string]any{
		"input":       input,
		"voice":       voiceConfig,
		"audioConfig": audioConfig,
	}

	body, err := c.doJSON(ctx, http.MethodPost, strings.TrimRight(c.TTSBaseURL, "/")+"/text:synthesize", token, payload)
	if err != nil {
		return nil, err
	}

	var out struct {
		AudioContent string `json:"audioContent"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode google tts response: %w", err)
	}
	if out.AudioContent == "" {
		return nil, fmt.Errorf("google tts returned empty audioContent")
	}
	return base64.StdEncoding.DecodeString(out.AudioContent)
}

func (c *Client) ListVoices(ctx context.Context, languageCode string) ([]TTSVoice, error) {
	_, token, err := c.authorize(ctx, "google-text-to-speech", true)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.TTSBaseURL, "/") + "/voices"
	if languageCode = strings.TrimSpace(languageCode); languageCode != "" {
		endpoint += "?languageCode=" + url.QueryEscape(languageCode)
	}
	body, err := c.doJSON(ctx, http.MethodGet, endpoint, token, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Voices []TTSVoice `json:"voices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode google voices response: %w", err)
	}
	return out.Voices, nil
}

func (c *Client) GetVoicePreference() (VoicePreference, error) {
	data, err := os.ReadFile(c.VoicePrefPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return VoicePreference{}, nil
		}
		return VoicePreference{}, err
	}
	var pref VoicePreference
	if err := json.Unmarshal(data, &pref); err != nil {
		return VoicePreference{}, err
	}
	return pref, nil
}

func (c *Client) SetVoicePreference(pref VoicePreference) error {
	pref.LanguageCode = strings.TrimSpace(pref.LanguageCode)
	pref.VoiceName = strings.TrimSpace(pref.VoiceName)
	if pref.VoiceName == "" {
		return fmt.Errorf("voice name is required")
	}
	if pref.LanguageCode == "" {
		pref.LanguageCode = DeriveLanguageCode(pref.VoiceName)
	}
	if pref.LanguageCode == "" {
		return fmt.Errorf("language code is required; pass --language or use a standard Google voice name")
	}
	if err := os.MkdirAll(filepath.Dir(c.VoicePrefPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pref, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(c.VoicePrefPath, data, 0o644)
}

func (c *Client) authorize(ctx context.Context, provider string, requireScope bool) (HealthStatus, string, error) {
	token, err := c.fetchAccessToken(ctx)
	if err != nil {
		return HealthStatus{}, "", err
	}
	info, err := c.fetchTokenInfo(ctx, token)
	if err != nil {
		return HealthStatus{}, "", err
	}
	scopes := splitScopes(info.Scope)
	status := HealthStatus{
		OK:                    true,
		Provider:              provider,
		ClientIDSource:        c.Credentials.ClientIDSource,
		ClientSecretSource:    c.Credentials.ClientSecretSource,
		RefreshTokenSource:    c.Credentials.RefreshTokenSource,
		QuotaProject:          c.Credentials.QuotaProject,
		HasCloudPlatformScope: hasScope(scopes, cloudPlatformScope),
		Scopes:                scopes,
	}
	if requireScope && !status.HasCloudPlatformScope {
		status.OK = false
		status.NextStep = "generate GOOGLE_CLOUD_REFRESH_TOKEN or re-authorize GMAIL_REFRESH_TOKEN with https://www.googleapis.com/auth/cloud-platform"
		return status, "", &scopeError{status: status}
	}
	return status, token, nil
}

func (c *Client) fetchAccessToken(ctx context.Context) (string, error) {
	values := url.Values{
		"client_id":     {c.Credentials.ClientID},
		"client_secret": {c.Credentials.ClientSecret},
		"refresh_token": {c.Credentials.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("google oauth token exchange failed: %s", strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("google oauth token exchange returned empty access_token")
	}
	return out.AccessToken, nil
}

func (c *Client) fetchTokenInfo(ctx context.Context, accessToken string) (TokenInfo, error) {
	endpoint := c.TokenInfoURL + "?access_token=" + url.QueryEscape(accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return TokenInfo{}, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return TokenInfo{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TokenInfo{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return TokenInfo{}, fmt.Errorf("google tokeninfo failed: %s", strings.TrimSpace(string(body)))
	}
	var out TokenInfo
	if err := json.Unmarshal(body, &out); err != nil {
		return TokenInfo{}, err
	}
	return out, nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint, bearerToken string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Credentials.QuotaProject != "" {
		req.Header.Set("X-Goog-User-Project", c.Credentials.QuotaProject)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("google api error: %s", strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (c *Client) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func loadCredentials() (Credentials, error) {
	raw, err := loadGlobalJSON()
	if err != nil {
		return Credentials{}, err
	}
	clientID, clientIDSource := firstValue(raw,
		"GOOGLE_CLOUD_CLIENT_ID",
		"GMAIL_WEB_CLIENT_ID",
		"GMAIL_CLIENT_ID",
	)
	clientSecret, clientSecretSource := firstValue(raw,
		"GOOGLE_CLOUD_CLIENT_SECRET",
		"GMAIL_WEB_CLIENT_SECRET",
		"GMAIL_CLIENT_SECRET",
	)
	refreshToken, refreshTokenSource := firstValue(raw,
		"GOOGLE_CLOUD_REFRESH_TOKEN",
		"GMAIL_REFRESH_TOKEN",
	)
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return Credentials{}, fmt.Errorf("missing google cloud credentials in env or ~/cicy-ai/global.json")
	}
	quotaProject, _ := firstValue(raw, "GOOGLE_CLOUD_PROJECT_ID")
	return Credentials{
		ClientID:           clientID,
		ClientSecret:       clientSecret,
		RefreshToken:       refreshToken,
		QuotaProject:       quotaProject,
		ClientIDSource:     clientIDSource,
		ClientSecretSource: clientSecretSource,
		RefreshTokenSource: refreshTokenSource,
	}, nil
}

func loadGlobalJSON() (map[string]any, error) {
	path := filepath.Join(userHomeDir(), "cicy-ai", "global.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func firstValue(raw map[string]any, keys ...string) (string, string) {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, "env:" + key
		}
		if value := strings.TrimSpace(anyString(raw[key])); value != "" {
			return value, "global.json:" + key
		}
	}
	return "", ""
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

func splitScopes(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil
	}
	parts := strings.Fields(scope)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func hasScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == target {
			return true
		}
	}
	return false
}

func DetectEncoding(filename string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".wav":
		return "LINEAR16"
	case ".flac":
		return "FLAC"
	case ".ogg", ".opus":
		return "OGG_OPUS"
	case ".mp3":
		return "MP3"
	case ".webm":
		return "WEBM_OPUS"
	case ".amr":
		return "AMR"
	case ".awb":
		return "AMR_WB"
	default:
		return ""
	}
}

func GuessSampleRate(filename string, audio []byte, encoding string) int {
	if strings.EqualFold(encoding, "LINEAR16") {
		if sampleRate, ok := wavSampleRate(audio); ok {
			return sampleRate
		}
	}
	switch strings.ToUpper(strings.TrimSpace(encoding)) {
	case "OGG_OPUS", "WEBM_OPUS":
		return 48000
	default:
		return 0
	}
}

func wavSampleRate(data []byte) (int, bool) {
	if len(data) < 28 {
		return 0, false
	}
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, false
	}
	return int(binary.LittleEndian.Uint32(data[24:28])), true
}

var languagePattern = regexp.MustCompile(`^([a-z]{2,3}-[A-Z]{2,3})-`)

func DeriveLanguageCode(voiceName string) string {
	match := languagePattern.FindStringSubmatch(strings.TrimSpace(voiceName))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func defaultVoicePrefPath() string {
	return filepath.Join(userHomeDir(), "Private", "cicy-skills", "tts-voice.json")
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~"
	}
	return home
}

func TranscriptText(resp STTResponse) string {
	var lines []string
	for _, result := range resp.Results {
		if len(result.Alternatives) == 0 {
			continue
		}
		text := strings.TrimSpace(result.Alternatives[0].Transcript)
		if text != "" {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

func ParseInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}
