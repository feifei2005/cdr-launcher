# Create: Delight Remake 启动器

Windows 整合包启动器。Go 负责启动、更新与文件操作，Wails v2 提供窗口与绑定，前端是 TypeScript + Vite 单页界面（WebView2 渲染）。

## 功能

- **实例识别**：读取 `versions/Create-Delight-Remake` 的 MC / Forge 版本、模组数量与整合包版本，配置失效时给出提示
- **Java 管理**：扫描本机 Java 21（PATH、常见安装目录、注册表），缺失时可一键下载；启动前校验主版本
- **启动游戏**：内存大小、natives 解包、工作目录与日志重定向；支持强制结束进程
- **账号体系**：Microsoft 正版登录（设备码）、离线登录、皮肤站 yggdrasil 登录；令牌用 Windows DPAPI 加密保存
- **便携实例**：默认在启动器所在目录创建 `.minecraft` 存放整合包，首次启动会自动检查并下载完整包
- **增量更新**：内置并锁定更新源 `https://mc-api.xn--suki-uf1gk54ba.cn/pack/`，按 `<updateUrl>/manifest.json` 做内容寻址的增量下载，推送进度事件、可取消；启动时自动恢复上次中断的更新
- **多线程下载**：默认 512 个文件并发、支持 HTTP/2 复用连接；单个大文件（≥8 MB）默认 32 连接做 Range 分片，服务端不支持时回退单连接续传；并发数与分片数可在设置中手动填写
- **启动器自更新**：检查内置清单 `pack/launcher/latest.json`，下载后校验大小与 sha256，替换自身并自动重启；所有更新地址写死在代码中，不在界面展示
- **备份与回滚**：更新前保留历史版本，可按时间戳回滚到任意备份
- **模组面板**：列出 mods 目录并启停（写入 overlay 记录，更新后仍生效）
- **日志与诊断**：查看 `latest.log` / 崩溃报告尾部；一键导出诊断包（日志 + 设置 + 实例信息）
- **系统代理**：登录、更新、Java 下载与游戏进程（JVM 代理参数）自动跟随 Windows 系统代理（含 Clash 等），遵循 `ProxyOverride` 例外列表
- **设置持久化**：`%APPDATA%\CDRLauncher\settings.json`

## 账号登录

在启动器「管理账号」面板中切换三种方式：

- **Microsoft 官方登录**：OAuth2 设备码流程，不需要内嵌浏览器或回调地址。Azure 应用 ID 已内置在代码中，玩家无需填写。
- **离线登录**：按昵称生成 `OfflinePlayer:<name>` 的 UUIDv3，服务器需为 `online-mode=false`。
- **皮肤站登录**：填 yggdrasil API 根地址（如 `https://littleskin.cn/api/yggdrasil`）和账号密码。启动时自动下载 `authlib-injector.jar` 并注入 `-javaagent`，皮肤与在线验证走该皮肤站。

令牌保存在 `%APPDATA%\CDRLauncher\accounts.dat`，使用 Windows DPAPI 按当前用户加密，换用户或换机器无法直接复制；皮肤站密码只用于换取令牌，不会落盘。

### 更换内置 Azure 应用（维护者）

1. 打开 [Azure 门户](https://portal.azure.com) → Microsoft Entra ID → 应用注册 → 新注册
2. 受支持的帐户类型选 **仅个人账户**
3. 重定向 URI 留空（设备码流程不需要）
4. 左侧「管理」→「清单」，把 `"allowPublicClient": null` 改成 `true` 并保存（新版门户的「身份验证」页里对应「允许公共客户端流」）
5. 把「应用程序(客户端) ID」写入 `internal/config/settings.go` 的 `DefaultMicrosoftClientID`，该值不会出现在界面或配置文件中
6. 到 [aka.ms/mce-reviewappid](https://aka.ms/mce-reviewappid) 提交该 Client ID 参加 Minecraft 第三方启动器审核；未通过时设备码与 Xbox 验证都能成功，但最后一步会返回 `Invalid app registration`

不需要客户端密码。设备码流程的 `XboxLive.signin` 走动态授权，但 Minecraft Services 仍要求应用通过上面的审核。

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
    --base-url https://mc-api.example.cn/pack/ --out dist/stable
cdrpub diff --from-root old --to-root new --from 5.2.0 --to 5.3.0 ^
    --out dist/stable --manifest dist/stable/manifest.json
cdrpub verify --dir dist/stable
cdrpub upload --dir dist/stable --bucket cdr-pack --prefix pack/ ^
    --public-base https://mc-api.example.cn/pack --skip-existing
cdrpub serve --dir dist --addr 127.0.0.1:8080
```

`upload` 直接走 Cloudflare R2 的 REST API（Bearer Token，读 `CF_API_TOKEN` / `CF_ACCOUNT_ID` 或 `--token` / `--account-id`），
并发默认 8，单对象失败最多重试 3 次；`--skip-existing` 会先列举桶内对象，只补传新增或大小变化的文件。
`manifest.json` 里的 `baseUrl` 就是启动器实际拼接 `blobs/<sha256>` 的地址，生成时必须填线上地址。

### 发布启动器自身更新

启动器版本号写死在 `app.go` 的 `appVersion`，发版前先改这个常量。自更新清单固定读取
`https://mc-api.xn--suki-uf1gk54ba.cn/pack/launcher/latest.json`，格式：

```json
{
  "version": "1.0.1",
  "publishedAt": "2026-10-26T12:00:00Z",
  "notes": "修复若干问题",
  "url": "cdr-launcher-1.0.1.exe",
  "sha256": "<64 位十六进制>",
  "size": 12345678
}
```

发布步骤：

```powershell
wails build
# 把 build/bin/cdr-launcher.exe 重命名为 cdr-launcher-1.0.1.exe 放进 dist/launcher
Get-FileHash dist/launcher/cdr-launcher-1.0.1.exe -Algorithm SHA256   # 填到 sha256，size 取文件字节数
# 在 dist/launcher/latest.json 写入清单，version 必须大于 appVersion
cdrpub upload --dir dist/launcher --bucket cdr-pack --prefix pack/launcher
```

`url` 可以写相对 `latest.json` 的文件名，也可以写完整 HTTPS 地址；启动器下载后校验大小与 sha256，
替换时先把旧程序改名为 `.old`，新进程启动成功后自动退出，下次启动清理 `.old` / `.new` 残留。
清单缺失（404）时视为无更新，不会打扰用户。

## 目录结构

```
app.go             Wails 绑定层（前端可调用的方法、状态聚合）
main.go            窗口与资源嵌入
open.go            打开目录 / 选择目录等系统交互
internal/config    设置读写、默认值与校验
internal/auth      微软设备码登录、皮肤站 yggdrasil、DPAPI 账号存储、authlib-injector 下载
internal/launcher  Java 扫描与下载、启动参数、natives、进程管理
internal/netproxy  读取 Windows 系统代理（注册表 + 环境变量 + 例外列表）
internal/fetch     多连接 Range 分片下载
internal/manifest  manifest 类型、内容寻址 blob、差分补丁
internal/updater   更新计划、下载、安装、备份与回滚
internal/selfupdate 启动器自更新（检查清单、下载校验、替换自身）
internal/mods      模组启停与 overlay 记录
internal/logs      日志尾部读取、诊断包导出
cmd/cdrpub         发布工具（manifest / diff / verify / serve）
frontend/          Vite + TypeScript 界面与 Wails 生成的绑定
```

## 说明

- 仅支持 Windows（依赖 WebView2 与 Windows 进程控制）。
- 代理优先读环境变量，其次读 `HKCU\...\Internet Settings` 的 `ProxyEnable` / `ProxyServer` / `ProxyOverride`；仅启用 PAC（自动配置脚本）时启动器按直连处理并在设置中提示，建议在代理软件里开启全局/TUN 模式。
- 游戏进程会追加 `-Djava.net.useSystemProxies=true` 与显式 `http/https/socksProxyHost` 参数，游戏内的登录、皮肤等 HTTP(S) 请求同样走代理；多人游戏服务器的直连 TCP 不经过 JVM 代理，需要代理软件开启 TUN/全局模式。
- 下载并发默认 512（可填 1–1024），大文件分片默认 32（可填 1–64）；分片依赖服务端 `Range` 支持，R2/Worker 已开启，遇到不支持的服务端会自动回退单连接。
- `dist/`、`build/bin/`、`frontend/dist/` 属于本地构建与发布产物，不纳入版本控制。
- 单测覆盖参数组装、Java 版本判定、manifest/差分、更新安装与回滚、模组、日志、账号与皮肤站地址逻辑；启动游戏、微软登录、皮肤站登录与更新安装等端到端流程尚未在真机完整验证。
