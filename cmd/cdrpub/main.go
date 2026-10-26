// cdrpub 是 Create-Delight-Remake 的发布工具：生成 manifest、差分补丁、
// 校验产物并提供本地测试源。上传/刷新 CDN 缓存留待部署阶段接入。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cdr-launcher/internal/manifest"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "manifest":
		err = runManifest(os.Args[2:])
	case "diff":
		err = runDiff(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "upload":
		err = runUpload(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		err = fmt.Errorf("未知命令: %s", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法: cdrpub <命令> [参数]

  manifest  生成版本清单与内容寻址 blob
  diff      生成 zstd 差分补丁
  verify    校验清单与 blob 完整性
  serve     启动本地 HTTP 测试源
  upload    把发布目录上传到 Cloudflare R2（并发、重试、可跳过已存在）

示例:
  cdrpub manifest --root "versions/Create-Delight-Remake" --version 5.3.0 \
      --base-url https://mc-api.example.cn/pack/ --out dist/stable
  cdrpub diff --from-root old --to-root new --from 5.2.0 --to 5.3.0 \
      --out dist/stable --manifest dist/stable/manifest.json
  cdrpub verify --dir dist/stable
  cdrpub upload --dir dist/stable --bucket cdr-pack --prefix pack/ \
      --public-base https://mc-api.example.cn/pack --skip-existing
  cdrpub serve --dir dist --addr 127.0.0.1:8080`)
}

func runManifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	root := fs.String("root", "", "整合包实例目录（版本隔离时为 versions/<id>）")
	version := fs.String("version", "", "版本号，例如 5.3.0")
	channel := fs.String("channel", "stable", "版本通道")
	baseURL := fs.String("base-url", "", "CDN 基础地址，例如 https://dl.example.com/pack/")
	out := fs.String("out", "", "输出目录，默认 dist/<channel>")
	packID := fs.String("pack-id", manifest.DefaultPackID, "整合包 ID")
	mc := fs.String("mc", "1.20.1", "Minecraft 版本")
	forge := fs.String("forge", "47.4.23", "Forge 版本")
	javaMajor := fs.Int("java-major", 21, "目标 Java 主版本")
	minLauncher := fs.String("min-launcher", "", "最低启动器版本")
	previous := fs.String("previous", "", "上一版 manifest，用于计算 deletes")
	patchesFile := fs.String("patches", "", "补丁列表 JSON（由 cdrpub diff 生成）")
	noBlobs := fs.Bool("no-blobs", false, "只生成清单，不写入 blob")
	var exclude stringList
	fs.Var(&exclude, "exclude", "额外排除的 glob，可重复")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *root == "" || *version == "" {
		return errors.New("--root 与 --version 必填")
	}
	if *baseURL == "" {
		return errors.New("--base-url 必填")
	}
	if *out == "" {
		*out = filepath.Join("dist", *channel)
	}
	blobDir := filepath.Join(*out, "blobs")

	fmt.Printf("扫描 %s\n", *root)
	files, err := manifest.Build(manifest.BuildOptions{
		Root:      *root,
		Exclude:   exclude,
		BlobsDir:  blobDir,
		SkipBlobs: *noBlobs,
	})
	if err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}

	m := &manifest.Manifest{
		Format:       manifest.FormatVersion,
		PackID:       *packID,
		Channel:      *channel,
		Version:      *version,
		PublishedAt:  time.Now().UTC().Format(time.RFC3339),
		MinLauncher:  *minLauncher,
		BaseURL:      strings.TrimRight(*baseURL, "/") + "/",
		Game:         manifest.GameInfo{MC: *mc, Forge: *forge, JavaMajor: *javaMajor},
		Files:        files,
		Protected:    manifest.DefaultProtected,
		UserEditable: manifest.DefaultUserEditable,
	}

	if *patchesFile != "" {
		raw, err := os.ReadFile(*patchesFile)
		if err != nil {
			return fmt.Errorf("读取补丁列表失败: %w", err)
		}
		var patches []manifest.Patch
		if err := json.Unmarshal(raw, &patches); err != nil {
			return fmt.Errorf("解析补丁列表失败: %w", err)
		}
		m.Patches = patches
	}
	if *previous != "" {
		prev, err := manifest.Load(*previous)
		if err != nil {
			return err
		}
		m.Deletes = manifest.Deletes(prev.Files, files)
	}

	if err := m.Write(filepath.Join(*out, "manifest.json")); err != nil {
		return err
	}
	if err := m.Write(filepath.Join(*out, "manifest-"+*version+".json")); err != nil {
		return err
	}

	mode := "已写入 blob"
	if *noBlobs {
		mode = "未写入 blob"
	}
	fmt.Printf("完成: %d 个文件，%.1f MiB，deletes=%d，patches=%d，%s\n",
		len(files), float64(total)/(1<<20), len(m.Deletes), len(m.Patches), mode)
	fmt.Printf("输出: %s\n", filepath.Join(*out, "manifest.json"))
	return nil
}

func runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fromRoot := fs.String("from-root", "", "旧版本实例目录")
	toRoot := fs.String("to-root", "", "新版本实例目录")
	from := fs.String("from", "", "旧版本号")
	to := fs.String("to", "", "新版本号")
	out := fs.String("out", "dist", "输出目录，补丁写入 <out>/blobs")
	minSize := fs.Int64("min-size", manifest.DefaultMinPatchSize, "参与差分的最小旧文件字节数")
	maxRatio := fs.Float64("max-ratio", manifest.DefaultMaxPatchRatio, "补丁体积上限（相对新文件）")
	manifestFile := fs.String("manifest", "", "可选：把补丁写入该 manifest")
	var exclude stringList
	fs.Var(&exclude, "exclude", "额外排除的 glob，可重复")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromRoot == "" || *toRoot == "" || *from == "" || *to == "" {
		return errors.New("--from-root、--to-root、--from、--to 必填")
	}
	blobDir := filepath.Join(*out, "blobs")
	patches, deletes, err := manifest.Diff(manifest.DiffOptions{
		FromRoot: *fromRoot,
		ToRoot:   *toRoot,
		From:     *from,
		To:       *to,
		BlobsDir: blobDir,
		Exclude:  exclude,
		MinSize:  *minSize,
		MaxRatio: *maxRatio,
	})
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(patches, "", "  ")
	if err != nil {
		return err
	}
	outFile := filepath.Join(*out, "patches-"+*from+"-"+*to+".json")
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outFile, append(raw, '\n'), 0o644); err != nil {
		return err
	}

	var total int64
	for _, p := range patches {
		total += p.Size
	}
	fmt.Printf("补丁: %d 个，共 %.1f MiB；删除: %d；列表: %s\n",
		len(patches), float64(total)/(1<<20), len(deletes), outFile)

	if *manifestFile != "" {
		m, err := manifest.Load(*manifestFile)
		if err != nil {
			return err
		}
		m.Patches = patches
		if len(deletes) > 0 {
			m.Deletes = deletes
		}
		if err := m.Write(*manifestFile); err != nil {
			return err
		}
		fmt.Printf("已更新 %s\n", *manifestFile)
	}
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	dir := fs.String("dir", "", "发布目录（含 manifest.json 与 blobs）")
	manifestPath := fs.String("manifest", "", "manifest 路径；默认 <dir>/manifest.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" {
		if *dir == "" {
			return errors.New("--dir 或 --manifest 必填")
		}
		*manifestPath = filepath.Join(*dir, "manifest.json")
	}
	if *dir == "" {
		*dir = filepath.Dir(*manifestPath)
	}
	m, err := manifest.Load(*manifestPath)
	if err != nil {
		return err
	}
	problems := manifest.Verify(m, filepath.Join(*dir, "blobs"))
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "-", p)
		}
		return fmt.Errorf("校验失败: %d 个问题", len(problems))
	}
	fmt.Printf("校验通过: %s %s，%d 个文件，%d 个补丁\n",
		m.PackID, m.Version, len(m.Files), len(m.Patches))
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dir := fs.String("dir", "dist", "发布根目录")
	addr := fs.String("addr", "127.0.0.1:8080", "监听地址")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info, err := os.Stat(*dir)
	if err != nil {
		return fmt.Errorf("目录不可访问: %w", err)
	}
	if !info.IsDir() {
		return errors.New("--dir 不是目录")
	}
	root := http.Dir(*dir)
	fileServer := http.FileServer(root)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "manifest.json"):
			w.Header().Set("Cache-Control", "no-store")
		case strings.Contains(r.URL.Path, "/blobs/"), strings.Contains(r.URL.Path, "/patches/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Printf("本地源: http://%s/ （目录 %s）\n", *addr, *dir)
	fmt.Println("按 Ctrl+C 停止")
	return srv.ListenAndServe()
}
