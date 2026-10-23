package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// OAuth2Config authenticates each HTTP check using a new client-credentials
// grant. Token endpoint trust is separate from the monitored server's client
// certificates and headers. Tokens are never retained in monitor configuration.
type OAuth2Config struct {
	TokenURL          string `json:"tokenUrl"`
	ClientID          string `json:"clientId"`
	ClientSecret      string `json:"clientSecret"`
	Scope             string `json:"scope,omitempty"`
	Audience          string `json:"audience,omitempty"`
	AuthMethod        string `json:"authMethod,omitempty"`
	TokenCAPEM        string `json:"tokenCaPem,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHttp,omitempty"`
}

func (o *OAuth2Config) UnmarshalJSON(data []byte) error {
	fields, err := oauth2Object(data)
	if err != nil {
		return err
	}
	allowed := map[string]bool{"tokenUrl": true, "clientId": true, "clientSecret": true,
		"scope": true, "audience": true, "authMethod": true, "tokenCaPem": true, "allowInsecureHttp": true}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("Invalid OAuth2 options")
		}
	}
	type plain OAuth2Config
	var value plain
	if json.Unmarshal(data, &value) != nil {
		return errors.New("Invalid OAuth2 options")
	}
	*o = OAuth2Config(value)
	return nil
}

// Reject duplicate keys before selecting credentials, even when the values
// happen to agree. The same decoder protects authorization-server responses.
func oauth2Object(data []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("Invalid OAuth2 object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, errors.New("Invalid OAuth2 object")
		}
		if _, exists := fields[key]; exists {
			return nil, errors.New("Duplicate OAuth2 field")
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, errors.New("Invalid OAuth2 object")
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, errors.New("Invalid OAuth2 object")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("Invalid OAuth2 trailing input")
	}
	return fields, nil
}

func validateOAuth2(o OAuth2Config, target string) error {
	u, err := parseTargetURL(o.TokenURL, false)
	if err != nil {
		return invalid("OAuth2 token URL is invalid")
	}
	// Option-only validation has no target; execution supplies it again.
	targetSecure := true
	if target != "" {
		targetURL, targetErr := parseTargetURL(target, false)
		if targetErr != nil {
			return invalid("OAuth2 target URL is invalid")
		}
		targetSecure = targetURL.Scheme == "https"
	}
	if !o.AllowInsecureHTTP && (u.Scheme != "https" || !targetSecure) {
		return invalid("OAuth2 requires HTTPS unless insecure HTTP is explicitly enabled")
	}
	if o.ClientID == "" || len(o.ClientID) > 4096 || len(o.ClientSecret) > 8192 ||
		len(o.Scope) > 4096 || len(o.Audience) > 4096 || hasControl(o.Scope) || hasControl(o.Audience) {
		return invalid("OAuth2 credential or grant fields are invalid or too long")
	}
	if o.AuthMethod != "" && o.AuthMethod != "client_secret_basic" && o.AuthMethod != "client_secret_post" {
		return invalid("OAuth2 authentication method is unsupported")
	}
	if o.AuthMethod != "client_secret_post" && 4*((len(url.QueryEscape(o.ClientID))+len(url.QueryEscape(o.ClientSecret))+3)/3) > maxHeaderBytes-1024 {
		return invalid("OAuth2 encoded credentials exceed header limit")
	}
	if validateTLS(TLSConfig{CAPEM: o.TokenCAPEM}) != nil {
		return invalid("OAuth2 token CA configuration is invalid")
	}
	return nil
}

func oauth2Token(ctx context.Context, options HTTPConfig) (string, error) {
	o := *options.OAuth2
	transport, err := makeTransport(TLSConfig{CAPEM: o.TokenCAPEM}, options.ProxyURL, options.UseEnvironmentProxy)
	if err != nil {
		return "", errors.New("OAuth2 transport unavailable")
	}
	defer transport.CloseIdleConnections()
	form := url.Values{"grant_type": {"client_credentials"}}
	if o.Scope != "" {
		form.Set("scope", o.Scope)
	}
	if o.Audience != "" {
		form.Set("audience", o.Audience)
	}
	if o.AuthMethod == "client_secret_post" {
		form.Set("client_id", o.ClientID)
		form.Set("client_secret", o.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("OAuth2 request unavailable")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if o.AuthMethod != "client_secret_post" {
		// RFC 6749 section 2.3.1 encodes both credentials before Basic auth.
		req.SetBasicAuth(url.QueryEscape(o.ClientID), url.QueryEscape(o.ClientSecret))
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("OAuth2 token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("OAuth2 token request refused")
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return "", errors.New("OAuth2 response is not JSON")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return "", errors.New("OAuth2 response exceeds limits")
	}
	if _, err = decodeJSON(data); err != nil {
		return "", errors.New("OAuth2 response is invalid")
	}
	fields, err := oauth2Object(data)
	if err != nil {
		return "", errors.New("OAuth2 response is invalid")
	}
	var token, kind string
	if json.Unmarshal(fields["access_token"], &token) != nil || json.Unmarshal(fields["token_type"], &kind) != nil ||
		!strings.EqualFold(kind, "Bearer") || !oauth2Bearer(token) {
		return "", errors.New("OAuth2 response has no valid bearer token")
	}
	return token, nil
}

func oauth2Bearer(token string) bool {
	if token == "" || len(token) > 8192 {
		return false
	}
	padding := false
	content := false
	for _, c := range token {
		if c == '=' {
			padding = true
			continue
		}
		if padding || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", c)) {
			return false
		}
		content = true
	}
	return content
}
