// Package fetch 提供多连接 Range 分片下载，用于加速单个大文件。
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// MinSegmentSize 是建议启用分片的最小文件大小：太小的文件分片收益抵不过握手开销。
const MinSegmentSize = 8 << 20

// minSegmentBytes 是单个分片的下限，避免把文件切成过多碎片。
const minSegmentBytes = 64 << 10

// ErrRangeUnsupported 表示服务端忽略了 Range 请求，调用方应回退到单连接下载。
var ErrRangeUnsupported = errors.New("服务器不支持 Range 分片下载")

// Segmented 用多条 Range 连接并发把 rawURL 写入 w，要求已知文件大小。
// progress 按新增字节数回调，可为 nil。
func Segmented(ctx context.Context, client *http.Client, rawURL string, w io.WriterAt, size int64, connections int, progress func(int64)) error {
	if size <= 0 {
		return errors.New("分片下载需要已知文件大小")
	}
	if connections < 2 {
		return ErrRangeUnsupported
	}
	limit := int(size / minSegmentBytes)
	if limit < 1 {
		limit = 1
	}
	if connections > limit {
		connections = limit
	}
	if connections < 2 {
		return ErrRangeUnsupported
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	chunk := (size + int64(connections) - 1) / int64(connections)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	fail := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}

	for i := 0; i < connections; i++ {
		start := int64(i) * chunk
		if start >= size {
			break
		}
		end := start + chunk - 1
		if end >= size {
			end = size - 1
		}
		length := end - start + 1
		wg.Add(1)
		go func(start, end, length int64) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
			if err != nil {
				fail(err)
				return
			}
			req.Header.Set("User-Agent", "CDRLauncher/1.0")
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			resp, err := client.Do(req)
			if err != nil {
				fail(fmt.Errorf("分片请求失败: %w", err))
				return
			}
			defer resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusPartialContent:
				got, err := rangeStart(resp.Header.Get("Content-Range"))
				if err != nil {
					fail(err)
					return
				}
				if got != start {
					fail(fmt.Errorf("分片响应区间不符: 期望 %d，实际 %d", start, got))
					return
				}
			case http.StatusOK:
				fail(ErrRangeUnsupported)
				return
			default:
				fail(fmt.Errorf("分片下载失败: HTTP %s", resp.Status))
				return
			}

			out := &countingWriter{w: io.NewOffsetWriter(w, start), cb: progress}
			n, err := io.Copy(out, io.LimitReader(resp.Body, length))
			if err != nil {
				fail(fmt.Errorf("写入分片失败: %w", err))
				return
			}
			if n != length {
				fail(fmt.Errorf("分片大小不符：收到 %d，需要 %d", n, length))
			}
		}(start, end, length)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return nil
}

type countingWriter struct {
	w  io.Writer
	cb func(int64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 && c.cb != nil {
		c.cb(int64(n))
	}
	return n, err
}

func rangeStart(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes ") {
		return 0, errors.New("缺少 Content-Range")
	}
	value = strings.TrimPrefix(value, "bytes ")
	before, _, ok := strings.Cut(value, "-")
	if !ok {
		return 0, fmt.Errorf("Content-Range 格式错误: %s", value)
	}
	var start int64
	if _, err := fmt.Sscanf(before, "%d", &start); err != nil {
		return 0, fmt.Errorf("Content-Range 起点错误: %s", value)
	}
	return start, nil
}
