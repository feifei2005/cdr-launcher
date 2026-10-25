package launcher

import (
	"crypto/md5"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Player 是启动参数所需的离线账户信息。
type Player struct {
	Name        string `json:"name"`
	UUID        string `json:"uuid"`
	AccessToken string `json:"accessToken"`
	UserType    string `json:"userType"`
	ClientID    string `json:"clientId"`
	XUID        string `json:"xuid"`
}

var offlineNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

// OfflinePlayer 生成与原版离线模式一致的 UUIDv3(MD5("OfflinePlayer:"+name))。
func OfflinePlayer(name string) (Player, error) {
	name = strings.TrimSpace(name)
	if !offlineNameRe.MatchString(name) {
		return Player{}, errors.New("游戏昵称需为 3-16 位字母、数字或下划线")
	}
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	sum[6] = (sum[6] & 0x0f) | 0x30 // UUID version 3
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	return Player{
		Name:        name,
		UUID:        fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16]),
		AccessToken: strings.Repeat("0", 32),
		UserType:    "legacy",
	}, nil
}
