package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	msDeviceCodeEndpoint = "https://login.microsoftonline.com/consumers/oauth2/v2.0/devicecode"
	msTokenEndpoint      = "https://login.microsoftonline.com/consumers/oauth2/v2.0/token"
	msScope              = "XboxLive.signin offline_access"

	xblAuthEndpoint   = "https://user.auth.xboxlive.com/user/authenticate"
	xstsEndpoint      = "https://xsts.auth.xboxlive.com/xsts/authorize"
	mcLoginEndpoint   = "https://api.minecraftservices.com/authentication/login_with_xbox"
	mcProfileEndpoint = "https://api.minecraftservices.com/minecraft/profile"
)

// DeviceCode 是微软设备码登录返回的一次性信息。
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

// MicrosoftSession 是完成整条 Xbox/Minecraft 链路后的登录结果。
type MicrosoftSession struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Name         string
	UUID         string
	XUID         string
}

type tokenPayload struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type xboxAuthResponse struct {
	Token         string `json:"Token"`
	DisplayClaims struct {
		XUI []struct {
			UHS string `json:"uhs"`
			XID string `json:"xid"`
		} `json:"xui"`
	} `json:"DisplayClaims"`
}

type xstsErrorResponse struct {
	Identity string `json:"Identity"`
	XErr     uint64 `json:"XErr"`
	Message  string `json:"Message"`
}

type minecraftProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// StartDeviceCode 申请设备码，用户需要在浏览器中完成授权。
func StartDeviceCode(ctx context.Context, clientID string) (DeviceCode, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return DeviceCode{}, errors.New("未配置 Microsoft Client ID，请先在启动器设置中填写 Azure 应用 ID")
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("scope", msScope)
	status, raw, err := postForm(ctx, msDeviceCodeEndpoint, form)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("请求 Microsoft 登录失败: %w", err)
	}
	if status != http.StatusOK {
		return DeviceCode{}, fmt.Errorf("请求 Microsoft 登录失败: %s", oauthErrorText(raw, status))
	}
	var dc DeviceCode
	if err := json.Unmarshal(raw, &dc); err != nil {
		return DeviceCode{}, fmt.Errorf("解析设备码失败: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return DeviceCode{}, errors.New("Microsoft 返回的设备码为空")
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	if dc.ExpiresIn <= 0 {
		dc.ExpiresIn = 900
	}
	log.Printf("msa: 设备码已获取（interval=%ds, expires=%ds）", dc.Interval, dc.ExpiresIn)
	return dc, nil
}

// transientError 表示可重试的临时故障（网络抖动、5xx、429）。
type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }
func (e transientError) Unwrap() error { return e.err }

// WaitForDeviceToken 轮询设备码，直到用户完成授权、超时或 ctx 取消。
func WaitForDeviceToken(ctx context.Context, clientID string, dc DeviceCode) (MicrosoftSession, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval < 2*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	started := time.Now()
	transient := 0
	log.Printf("msa: 等待浏览器授权（interval=%s）", interval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("msa: 等待授权被取消")
			return MicrosoftSession{}, ctx.Err()
		case <-time.After(interval):
		}
		session, state, err := pollDeviceToken(ctx, clientID, dc.DeviceCode)
		if err != nil {
			var te transientError
			if errors.As(err, &te) && transient < 2 {
				transient++
				log.Printf("msa: 轮询暂时失败（第 %d 次），继续重试: %v", transient, err)
				continue
			}
			log.Printf("msa: 轮询失败（已等待 %s）: %v", time.Since(started).Round(time.Second), err)
			return MicrosoftSession{}, err
		}
		transient = 0
		switch state {
		case pollOK:
			log.Printf("msa: 已获取 Microsoft 令牌（等待 %s）", time.Since(started).Round(time.Second))
			return session, nil
		case pollSlowDown:
			interval += 5 * time.Second
			log.Printf("msa: 服务端要求降速，轮询间隔调整为 %s", interval)
		default:
			log.Printf("msa: 授权尚未完成（已等待 %s）", time.Since(started).Round(time.Second))
			if time.Now().After(deadline) {
				return MicrosoftSession{}, errors.New("登录码已过期，请重新获取")
			}
		}
	}
}

type pollState int

const (
	pollOK pollState = iota
	pollPending
	pollSlowDown
)

func pollDeviceToken(ctx context.Context, clientID, deviceCode string) (MicrosoftSession, pollState, error) {
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("client_id", clientID)
	form.Set("device_code", deviceCode)
	status, raw, err := postForm(ctx, msTokenEndpoint, form)
	if err != nil {
		return MicrosoftSession{}, pollPending, transientError{fmt.Errorf("轮询 Microsoft 登录失败: %w", err)}
	}
	if status >= http.StatusInternalServerError || status == http.StatusTooManyRequests {
		return MicrosoftSession{}, pollPending, transientError{fmt.Errorf("Microsoft 登录服务暂时不可用（HTTP %d）", status)}
	}
	var payload tokenPayload
	_ = json.Unmarshal(raw, &payload)
	if status == http.StatusOK && payload.AccessToken != "" {
		return MicrosoftSession{
			AccessToken:  payload.AccessToken,
			RefreshToken: payload.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
		}, pollOK, nil
	}
	switch payload.Error {
	case "authorization_pending":
		return MicrosoftSession{}, pollPending, nil
	case "slow_down":
		return MicrosoftSession{}, pollSlowDown, nil
	case "expired_token":
		return MicrosoftSession{}, pollOK, errors.New("登录码已过期，请重新获取")
	case "authorization_declined":
		return MicrosoftSession{}, pollOK, errors.New("登录请求已被拒绝")
	case "bad_verification_code":
		return MicrosoftSession{}, pollOK, errors.New("登录码无效，请重新获取")
	}
	if payload.Error != "" {
		return MicrosoftSession{}, pollOK, fmt.Errorf("Microsoft 登录失败: %s", tokenErrorText(payload))
	}
	return MicrosoftSession{}, pollOK, fmt.Errorf("Microsoft 登录失败: %s", truncate(raw, 200))
}

// RefreshMicrosoftToken 用 refresh token 换新的微软访问令牌（尚未换 Minecraft 令牌）。
func RefreshMicrosoftToken(ctx context.Context, clientID, refreshToken string) (MicrosoftSession, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientID)
	form.Set("refresh_token", refreshToken)
	form.Set("scope", msScope)
	status, raw, err := postForm(ctx, msTokenEndpoint, form)
	if err != nil {
		return MicrosoftSession{}, fmt.Errorf("刷新微软登录失败: %w", err)
	}
	var payload tokenPayload
	_ = json.Unmarshal(raw, &payload)
	if status != http.StatusOK || payload.AccessToken == "" {
		if payload.Error == "invalid_grant" {
			return MicrosoftSession{}, errors.New("微软登录已过期，请重新登录")
		}
		return MicrosoftSession{}, fmt.Errorf("刷新微软登录失败: %s", tokenErrorText(payload))
	}
	return MicrosoftSession{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	}, nil
}

// AuthenticateMinecraft 走完 Xbox Live → XSTS → Minecraft Services → 档案查询。
func AuthenticateMinecraft(ctx context.Context, session MicrosoftSession) (MicrosoftSession, error) {
	log.Printf("msa: 验证 Xbox Live 令牌")
	xblToken, _, err := authXboxLive(ctx, session.AccessToken)
	if err != nil {
		return MicrosoftSession{}, err
	}
	log.Printf("msa: 申请 XSTS 授权")
	xstsToken, uhs, xuid, err := authXSTS(ctx, xblToken)
	if err != nil {
		return MicrosoftSession{}, err
	}
	log.Printf("msa: 换取 Minecraft 访问令牌")
	mcToken, expiresIn, err := loginWithXbox(ctx, uhs, xstsToken)
	if err != nil {
		return MicrosoftSession{}, err
	}
	log.Printf("msa: 查询 Minecraft 档案")
	profile, err := fetchMinecraftProfile(ctx, mcToken)
	if err != nil {
		return MicrosoftSession{}, err
	}
	log.Printf("msa: 登录完成，账号 %s", profile.Name)
	session.AccessToken = mcToken
	session.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	session.Name = profile.Name
	session.UUID = normalizeUUID(profile.ID)
	session.XUID = xuid
	return session, nil
}

func authXboxLive(ctx context.Context, msAccessToken string) (token, uhs string, err error) {
	payload := map[string]any{
		"Properties": map[string]any{
			"AuthMethod": "RPS",
			"SiteName":   "user.auth.xboxlive.com",
			"RpsTicket":  "d=" + msAccessToken,
		},
		"RelyingParty": "http://auth.xboxlive.com",
		"TokenType":    "JWT",
	}
	status, raw, err := postJSON(ctx, xblAuthEndpoint, payload, map[string]string{
		"x-xbl-contract-version": "1",
	})
	if err != nil {
		return "", "", fmt.Errorf("Xbox Live 验证失败: %w", err)
	}
	if status != http.StatusOK {
		if msg := xboxErrorText(raw); msg != "" {
			return "", "", fmt.Errorf("Xbox Live 验证失败: %s", msg)
		}
		return "", "", fmt.Errorf("Xbox Live 验证失败（HTTP %d）", status)
	}
	var resp xboxAuthResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", fmt.Errorf("解析 Xbox Live 响应失败: %w", err)
	}
	if resp.Token == "" || len(resp.DisplayClaims.XUI) == 0 {
		return "", "", errors.New("Xbox Live 返回的令牌为空")
	}
	return resp.Token, resp.DisplayClaims.XUI[0].UHS, nil
}

func authXSTS(ctx context.Context, xblToken string) (token, uhs, xuid string, err error) {
	payload := map[string]any{
		"Properties": map[string]any{
			"SandboxId":  "RETAIL",
			"UserTokens": []string{xblToken},
		},
		"RelyingParty": "rp://api.minecraftservices.com/",
		"TokenType":    "JWT",
	}
	status, raw, err := postJSON(ctx, xstsEndpoint, payload, map[string]string{
		"x-xbl-contract-version": "1",
	})
	if err != nil {
		return "", "", "", fmt.Errorf("XSTS 验证失败: %w", err)
	}
	if status != http.StatusOK {
		var xerr xstsErrorResponse
		if json.Unmarshal(raw, &xerr) == nil && xerr.XErr != 0 {
			return "", "", "", errors.New(xstsErrorHint(xerr.XErr, xerr.Message))
		}
		return "", "", "", fmt.Errorf("XSTS 验证失败（HTTP %d）", status)
	}
	var resp xboxAuthResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", "", fmt.Errorf("解析 XSTS 响应失败: %w", err)
	}
	if resp.Token == "" || len(resp.DisplayClaims.XUI) == 0 {
		return "", "", "", errors.New("XSTS 返回的令牌为空")
	}
	claim := resp.DisplayClaims.XUI[0]
	return resp.Token, claim.UHS, claim.XID, nil
}

func loginWithXbox(ctx context.Context, uhs, xstsToken string) (string, int, error) {
	payload := map[string]string{"identityToken": "XBL3.0 x=" + uhs + ";" + xstsToken}
	status, raw, err := postJSON(ctx, mcLoginEndpoint, payload, nil)
	if err != nil {
		return "", 0, fmt.Errorf("Minecraft 登录失败: %w", err)
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Message     string `json:"errorMessage"`
	}
	_ = json.Unmarshal(raw, &resp)
	if status != http.StatusOK || resp.AccessToken == "" {
		if status == http.StatusUnauthorized {
			return "", 0, errors.New("Xbox 身份验证被拒绝，请重新登录")
		}
		if strings.Contains(strings.ToLower(resp.Message), "invalid app registration") {
			return "", 0, errors.New("Azure 应用未通过 Minecraft 第三方启动器审核（Invalid app registration）。请到 https://aka.ms/mce-reviewappid 提交 Client ID，审核通过后重新登录")
		}
		if resp.Message != "" {
			return "", 0, fmt.Errorf("Minecraft 登录失败: %s", resp.Message)
		}
		return "", 0, fmt.Errorf("Minecraft 登录失败（HTTP %d）: %s", status, truncate(raw, 160))
	}
	if resp.ExpiresIn <= 0 {
		resp.ExpiresIn = 86400
	}
	return resp.AccessToken, resp.ExpiresIn, nil
}

func fetchMinecraftProfile(ctx context.Context, accessToken string) (minecraftProfile, error) {
	status, raw, err := getJSON(ctx, mcProfileEndpoint, map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	if err != nil {
		return minecraftProfile{}, fmt.Errorf("查询 Minecraft 账号失败: %w", err)
	}
	if status == http.StatusNotFound {
		return minecraftProfile{}, errors.New("该 Microsoft 账号未拥有 Minecraft Java 版")
	}
	if status != http.StatusOK {
		return minecraftProfile{}, fmt.Errorf("查询 Minecraft 账号失败（HTTP %d）", status)
	}
	var profile minecraftProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return minecraftProfile{}, fmt.Errorf("解析 Minecraft 账号失败: %w", err)
	}
	if profile.ID == "" || profile.Name == "" {
		return minecraftProfile{}, errors.New("Minecraft 账号信息不完整")
	}
	return profile, nil
}

func oauthErrorText(raw []byte, status int) string {
	var payload tokenPayload
	if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
		return tokenErrorText(payload)
	}
	return fmt.Sprintf("HTTP %d: %s", status, truncate(raw, 160))
}

func tokenErrorText(payload tokenPayload) string {
	if payload.ErrorDescription != "" {
		return payload.Error + ": " + payload.ErrorDescription
	}
	if payload.Error != "" {
		return payload.Error
	}
	return "未知错误"
}

func xboxErrorText(raw []byte) string {
	var payload xstsErrorResponse
	if json.Unmarshal(raw, &payload) == nil && payload.XErr != 0 {
		return xstsErrorHint(payload.XErr, payload.Message)
	}
	return ""
}

func xstsErrorHint(code uint64, message string) string {
	switch code {
	case 2148916233:
		return "该 Microsoft 账号没有 Xbox 档案，请先访问 xbox.com 创建"
	case 2148916235:
		return "Xbox Live 在你所在地区不可用"
	case 2148916236, 2148916237:
		return "该账号需要先完成成人验证"
	case 2148916238:
		return "未成年账号需要先加入 Microsoft 家庭组"
	}
	if message != "" {
		return fmt.Sprintf("%s（XErr=%d）", message, code)
	}
	return fmt.Sprintf("Xbox 验证失败（XErr=%d）", code)
}
