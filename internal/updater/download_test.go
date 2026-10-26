package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloaderUsesSegmentedForLargeBlob(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefgh"), 32*1024)
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])

	var ranged atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged.Add(1)
		}
		http.ServeContent(w, r, "blob", time.Time{}, bytes.NewReader(data))
	}))
	defer server.Close()

	dir := t.TempDir()
	dl, err := NewDownloader(server.URL+"/", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	dl.ChunkSize = 64 << 10
	dl.SetConcurrency(2, 2)
	if err := dl.Fetch(context.Background(), []Blob{{Sha256: hexSum, Size: int64(len(data))}}); err != nil {
		t.Fatalf("分片下载失败: %v", err)
	}
	if ranged.Load() < 2 {
		t.Fatalf("期望多个 Range 请求，实际 %d", ranged.Load())
	}
	got, err := os.ReadFile(filepath.Join(dir, hexSum))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("下载内容不一致")
	}
}

func TestDownloaderFallsBackWhenRangeIgnored(t *testing.T) {
	data := bytes.Repeat([]byte("01234567"), 32*1024)
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "262144")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	dir := t.TempDir()
	dl, err := NewDownloader(server.URL+"/", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	dl.ChunkSize = 64 << 10
	dl.SetConcurrency(2, 4)
	if err := dl.Fetch(context.Background(), []Blob{{Sha256: hexSum, Size: int64(len(data))}}); err != nil {
		t.Fatalf("服务端不支持 Range 时应回退单连接: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, hexSum))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("回退下载内容不一致")
	}
}
