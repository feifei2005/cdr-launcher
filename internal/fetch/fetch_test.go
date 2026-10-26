package fetch

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestSegmentedDownloadsAllRanges(t *testing.T) {
	data := make([]byte, MinSegmentSize+1024*256)
	for i := range data {
		data[i] = byte(i % 251)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "blob", time.Time{}, bytes.NewReader(data))
	}))
	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "seg-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var reported atomic.Int64
	err = Segmented(context.Background(), server.Client(), server.URL, file, int64(len(data)), 4, func(n int64) { reported.Add(n) })
	if err != nil {
		t.Fatalf("分片下载失败: %v", err)
	}
	if got := reported.Load(); got != int64(len(data)) {
		t.Fatalf("进度回调合计 %d，期望 %d", got, len(data))
	}
	got, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("分片下载内容不一致")
	}
}

func TestSegmentedRejectsServerWithoutRange(t *testing.T) {
	data := make([]byte, 2*MinSegmentSize)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "seg-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	err = Segmented(context.Background(), server.Client(), server.URL, file, int64(len(data)), 4, nil)
	if !errors.Is(err, ErrRangeUnsupported) {
		t.Fatalf("期望 ErrRangeUnsupported，实际 %v", err)
	}
}
