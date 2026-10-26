package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const defaultYggdrasilPath = "/api/yggdrasil"

// YggdrasilSession 是皮肤站 yggdrasil 登录结果。
type YggdrasilSession struct {
	AccessToken string
	ClientToken string
	UUID        string
	Name        string
}

type yggdrasilProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type yggdrasilAuthResponse struct {
	AccessToken      string            `json:"accessToken"`
	ClientToken      string            `json:"clientToken"`
	SelectedProfile  *yggdrasilProfile `json:"selectedProfile"`
	RequestedProfile *yggdrasilProfile `json:"requestedProfile"`
	Error            string            `json:"error"`
	ErrorMessage     string            `json:"errorMessage"`
}

// NormalizeServerURL 规范化皮肤站 API 根地址，允许只填站点域名。
func NormalizeServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("请填写皮肤站 API 地址")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("皮肤站地址需为完整的 http(s) 地址")
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = defaultYggdrasilPath
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return strings.TrimRight(u.String(), "/"), nil
}

// LoginYggdrasil 用账号密码换取皮肤站 accessToken；密码不会被保存。
func LoginYggdrasil(ctx context.Context, server, username, password string) (YggdrasilSession, error) {
	server, err := NormalizeServerURL(server)
	if err != nil {
		return YggdrasilSession{}, err
	}
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return YggdrasilSession{}, errors.New("请填写皮肤站账号与密码")
	}
	clientToken := randomToken()
	payload := map[string]any{
		"username":    username,
		"password":    password,
		"clientToken": clientToken,
		"agent": map[string]any{
			"name":    "Minecraft",
			"version": 1,
		},
		"requestUser": false,
	}
	status, raw, err := postJSON(ctx, server+"/authserver/authenticate", payload, nil)
	if err != nil {
		return YggdrasilSession{}, fmt.Errorf("连接皮肤站失败: %w", err)
	}
	var resp yggdrasilAuthResponse
	_ = json.Unmarshal(raw, &resp)
	if status != http.StatusOK {
		return YggdrasilSession{}, fmt.Errorf("皮肤站登录失败: %s", yggdrasilErrorText(resp, status, raw))
	}
	profile := resp.SelectedProfile
	if profile == nil {
		profile = resp.RequestedProfile
	}
	if profile == nil || profile.ID == "" {
		return YggdrasilSession{}, errors.New("该皮肤站账号没有可用的游戏角色，请先在皮肤站创建角色")
	}
	if resp.AccessToken == "" {
		return YggdrasilSession{}, errors.New("皮肤站未返回 accessToken")
	}
	if resp.ClientToken != "" {
		clientToken = resp.ClientToken
	}
	return YggdrasilSession{
		AccessToken: resp.AccessToken,
		ClientToken: clientToken,
		UUID:        profile.ID,
		Name:        profile.Name,
	}, nil
}

// ValidateYggdrasil 检查令牌是否仍然有效。
func ValidateYggdrasil(ctx context.Context, server string, session YggdrasilSession) (bool, error) {
	payload := map[string]string{
		"accessToken": session.AccessToken,
		"clientToken": session.ClientToken,
	}
	status, raw, err := postJSON(ctx, strings.TrimRight(server, "/")+"/authserver/validate", payload, nil)
	if err != nil {
		return false, fmt.Errorf("连接皮肤站失败: %w", err)
	}
	switch status {
	case http.StatusNoContent, http.StatusOK:
		return true, nil
	case http.StatusForbidden:
		return false, nil
	}
	return false, fmt.Errorf("皮肤站校验失败（HTTP %d）: %s", status, truncate(raw, 160))
}

// RefreshYggdrasil 用现有令牌换新令牌。
func RefreshYggdrasil(ctx context.Context, server string, session YggdrasilSession) (YggdrasilSession, error) {
	payload := map[string]string{
		"accessToken": session.AccessToken,
		"clientToken": session.ClientToken,
	}
	status, raw, err := postJSON(ctx, strings.TrimRight(server, "/")+"/authserver/refresh", payload, nil)
	if err != nil {
		return YggdrasilSession{}, fmt.Errorf("连接皮肤站失败: %w", err)
	}
	var resp yggdrasilAuthResponse
	_ = json.Unmarshal(raw, &resp)
	if status != http.StatusOK || resp.AccessToken == "" {
		return YggdrasilSession{}, fmt.Errorf("皮肤站令牌已失效: %s", yggdrasilErrorText(resp, status, raw))
	}
	next := session
	next.AccessToken = resp.AccessToken
	if resp.ClientToken != "" {
		next.ClientToken = resp.ClientToken
	}
	if resp.SelectedProfile != nil && resp.SelectedProfile.ID != "" {
		next.UUID = resp.SelectedProfile.ID
		next.Name = resp.SelectedProfile.Name
	}
	return next, nil
}

func yggdrasilErrorText(resp yggdrasilAuthResponse, status int, raw []byte) string {
	switch {
	case resp.Error == "ForbiddenOperationException" && strings.Contains(strings.ToLower(resp.ErrorMessage), "invalid"):
		return "账号或密码错误"
	case resp.ErrorMessage != "":
		return resp.ErrorMessage
	case resp.Error != "":
		return resp.Error
	}
	return fmt.Sprintf("HTTP %d: %s", status, truncate(raw, 160))
}

func randomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "cdr-launcher"
	}
	return hex.EncodeToString(buf)
}
