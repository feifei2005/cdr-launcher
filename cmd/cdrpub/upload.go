package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cdr-launcher/internal/netproxy"
)

const r2APIBase = "https://api.cloudflare.com/client/v4"

type uploadFile struct {
	rel  string
	key  string
	size int64
}

type httpStatusError struct {
	code int
	body string
}

func (e *httpStatusError) Error() string {
	body := strings.TrimSpace(e.body)
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	return fmt.Sprintf("HTTP %d: %s", e.code, body)
}

type r2Object struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

type r2ListResponse struct {
	Success bool       `json:"success"`
	Result  []r2Object `json:"result"`
	Info    struct {
		Cursor      string `json:"cursor"`
		IsTruncated bool   `json:"is_truncated"`
	} `json:"result_info"`
	Errors []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func runUpload(args []string) error {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	dir := fs.String("dir", "dist/stable", "发布目录（含 manifest.json 与 blobs/）")
	bucket := fs.String("bucket", "", "R2 桶名")
	prefix := fs.String("prefix", "pack/", "对象键前缀")
	accountID := fs.String("account-id", os.Getenv("CF_ACCOUNT_ID"), "Cloudflare 账号 ID（或环境变量 CF_ACCOUNT_ID）")
	token := fs.String("token", os.Getenv("CF_API_TOKEN"), "Cloudflare API Token（或环境变量 CF_API_TOKEN）")
	workers := fs.Int("concurrency", 4, "并发上传数（Cloudflare API 限速约 4 请求/秒，调高无益）")
	retries := fs.Int("retry", 5, "单个对象失败重试次数")
	skipExisting := fs.Bool("skip-existing", false, "跳过桶中已存在且大小一致的对象")
	dryRun := fs.Bool("dry-run", false, "只统计文件，不上传")
	publicBase := fs.String("public-base", "", "可选：上传后用于提示的访问地址前缀")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bucket == "" {
		return errors.New("--bucket 必填")
	}
	if !*dryRun && (*accountID == "" || *token == "") {
		return errors.New("--account-id 与 --token 必填（或设置 CF_ACCOUNT_ID / CF_API_TOKEN）")
	}
	if *workers < 1 {
		*workers = 1
	}
	info, err := os.Stat(*dir)
	if err != nil {
		return fmt.Errorf("目录不可访问: %w", err)
	}
	if !info.IsDir() {
		return errors.New("--dir 不是目录")
	}

	files, total, err := collectUploads(*dir, *prefix)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("目录下没有可上传的文件")
	}
	fmt.Printf("待上传 %d 个文件，共 %.1f MiB → r2://%s/%s\n",
		len(files), float64(total)/(1<<20), *bucket, *prefix)

	if *dryRun {
		for _, f := range files[:min(len(files), 5)] {
			fmt.Printf("  %s -> %s (%d B)\n", f.rel, f.key, f.size)
		}
		if len(files) > 5 {
			fmt.Printf("  … 其余 %d 个\n", len(files)-5)
		}
		return nil
	}

	bucketURL := fmt.Sprintf("%s/accounts/%s/r2/buckets/%s", r2APIBase, *accountID, *bucket)
	transport := &http.Transport{
		MaxIdleConns:        *workers * 2,
		MaxIdleConnsPerHost: *workers * 2,
		MaxConnsPerHost:     *workers * 2,
		IdleConnTimeout:     60 * time.Second,
	}
	netproxy.Apply(transport)
	client := &http.Client{Transport: transport}

	if *skipExisting {
		existing, err := listObjects(client, bucketURL, *token, *prefix)
		if err != nil {
			return fmt.Errorf("列举已有对象失败: %w", err)
		}
		kept := files[:0]
		var skipped, skippedBytes int64
		for _, f := range files {
			if size, ok := existing[f.key]; ok && size == f.size {
				skipped++
				skippedBytes += f.size
				continue
			}
			kept = append(kept, f)
		}
		files = kept
		total -= skippedBytes
		fmt.Printf("桶中已存在 %d 个（%.1f MiB），实际待传 %d 个（%.1f MiB）\n",
			skipped, float64(skippedBytes)/(1<<20), len(files), float64(total)/(1<<20))
		if len(files) == 0 {
			fmt.Println("没有需要上传的对象")
			return nil
		}
	}

	var (
		index     int64
		doneFiles int64
		doneBytes int64
		mu        sync.Mutex
		failures  []string
		wg        sync.WaitGroup
	)
	ctx := context.Background()
	started := time.Now()

	stopProgress := make(chan struct{})
	go func() {
		if !isTerminal(os.Stdout) {
			<-stopProgress
			return
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopProgress:
				fmt.Print("\r\033[K")
				return
			case <-ticker.C:
				f := atomic.LoadInt64(&doneFiles)
				b := atomic.LoadInt64(&doneBytes)
				pct := 0.0
				if total > 0 {
					pct = float64(b) * 100 / float64(total)
				}
				fmt.Printf("\r上传 %d/%d (%.1f%%) %.1f/%.1f MiB", f, len(files), pct, float64(b)/(1<<20), float64(total)/(1<<20))
			}
		}
	}()

	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n := int(atomic.AddInt64(&index, 1)) - 1
				if n >= len(files) {
					return
				}
				f := files[n]
				endpoint := bucketURL + "/objects/" + encodeKey(f.key)
				if err := putWithRetry(ctx, client, endpoint, *token, filepath.Join(*dir, filepath.FromSlash(f.rel)), f.size, *retries); err != nil {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("%s: %v", f.key, err))
					mu.Unlock()
					continue
				}
				atomic.AddInt64(&doneFiles, 1)
				atomic.AddInt64(&doneBytes, f.size)
			}
		}()
	}
	wg.Wait()
	close(stopProgress)

	elapsed := time.Since(started)
	uploadedFiles := atomic.LoadInt64(&doneFiles)
	uploadedBytes := atomic.LoadInt64(&doneBytes)
	rate := float64(uploadedBytes) / (1 << 20) / elapsed.Seconds()
	fmt.Printf("完成: %d/%d 个，%.1f MiB，用时 %.1fs（%.1f MiB/s）\n",
		uploadedFiles, len(files), float64(uploadedBytes)/(1<<20), elapsed.Seconds(), rate)

	if *publicBase != "" {
		base := strings.TrimRight(*publicBase, "/")
		fmt.Printf("访问地址: %s/manifest.json，blob 形如 %s/blobs/<sha256>\n", base, base)
	}
	if len(failures) > 0 {
		for _, f := range failures[:min(len(failures), 20)] {
			fmt.Fprintln(os.Stderr, "-", f)
		}
		if len(failures) > 20 {
			fmt.Fprintf(os.Stderr, "… 另有 %d 个失败\n", len(failures)-20)
		}
		return fmt.Errorf("%d 个对象上传失败", len(failures))
	}
	return nil
}

func collectUploads(dir, prefix string) ([]uploadFile, int64, error) {
	prefix = strings.Trim(prefix, "/")
	var files []uploadFile
	var total int64
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		key := rel
		if prefix != "" {
			key = prefix + "/" + rel
		}
		files = append(files, uploadFile{rel: rel, key: key, size: info.Size()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, total, nil
}

func putWithRetry(ctx context.Context, client *http.Client, endpoint, token, path string, size int64, retries int) error {
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		last = putObject(attemptCtx, client, endpoint, token, path, size)
		cancel()
		if last == nil {
			return nil
		}
		if !retryable(last) {
			return last
		}
		if attempt < retries {
			// 指数退避 + 抖动，避免多个 worker 同时重试再次撞限速
			delay := time.Duration(1<<attempt) * 2 * time.Second
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			time.Sleep(delay/2 + time.Duration(rand.Int63n(int64(delay))))
		}
	}
	return last
}

func putObject(ctx context.Context, client *http.Client, endpoint, token, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return &httpStatusError{code: resp.StatusCode, body: string(body)}
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return nil
}

func retryable(err error) bool {
	var se *httpStatusError
	if errors.As(err, &se) {
		// 429/408 是限速与超时；400 是 Cloudflare 边缘偶发拒收（重试可过）
		return se.code == http.StatusTooManyRequests || se.code == http.StatusRequestTimeout ||
			se.code == http.StatusBadRequest || se.code >= 500
	}
	return true
}

func listObjects(client *http.Client, bucketURL, token, prefix string) (map[string]int64, error) {
	keys := make(map[string]int64)
	cursor := ""
	for {
		q := url.Values{}
		q.Set("prefix", strings.Trim(prefix, "/")+"/")
		q.Set("per_page", "1000")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		req, err := http.NewRequest(http.MethodGet, bucketURL+"/objects?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			return nil, &httpStatusError{code: resp.StatusCode, body: string(raw)}
		}
		var parsed r2ListResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("解析对象列表失败: %w", err)
		}
		if !parsed.Success {
			return nil, fmt.Errorf("列举对象失败: %v", parsed.Errors)
		}
		for _, o := range parsed.Result {
			keys[o.Key] = o.Size
		}
		if !parsed.Info.IsTruncated || parsed.Info.Cursor == "" {
			break
		}
		cursor = parsed.Info.Cursor
	}
	return keys, nil
}

func encodeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
