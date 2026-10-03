package ente

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/net"
)

const (
	// DefaultEndpoint is the public Ente museum server.
	DefaultEndpoint = "https://api.ente.com"

	// clientPackage is the official app identifier the museum server expects.
	clientPackage = "io.ente.photos"

	// userAgent mirrors the official Android app (its Dio client sends the
	// platform WebView UA). The museum server uses it to pick the same hot-DC
	// presign behaviour as the official client, so the Go http default must
	// never leak instead.
	userAgent = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36"

	// authTokenHeader authenticates an account (X-Auth-Token).
	authTokenHeader = "X-Auth-Token"
	// AuthAccessHeader authenticates a public share (X-Auth-Access-Token).
	AuthAccessHeader = "X-Auth-Access-Token"

	// linkDeviceHeader replays the linkDeviceToken on every request.
	linkDeviceHeader = "X-Auth-Link-Device-Token"
	// deviceTokenHeader carries a newly issued linkDeviceToken.
	deviceTokenHeader = "X-Link-Device-Token"
)

// Client is a museum server API client. It is safe for concurrent use.
type Client struct {
	http        *http.Client
	endpoint    string
	tokenHeader string
	token       string

	deviceToken   string
	onDeviceToken func(string)
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithDeviceToken seeds the linkDeviceToken replayed as X-Auth-Link-Device-Token
// and registers onIssued, which is called with each newly issued token.
func WithDeviceToken(token string, onIssued func(string)) ClientOption {
	return func(c *Client) {
		c.deviceToken = token
		c.onDeviceToken = onIssued
	}
}

// NewClient returns a museum server client for endpoint. tokenHeader selects
// the credential header: authTokenHeader for accounts, AuthAccessHeader for
// public shares. An empty endpoint falls back to DefaultEndpoint.
func NewClient(endpoint, token, tokenHeader string, opts ...ClientOption) *Client {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	c := &Client{
		http:        net.NewHttpClient(),
		endpoint:    strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		tokenHeader: tokenHeader,
		token:       token,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) authHeaders() http.Header {
	h := make(http.Header, 5)
	h.Set("User-Agent", userAgent)
	h.Set("X-Client-Package", clientPackage)
	if c.tokenHeader != "" && c.token != "" {
		h.Set(c.tokenHeader, c.token)
	}
	if c.deviceToken != "" {
		h.Set(linkDeviceHeader, c.deviceToken)
	}
	return h
}

// captureDeviceToken records a token issued (or renewed) by the server.
func (c *Client) captureDeviceToken(resp *http.Response) {
	token := resp.Header.Get(deviceTokenHeader)
	if token == "" || token == c.deviceToken {
		return
	}
	c.deviceToken = token
	if c.onDeviceToken != nil {
		c.onDeviceToken(token)
	}
}

func (c *Client) send(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.authHeaders()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	c.captureDeviceToken(resp)
	return resp, nil
}

// getJSON GETs path and decodes the JSON body into out.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.send(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.mapError(ctx, path, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("[Ente] GET %s: invalid JSON response: %w", path, err)
	}
	return nil
}

// mapError translates museum server failures into actionable messages.
// Status semantics follow the museum server collection-link middleware.
func (c *Client) mapError(ctx context.Context, path string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	status := resp.StatusCode
	switch {
	case status == http.StatusGone:
		// 410 means the owner's subscription lapsed; the account driver sees
		// its own credentials rejected, the share driver a disabled link.
		if c.tokenHeader == authTokenHeader {
			return errors.New("[Ente] 账号订阅已失效(410),请续费或更新凭证")
		}
		return errors.New("[Ente] 分享已过期(所有者订阅失效或分享已停用)")
	case status == http.StatusForbidden && strings.Contains(strings.ToLower(string(body)), "device limit"):
		return errors.New("[Ente] 分享设备数已达上限,请让所有者清理设备")
	case status == http.StatusUnauthorized:
		if c.tokenHeader == AuthAccessHeader {
			ok, err := c.probePublicCollection(ctx)
			if ok {
				return errors.New("[Ente] 分享受密码保护,暂不支持,请使用无密码分享")
			}
			if err != nil {
				return errors.New("[Ente] token 无效或已失效,且密码保护探测失败无法确认原因")
			}
			return errors.New("[Ente] token 无效或已失效")
		}
		return errors.New("[Ente] token 无效或已失效")
	default:
		return fmt.Errorf("[Ente] GET %s failed: %d %s", path, status, strings.TrimSpace(string(body)))
	}
}

// probePublicCollection checks /public-collection/info to tell a
// password-protected share (200) from a dead token (401).
func (c *Client) probePublicCollection(ctx context.Context) (bool, error) {
	resp, err := c.send(ctx, "/public-collection/info")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300, nil
}

// ---------------------------------------------------------------------------
// endpoints
// ---------------------------------------------------------------------------

type enteCollectionsResponse struct {
	Collections []EnteCollection `json:"collections"`
}

// GetCollections lists the account's collections. The museum server wraps the
// list in a collections object, same convention as the diff endpoints.
func (c *Client) GetCollections(ctx context.Context) ([]EnteCollection, error) {
	var out enteCollectionsResponse
	if err := c.getJSON(ctx, "/collections/v2?sinceTime=0", &out); err != nil {
		return nil, err
	}
	return out.Collections, nil
}

// GetCollectionDiff returns one page of a collection's diff.
func (c *Client) GetCollectionDiff(ctx context.Context, collectionID, sinceTime int64) (*EnteDiffResponse, error) {
	var out EnteDiffResponse
	path := "/collections/v2/diff?collectionID=" + strconv.FormatInt(collectionID, 10) + "&sinceTime=" + strconv.FormatInt(sinceTime, 10)
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPublicDiff returns one page of a public share's diff.
func (c *Client) GetPublicDiff(ctx context.Context, sinceTime int64) (*EnteDiffResponse, error) {
	var out EnteDiffResponse
	path := "/public-collection/diff?sinceTime=" + strconv.FormatInt(sinceTime, 10)
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type enteURLResponse struct {
	URL string `json:"url"`
}

// presignedURL GETs a v3 endpoint and returns the presigned S3 URL.
func (c *Client) presignedURL(ctx context.Context, path string) (string, error) {
	var out enteURLResponse
	if err := c.getJSON(ctx, path, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// GetFileDownloadURL returns the presigned download URL of an account file.
func (c *Client) GetFileDownloadURL(ctx context.Context, id int64) (string, error) {
	return c.presignedURL(ctx, "/files/download/v3/"+strconv.FormatInt(id, 10))
}

// GetFileThumbURL returns the presigned thumbnail URL of an account file.
func (c *Client) GetFileThumbURL(ctx context.Context, id int64) (string, error) {
	return c.presignedURL(ctx, "/files/thumbnail/v3/"+strconv.FormatInt(id, 10))
}

// GetDownloadURL returns the presigned download URL of a shared file.
func (c *Client) GetDownloadURL(ctx context.Context, id int64) (string, error) {
	return c.presignedURL(ctx, "/public-collection/files/download/v3/"+strconv.FormatInt(id, 10))
}

// GetThumbURL returns the presigned thumbnail URL of a shared file.
func (c *Client) GetThumbURL(ctx context.Context, id int64) (string, error) {
	return c.presignedURL(ctx, "/public-collection/files/thumbnail/v3/"+strconv.FormatInt(id, 10))
}

// FetchBinary streams a presigned S3 URL. S3 authenticates the URL itself, so
// no ente header may be attached; the Go default User-Agent is suppressed too.
func (c *Client) FetchBinary(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// A present-but-empty User-Agent is not written by net/http.
	req.Header.Set("User-Agent", "")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("[Ente] binary fetch failed: %d", resp.StatusCode)
	}
	return resp.Body, nil
}
