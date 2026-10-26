package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cdr-launcher/internal/fetch"
	"cdr-launcher/internal/netproxy"
)

const (
	defaultWorkers  = 512
	defaultSegments = 32
	maxWorkers      = 1024
	maxSegments     = 64
	maxAttempts     = 3
)

// Downloader 负责把 blob 下载到 staging 目录，支持 Range 续传与多连接分片。
type Downloader struct {
	Client   *http.Client
	BaseURL  string
	Dir      string
	Workers  int
	Segments int
	// ChunkSize 是启用分片的最小文件大小，测试中可调小。
	ChunkSize int64
	Progress  func(done, total int64)
}

// SetConcurrency 设置并发下载的文件数与单文件分片连接数。
func (d *Downloader) SetConcurrency(workers, segments int) {
	if workers < 1 {
		workers = defaultWorkers
	}
	if workers > maxWorkers {
		workers = maxWorkers
	}
	if segments <= 0 {
		segments = defaultSegments
	}
	if segments > maxSegments {
		segments = maxSegments
	}
	d.Workers = workers
	d.Segments = segments
	if transport, ok := d.Client.Transport.(*http.Transport); ok {
		transport.MaxIdleConns = workers * 2
		transport.MaxIdleConnsPerHost = workers
	}
}

// ManifestURL 把更新源转换为 manifest 地址。
func ManifestURL(base string) (string, error) {
	raw := strings.TrimSpace(base)
	if raw == "" {
		return "", fmt.Errorf("未配置更新地址")
	}
	u, err := validateRemoteURL(raw)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".json") {
		return u.String(), nil
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("更新地址不能包含查询参数")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.Path += "manifest.json"
	return u.String(), nil
}

func blobURL(base, sum string) (string, error) {
	u, err := validateRemoteURL(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("资源地址不能包含查询参数")
	}
	if !validSHA(sum) {
		return "", fmt.Errorf("blob sha256 非法: %s", sum)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/blobs/" + sum
	return u.String(), nil
}

func validateRemoteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("更新地址格式错误: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("更新地址必须使用 HTTP(S): %s", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("更新地址缺少主机名: %s", raw)
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("非本机更新地址必须使用 HTTPS: %s", raw)
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewDownloader 创建下载器。
func NewDownloader(base, dir string, progress func(done, total int64)) (*Downloader, error) {
	u, err := validateRemoteURL(strings.TrimSpace(base))
	if err != nil {
		return nil, err
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("资源地址不能包含查询参数")
	}
	if dir == "" {
		return nil, fmt.Errorf("下载目录不能为空")
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          defaultWorkers * 2,
		MaxIdleConnsPerHost:   defaultWorkers,
		ForceAttemptHTTP2:     true,
	}
	netproxy.Apply(transport)
	return &Downloader{
		Client: &http.Client{
			Timeout:   0,
			Transport: transport,
		},
		BaseURL:   base,
		Dir:       dir,
		Workers:   defaultWorkers,
		Segments:  defaultSegments,
		ChunkSize: fetch.MinSegmentSize,
		Progress:  progress,
	}, nil
}

// Fetch 下载全部 blob，已存在且校验通过的文件会被跳过。
func (d *Downloader) Fetch(ctx context.Context, blobs []Blob) error {
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return fmt.Errorf("创建下载目录失败: %w", err)
	}
	unique := make(map[string]int64, len(blobs))
	for _, b := range blobs {
		if !validSHA(b.Sha256) || b.Size < 0 {
			return fmt.Errorf("blob 信息非法: %s", b.Sha256)
		}
		if prev, ok := unique[b.Sha256]; ok && prev != b.Size {
			return fmt.Errorf("blob %s 大小冲突", b.Sha256)
		}
		unique[b.Sha256] = b.Size
	}

	var pending []Blob
	var baseline int64
	for sum, size := range unique {
		final := filepath.Join(d.Dir, sum)
		if verifyFile(final, size, sum) == nil {
			continue
		}
		_ = os.Remove(final)
		part := final + ".part"
		if fi, err := os.Stat(part); err == nil {
			if fi.Size() >= size {
				if fi.Size() == size && verifyFile(part, size, sum) == nil {
					if err := os.Rename(part, final); err == nil {
						continue
					}
				}
				_ = os.Remove(part)
			} else {
				baseline += fi.Size()
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		pending = append(pending, Blob{Sha256: sum, Size: size})
	}
	if len(pending) == 0 {
		return nil
	}

	tracker := &progressTracker{total: sumSizes(pending), cb: d.Progress}
	tracker.done.Store(baseline)
	tracker.emit(true)

	workers := d.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	if workers > len(pending) {
		workers = len(pending)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan Blob)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	worker := func() {
		defer wg.Done()
		for b := range jobs {
			if ctx.Err() != nil {
				return
			}
			if err := d.fetchOne(ctx, b, tracker); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				cancel()
				return
			}
		}
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}
	for _, b := range pending {
		select {
		case jobs <- b:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tracker.emit(true)
	return nil
}

func (d *Downloader) fetchOne(ctx context.Context, b Blob, tracker *progressTracker) error {
	final := filepath.Join(d.Dir, b.Sha256)
	part := final + ".part"
	link, err := blobURL(d.BaseURL, b.Sha256)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = d.fetchAttempt(ctx, link, b, final, part, tracker)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		backoff := time.Duration(300*(1<<attempt)) * time.Millisecond
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("下载 %s 失败: %w", b.Sha256, lastErr)
}

func (d *Downloader) chunkThreshold() int64 {
	if d.ChunkSize > 0 {
		return d.ChunkSize
	}
	return fetch.MinSegmentSize
}

// fetchChunked 用多条连接分片下载单个大 blob。
func (d *Downloader) fetchChunked(ctx context.Context, link string, b Blob, final, part string, tracker *progressTracker) error {
	if err := os.Remove(part); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(b.Size); err != nil {
		f.Close()
		_ = os.Remove(part)
		return err
	}

	var attempt atomic.Int64
	err = fetch.Segmented(ctx, d.Client, link, f, b.Size, d.Segments, func(n int64) {
		attempt.Add(n)
		if tracker != nil {
			tracker.add(n)
		}
	})
	if err != nil {
		f.Close()
		_ = os.Remove(part)
		if tracker != nil {
			tracker.add(-attempt.Load())
		}
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(part)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := verifyFile(part, b.Size, b.Sha256); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := os.Remove(final); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(part, final)
}

func (d *Downloader) fetchAttempt(ctx context.Context, link string, b Blob, final, part string, tracker *progressTracker) error {
	if verifyFile(final, b.Size, b.Sha256) == nil {
		return nil
	}
	if fi, err := os.Stat(part); err == nil {
		if fi.Size() > b.Size {
			if err := os.Remove(part); err != nil {
				return err
			}
		} else if fi.Size() == b.Size {
			if err := verifyFile(part, b.Size, b.Sha256); err == nil {
				_ = os.Remove(final)
				return os.Rename(part, final)
			}
			if err := os.Remove(part); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	offset := int64(0)
	if fi, err := os.Stat(part); err == nil {
		offset = fi.Size()
	}

	// 大文件且服务器支持时走多连接分片；不支持则回退到单连接续传。
	if offset == 0 && d.Segments > 1 && b.Size >= d.chunkThreshold() {
		err := d.fetchChunked(ctx, link, b, final, part, tracker)
		if err == nil {
			return nil
		}
		_ = os.Remove(part)
		if !errors.Is(err, fetch.ErrRangeUnsupported) {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "CDRLauncher/1.0")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if offset > 0 {
			if err := os.Remove(part); err != nil {
				return err
			}
			offset = 0
		}
	case http.StatusPartialContent:
		start, err := contentRangeStart(resp.Header.Get("Content-Range"))
		if err != nil {
			return err
		}
		if start != offset {
			return fmt.Errorf("服务器返回的 Range 起点不符: %d != %d", start, offset)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		if offset == b.Size {
			if err := verifyFile(part, b.Size, b.Sha256); err == nil {
				_ = os.Remove(final)
				return os.Rename(part, final)
			}
		}
		return fmt.Errorf("服务器拒绝续传: %s", resp.Status)
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	remaining := b.Size - offset
	written, copyErr := io.CopyBuffer(f, io.LimitReader(resp.Body, remaining+1), make([]byte, 128<<10))
	if copyErr != nil {
		f.Close()
		return copyErr
	}
	if written != remaining {
		f.Close()
		return fmt.Errorf("下载字节数不符: 收到 %d，需要 %d", written, remaining)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if tracker != nil {
		tracker.add(written)
	}
	if err := verifyFile(part, b.Size, b.Sha256); err != nil {
		return err
	}
	if err := os.Remove(final); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(part, final)
}

func contentRangeStart(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes ") {
		return 0, fmt.Errorf("缺少 Content-Range")
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

func sumSizes(blobs []Blob) int64 {
	var total int64
	for _, b := range blobs {
		total += b.Size
	}
	return total
}

type progressTracker struct {
	done  atomic.Int64
	total int64
	cb    func(done, total int64)
	mu    sync.Mutex
	last  time.Time
}

func (p *progressTracker) add(n int64) {
	if p == nil || p.cb == nil {
		p.done.Add(n)
		return
	}
	p.done.Add(n)
	p.emit(false)
}

func (p *progressTracker) emit(force bool) {
	if p == nil || p.cb == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !force && time.Since(p.last) < 200*time.Millisecond {
		return
	}
	p.last = time.Now()
	done := p.done.Load()
	if done > p.total {
		done = p.total
	}
	p.cb(done, p.total)
}
