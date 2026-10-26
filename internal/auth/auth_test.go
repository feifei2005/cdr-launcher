package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeServerURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://littleskin.cn", "https://littleskin.cn/api/yggdrasil", false},
		{"https://littleskin.cn/", "https://littleskin.cn/api/yggdrasil", false},
		{"https://littleskin.cn/api/yggdrasil/", "https://littleskin.cn/api/yggdrasil", false},
		{"  https://example.com/custom/api  ", "https://example.com/custom/api", false},
		{"littleskin.cn", "", true},
		{"", "", true},
		{"ftp://littleskin.cn", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeServerURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeServerURL(%q) 期望报错，实际得到 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeServerURL(%q) 返回错误: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeServerURL(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestNewMicrosoftAccount(t *testing.T) {
	session := MicrosoftSession{
		UUID:        "ABCDEF01-2345-6789-ABCD-EF1234567890",
		Name:        "Steve",
		AccessToken: "token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	acc := NewMicrosoftAccount(session, "client")
	if acc.ID != "microsoft:abcdef0123456789abcdef1234567890" {
		t.Fatalf("账号 ID 不符: %q", acc.ID)
	}
	if acc.Kind != KindMicrosoft || acc.Name != "Steve" || acc.ClientID != "client" {
		t.Fatalf("账号字段不符: %+v", acc)
	}
	if acc.Expired(time.Now()) {
		t.Fatal("刚登录的账号不应视为过期")
	}
	if !acc.Expired(time.Now().Add(59 * time.Minute)) {
		t.Fatal("距过期不足 3 分钟时应视为过期")
	}
}

func TestNewYggdrasilAccount(t *testing.T) {
	acc := NewYggdrasilAccount("https://littleskin.cn/api/yggdrasil/", YggdrasilSession{
		AccessToken: "token",
		ClientToken: "client-token",
		UUID:        "0123456789ABCDEF0123456789ABCDEF",
		Name:        "Alex",
	})
	if acc.ID != "yggdrasil:https://littleskin.cn/api/yggdrasil:0123456789abcdef0123456789abcdef" {
		t.Fatalf("账号 ID 不符: %q", acc.ID)
	}
	if acc.ServerURL != "https://littleskin.cn/api/yggdrasil" {
		t.Fatalf("服务器地址未去除尾部斜杠: %q", acc.ServerURL)
	}
	if acc.Expired(time.Now()) {
		t.Fatal("皮肤站账号不应按微软令牌过期规则判断")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "accounts.dat"))
	if _, err := store.Load(); err != nil {
		t.Fatalf("文件不存在时应返回空列表: %v", err)
	}
	list := []Account{{
		ID:          "microsoft:abc",
		Kind:        KindMicrosoft,
		Name:        "Steve",
		AccessToken: "secret-token",
		CreatedAt:   time.Now(),
	}}
	if err := store.Save(list); err != nil {
		t.Fatalf("保存账号失败: %v", err)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-token") {
		t.Fatal("账号文件不应包含明文令牌")
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if len(got) != 1 || got[0].AccessToken != "secret-token" || got[0].Name != "Steve" {
		t.Fatalf("账号内容不一致: %+v", got)
	}
}
