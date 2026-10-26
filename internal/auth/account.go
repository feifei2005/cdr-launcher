package auth

import (
	"strings"
	"time"
)

// Kind 区分账号来源。
type Kind string

const (
	KindOffline   Kind = "offline"
	KindMicrosoft Kind = "microsoft"
	KindYggdrasil Kind = "yggdrasil"
)

// Account 是持久化的登录账号；令牌由 Store 用 DPAPI 加密后落盘。
type Account struct {
	ID           string    `json:"id"`
	Kind         Kind      `json:"kind"`
	Name         string    `json:"name"`
	UUID         string    `json:"uuid"`
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ClientToken  string    `json:"clientToken,omitempty"`
	ServerURL    string    `json:"serverUrl,omitempty"`
	ClientID     string    `json:"clientId,omitempty"`
	XUID         string    `json:"xuid,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// DisplayKind 返回账号类型的中文名称。
func (a Account) DisplayKind() string {
	switch a.Kind {
	case KindMicrosoft:
		return "Microsoft"
	case KindYggdrasil:
		return "皮肤站"
	default:
		return "离线"
	}
}

// Expired 判断微软访问令牌是否即将过期（提前 3 分钟视为过期）。
// 皮肤站令牌没有可靠过期时间，启动前用 validate 校验。
func (a Account) Expired(now time.Time) bool {
	if a.Kind != KindMicrosoft || a.ExpiresAt.IsZero() {
		return false
	}
	return now.Add(3 * time.Minute).After(a.ExpiresAt)
}

func microsoftAccountID(uuid string) string {
	return "microsoft:" + normalizeUUID(uuid)
}

func yggdrasilAccountID(server, uuid string) string {
	return "yggdrasil:" + strings.TrimRight(server, "/") + ":" + normalizeUUID(uuid)
}

func normalizeUUID(uuid string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(uuid), "-", ""))
}

// NewMicrosoftAccount 由一次完整的微软登录结果构造账号。
func NewMicrosoftAccount(session MicrosoftSession, clientID string) Account {
	return Account{
		ID:           microsoftAccountID(session.UUID),
		Kind:         KindMicrosoft,
		Name:         session.Name,
		UUID:         normalizeUUID(session.UUID),
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ClientID:     clientID,
		XUID:         session.XUID,
		ExpiresAt:    session.ExpiresAt,
		CreatedAt:    time.Now(),
	}
}

// NewYggdrasilAccount 由一次皮肤站登录结果构造账号。
func NewYggdrasilAccount(server string, session YggdrasilSession) Account {
	return Account{
		ID:          yggdrasilAccountID(server, session.UUID),
		Kind:        KindYggdrasil,
		Name:        session.Name,
		UUID:        normalizeUUID(session.UUID),
		AccessToken: session.AccessToken,
		ClientToken: session.ClientToken,
		ServerURL:   strings.TrimRight(server, "/"),
		CreatedAt:   time.Now(),
	}
}

// Merge 合并同一账号的新令牌，保留创建时间。
func (a Account) Merge(next Account) Account {
	next.CreatedAt = a.CreatedAt
	return next
}
