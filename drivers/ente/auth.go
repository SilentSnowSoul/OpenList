package ente

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
)

// Password-login flow, ported from OpenList-APIPages frontend/src/lib/ente/
// client.ts and login.ts: login-family endpoints, response normalization and
// error mapping.

// srpAttributes is the attributes object of GET /users/srp/attributes.
type srpAttributes struct {
	SrpUserID         string `json:"srpUserID"`
	SrpSalt           string `json:"srpSalt"`
	KEKSalt           string `json:"kekSalt"`
	MemLimit          int64  `json:"memLimit"`
	OpsLimit          int64  `json:"opsLimit"`
	IsEmailMFAEnabled bool   `json:"isEmailMFAEnabled"`
}

// authResponse is the normalized AuthorizationResponse of verify-session and
// the two-factor endpoints. The 2FA branch carries no keyAttributes/token.
type authResponse struct {
	KeyAttributes        *enteKeyAttributes `json:"keyAttributes"`
	EncryptedToken       string             `json:"encryptedToken"`
	SrpM2                string             `json:"srpM2"`
	TwoFactorSessionID   string             `json:"twoFactorSessionID"`
	TwoFactorSessionIDV2 string             `json:"twoFactorSessionIDV2"`
	PasskeySessionID     string             `json:"passkeySessionID"`
}

// twoFactorSessionID returns the pending two-factor session; V1 and V2 are
// the same session to the server, which may send only V2.
func (r *authResponse) twoFactorSessionID() string {
	if r.TwoFactorSessionID != "" {
		return r.TwoFactorSessionID
	}
	return r.TwoFactorSessionIDV2
}

// errEmailOTPUnsupported covers both SRP-less accounts (404 attributes) and
// email-MFA accounts: the only remaining path is an emailed OTP code.
var errEmailOTPUnsupported = errors.New("[Ente] 该账号不支持密码登录(未注册 SRP 或已启用邮箱验证码),请改用 APIPages 凭证模式(token+master_key)")

// loginCredentials are the in-memory results of a password login. The master
// and secret keys are never written back to the storage addition.
type loginCredentials struct {
	token     string // base64url, ready for X-Auth-Token
	masterKey []byte
	secretKey []byte
}

// loginPassword runs the full SRP login: srp/attributes → argon2id KEK →
// blake2b loginKey → create/verify-session → TOTP when required → decrypt
// keyAttributes and unseal the account token.
func (d *Ente) loginPassword(ctx context.Context) (*loginCredentials, error) {
	client := NewClient(d.Endpoint, "", authTokenHeader)
	attrs, err := client.getSrpAttributes(ctx, d.Email)
	if err != nil {
		return nil, err
	}
	if attrs.IsEmailMFAEnabled {
		return nil, errEmailOTPUnsupported
	}
	kekSalt, err := fromB64(attrs.KEKSalt)
	if err != nil {
		return nil, fmt.Errorf("[Ente] 无效的 kekSalt: %w", err)
	}
	kek, err := deriveKEK(d.Password, kekSalt, attrs.OpsLimit, attrs.MemLimit)
	if err != nil {
		return nil, fmt.Errorf("[Ente] KEK 派生失败: %w", err)
	}
	loginKey, err := deriveLoginKey(kek)
	if err != nil {
		return nil, fmt.Errorf("[Ente] loginKey 派生失败: %w", err)
	}
	srpSalt, err := fromB64(attrs.SrpSalt)
	if err != nil {
		return nil, fmt.Errorf("[Ente] 无效的 srpSalt: %w", err)
	}
	session, err := newSrpSession([]byte(attrs.SrpUserID), srpSalt, loginKey, nil)
	if err != nil {
		return nil, err
	}
	sessionID, srpB, err := client.createSrpSession(ctx, attrs.SrpUserID, base64.StdEncoding.EncodeToString(session.computeA()))
	if err != nil {
		return nil, err
	}
	srpBBytes, err := fromB64(srpB)
	if err != nil {
		return nil, fmt.Errorf("[Ente] 无效的 srpB: %w", err)
	}
	m1, err := session.computeM1(srpBBytes)
	if err != nil {
		return nil, fmt.Errorf("[Ente] %w", err)
	}
	auth, err := client.verifySrpSession(ctx, attrs.SrpUserID, sessionID, base64.StdEncoding.EncodeToString(m1))
	if err != nil {
		return nil, err
	}
	// verify-session is the only response carrying srpM2 (the two-factor
	// response does not), so it must be validated before the 2FA step.
	srpM2, err := fromB64(auth.SrpM2)
	if err != nil {
		return nil, errors.New("[Ente] 服务端未返回 srpM2,登录中止")
	}
	if !session.verifyM2(srpM2) {
		return nil, errors.New("[Ente] srpM2 校验失败,登录中止")
	}
	tfSessionID := auth.twoFactorSessionID()
	if auth.PasskeySessionID != "" && tfSessionID == "" {
		return nil, errors.New("[Ente] 账号启用了 passkey 登录,暂不支持,请改用 APIPages 凭证模式")
	}
	if tfSessionID != "" {
		if d.TwoFASecret == "" {
			return nil, errors.New("[Ente] 账号已开启两步验证,请在配置中填写 two_fa_secret")
		}
		code, err := totp.GenerateCode(d.TwoFASecret, time.Now())
		if err != nil {
			return nil, fmt.Errorf("[Ente] 生成 TOTP 验证码失败: %w", err)
		}
		auth, err = client.verifyTwoFactor(ctx, tfSessionID, code)
		if err != nil {
			return nil, err
		}
	}
	if auth.KeyAttributes == nil || auth.EncryptedToken == "" {
		return nil, errors.New("[Ente] 登录响应不完整(缺少 keyAttributes 或 encryptedToken)")
	}
	masterKey, secretKey, err := openKeyAttributes(*auth.KeyAttributes, kek)
	if err != nil {
		return nil, fmt.Errorf("[Ente] %w", err)
	}
	tokenBytes, err := openToken(auth.EncryptedToken, auth.KeyAttributes.PublicKey, secretKey)
	if err != nil {
		return nil, fmt.Errorf("[Ente] token 解封失败: %w", err)
	}
	return &loginCredentials{
		token:     base64.URLEncoding.EncodeToString(tokenBytes),
		masterKey: masterKey,
		secretKey: secretKey,
	}, nil
}

// ---------------------------------------------------------------------------
// login endpoints
// ---------------------------------------------------------------------------

// getSrpAttributes fetches the SRP login attributes of an email. A 404 means
// the account has no SRP verifier and only the email-OTP path remains.
func (c *Client) getSrpAttributes(ctx context.Context, email string) (*srpAttributes, error) {
	path := "/users/srp/attributes?email=" + url.QueryEscape(email)
	resp, err := c.send(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("[Ente] 无法连接 Ente 服务器: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errEmailOTPUnsupported
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, mapLoginError(path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Attributes *srpAttributes `json:"attributes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Attributes == nil {
		return nil, fmt.Errorf("[Ente] GET %s: invalid srp attributes response", path)
	}
	return out.Attributes, nil
}

func (c *Client) createSrpSession(ctx context.Context, srpUserID, srpA string) (sessionID, srpB string, err error) {
	var out struct {
		SessionID string `json:"sessionID"`
		SrpB      string `json:"srpB"`
	}
	err = c.postJSON(ctx, "/users/srp/create-session",
		map[string]string{"srpUserID": srpUserID, "srpA": srpA}, &out)
	if err != nil {
		return "", "", err
	}
	if out.SessionID == "" || out.SrpB == "" {
		return "", "", errors.New("[Ente] 登录响应不完整(缺少 sessionID 或 srpB)")
	}
	return out.SessionID, out.SrpB, nil
}

func (c *Client) verifySrpSession(ctx context.Context, srpUserID, sessionID, srpM1 string) (*authResponse, error) {
	var out authResponse
	if err := c.postJSON(ctx, "/users/srp/verify-session",
		map[string]string{"srpUserID": srpUserID, "sessionID": sessionID, "srpM1": srpM1}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) verifyTwoFactor(ctx context.Context, sessionID, code string) (*authResponse, error) {
	var out authResponse
	if err := c.postJSON(ctx, "/users/two-factor/verify",
		map[string]string{"sessionID": sessionID, "code": code}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// getKeyAttributes fetches keyAttributes with the account token, for the
// local-decrypt fast path of password mode.
func (c *Client) getKeyAttributes(ctx context.Context) (*enteKeyAttributes, error) {
	var out struct {
		HasSetKeys    bool               `json:"hasSetKeys"`
		KeyAttributes *enteKeyAttributes `json:"keyAttributes"`
	}
	if err := c.getJSON(ctx, "/users/session-validity/v2", &out); err != nil {
		return nil, err
	}
	if !out.HasSetKeys || out.KeyAttributes == nil {
		return nil, errors.New("[Ente] 账号尚未设置密钥")
	}
	return out.KeyAttributes, nil
}

// postJSON POSTs a JSON body to a login-family endpoint and decodes the JSON
// response into out, mapping failures to readable messages.
func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header = c.authHeaders()
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("[Ente] 无法连接 Ente 服务器: %w", err)
	}
	defer resp.Body.Close()
	c.captureDeviceToken(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return mapLoginError(path, resp.StatusCode, e.Error)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("[Ente] POST %s: invalid JSON response: %w", path, err)
	}
	return nil
}

// mapLoginError translates login endpoint failures into readable messages
// without leaking bare status codes on the mapped branches.
func mapLoginError(path string, status int, serverMsg string) error {
	switch {
	case status == http.StatusUnauthorized:
		return errors.New("[Ente] 邮箱、密码或两步验证码错误")
	case status == http.StatusNotFound:
		return fmt.Errorf("[Ente] 账号不存在或端点错误: POST %s: %s", path, serverMsg)
	case status == http.StatusTooManyRequests:
		return errors.New("[Ente] 登录尝试过于频繁,请稍后再试")
	default:
		return fmt.Errorf("[Ente] POST %s failed: %d %s", path, status, serverMsg)
	}
}
