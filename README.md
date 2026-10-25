# Create: Delight Remake 启动器

Windows 整合包启动器。Go 负责启动、更新与文件操作，Wails v2 提供窗口与绑定，前端是 TypeScript + Vite 单页界面（WebView2 渲染）。

## 功能

- **实例识别**：读取 `versions/Create-Delight-Remake` 的 MC / Forge 版本、模组数量与整合包版本，配置失效时给出提示
- **Java 管理**：扫描本机 Java 21（PATH、常见安装目录、注册表），缺失时可一键下载；启动前校验主版本
- **启动游戏**：内存大小、离线昵称、natives 解包、工作目录与日志重定向；支持强制结束进程
- **增量更新**：按 `<updateUrl>/manifest.json` 做内容寻址的增量下载，推送进度事件、可取消；启动时自动恢复上次中断的更新
- **备份与回滚**：更新前保留历史版本，可按时间戳回滚到任意备份
- **模组面板**：列出 mods 目录并启停（写入 overlay 记录，更新后仍生效）
- **日志与诊断**：查看 `latest.log` / 崩溃报告尾部；一键导出诊断包（日志 + 设置 + 实例信息）
- **设置持久化**：`%APPDATA%\CDRLauncher\settings.json`

## 开发

依赖：Go 1.27+、Node 18+、Wails CLI v2、WebView2 Runtime。

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest

wails dev        # 开发模式（前端热重载）
wails build      # 产物：build/bin/cdr-launcher.exe
go test ./...    # 单元测试
go vet ./...
```

## 发布更新源

`cmd/cdrpub` 是配套发布工具，生成版本清单、内容寻址 blob 与 zstd 差分补丁，并可起本地测试源：

```powershell
go build -o dist/cdrpub.exe ./cmd/cdrpub

cdrpub manifest --root "versions/Create-Delight-Remake" --version 5.3.0 ^
    --base-url https://dl.example.com/pack/ --out dist/stable
cdrpub diff --from-root old --to-root new --from 5.2.0 --to 5.3.0 ^
    --out dist/stable --manifest dist/stable/manifest.json
cdrpub verify --dir dist/stable
cdrpub serve --dir dist --addr 127.0.0.1:8080
```

把 `dist/stable` 整目录上传到 `updateUrl` 指向的静态站点即可；启动器读取其中的 `manifest.json`。

## 目录结构

```
app.go             Wails 绑定层（前端可调用的方法、状态聚合）
main.go            窗口与资源嵌入
open.go            打开目录 / 选择目录等系统交互
internal/config    设置读写、默认值与校验
internal/launcher  Java 扫描与下载、启动参数、natives、进程管理
internal/manifest  manifest 类型、内容寻址 blob、差分补丁
internal/updater   更新计划、下载、安装、备份与回滚
internal/mods      模组启停与 overlay 记录
internal/logs      日志尾部读取、诊断包导出
cmd/cdrpub         发布工具（manifest / diff / verify / serve）
frontend/          Vite + TypeScript 界面与 Wails 生成的绑定
```

## 说明

- 仅支持 Windows（依赖 WebView2 与 Windows 进程控制）。
- `dist/`、`build/bin/`、`frontend/dist/` 属于本地构建与发布产物，不纳入版本控制。
- 单测覆盖参数组装、Java 版本判定、manifest/差分、更新安装与回滚、模组与日志逻辑；启动游戏、更新安装等端到端流程尚未在真机完整验证。
