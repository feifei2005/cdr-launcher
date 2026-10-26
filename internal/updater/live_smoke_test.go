//go:build live

package updater

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cdr-launcher/internal/manifest"
)

// TestLiveIncrementalUpdate 用线上更新源验证真实增量更新：
// 以 5.2.2 的本地索引为起点，安装 5.2.3 时应当只下载新增的模组 blob。
//
//	CDR_LIVE_BASE=https://mc-api.xn--suki-uf1gk54ba.cn/pack \
//	CDR_LIVE_PREV=dist/stable/manifest-5.2.2.json \
//	go test -tags live ./internal/updater -run TestLiveIncrementalUpdate -v
func TestLiveIncrementalUpdate(t *testing.T) {
	base := os.Getenv("CDR_LIVE_BASE")
	prevPath := os.Getenv("CDR_LIVE_PREV")
	root := os.Getenv("CDR_LIVE_ROOT")
	if base == "" || prevPath == "" || root == "" {
		t.Skip("需要 CDR_LIVE_BASE、CDR_LIVE_PREV 与 CDR_LIVE_ROOT（已装旧版的实例目录）")
	}
	prev, err := manifest.Load(prevPath)
	if err != nil {
		t.Fatalf("读取基线清单失败: %v", err)
	}

	idx := &Index{
		Version:     prev.Version,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Files:       map[string]FileState{},
	}
	for _, f := range prev.Files {
		idx.Files[f.Path] = FileState{Size: f.Size, Sha256: f.Sha256}
	}
	if err := SaveIndex(root, idx); err != nil {
		t.Fatalf("写入基线索引失败: %v", err)
	}

	opts := Options{
		Root:           root,
		ManifestURL:    base + "/manifest.json",
		CurrentVersion: prev.Version,
		PackID:         prev.PackID,
		Channel:        prev.Channel,
	}

	info, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("检查更新失败: %v", err)
	}
	t.Logf("check: %s → %s, changed=%d bytes=%d", info.CurrentVersion, info.LatestVersion, info.ChangedFiles, info.DownloadBytes)
	if !info.Available || info.UpToDate {
		t.Fatalf("应检测到新版本，实际 available=%v upToDate=%v", info.Available, info.UpToDate)
	}
	if info.ChangedFiles != 1 {
		t.Fatalf("增量更新应只含 1 个文件，实际 %d", info.ChangedFiles)
	}

	res, err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("安装更新失败: %v", err)
	}
	t.Logf("update: %s updated=%v files=%d bytes=%d", res.Version, res.Updated, res.Files, res.DownloadBytes)
	if !res.Updated || res.Version != info.LatestVersion {
		t.Fatalf("更新结果异常: %+v", res)
	}

	jar := filepath.Join(root, "mods", "goddamnrepackager-0.5.1-forge-alpha.jar")
	st, err := os.Stat(jar)
	if err != nil {
		t.Fatalf("新增模组未落盘: %v", err)
	}
	if st.Size() != 37372 {
		t.Fatalf("模组大小异常: %d", st.Size())
	}

	after, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("复检失败: %v", err)
	}
	if !after.UpToDate {
		t.Fatalf("安装后应已是最新，实际 %s → %s", after.CurrentVersion, after.LatestVersion)
	}
	t.Logf("复检: %s 已是最新", after.CurrentVersion)
}
