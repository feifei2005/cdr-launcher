import './style.css';
import './app.css';

import {
    CancelMicrosoftLogin,
    CancelUpdate,
    CheckLauncherUpdate,
    CheckUpdate,
    DownloadJava,
    ExportDiagnostics,
    GetState,
    InstallLauncherUpdate,
    InstallUpdate,
    KillGame,
    ListBackups,
    ListMods,
    LoginYggdrasil,
    OpenFolder,
    PickDirectory,
    RemoveAccount,
    RescanJava,
    Rollback,
    SaveSettings,
    SetModEnabled,
    StartGame,
    StartMicrosoftLogin,
    SwitchAccount,
    TailLog
} from '../wailsjs/go/main/App';
import {BrowserOpenURL, ClipboardSetText, EventsOn} from '../wailsjs/runtime/runtime';

interface SettingsView {
    instanceDir: string;
    javaPath: string;
    playerName: string;
    memoryMb: number;
    channel: string;
    yggdrasilServer: string;
    downloadWorkers: number;
    downloadSegments: number;
}

interface AccountView {
    id: string;
    kind: 'microsoft' | 'yggdrasil' | string;
    name: string;
    uuid: string;
    serverUrl?: string;
    expiresAt?: string;
}

interface DeviceCodeView {
    userCode: string;
    verificationUri: string;
    message: string;
    expiresIn: number;
}

interface JavaView {
    path: string;
    version: string;
    major: number;
    source: string;
    compatible: boolean;
}

interface InstanceView {
    path: string;
    gameDir: string;
    versionId: string;
    mcVersion: string;
    forgeVersion: string;
    modCount: number;
    packVersion: string;
    error?: string;
}

interface StateView {
    settings: SettingsView;
    java: JavaView[];
    javaScanning: boolean;
    javaError?: string;
    instance: InstanceView;
    accounts: AccountView[];
    accountId: string;
    microsoftLoggingIn: boolean;
    proxy: string;
    launcherVersion: string;
    launcherUpdating: boolean;
    running: boolean;
    updating: boolean;
    rollingBack: boolean;
}

interface ModView {
    name: string;
    enabled: boolean;
    size: number;
    sha256?: string;
}

interface LogView {
    kind: string;
    path: string;
    size: number;
    modifiedAt: string;
    lines: string[];
    truncated: boolean;
    message?: string;
}

interface DiagnosticsView {
    path: string;
    size: number;
}

interface BackupView {
    stamp: string;
    from: string;
    to: string;
    createdAt: string;
    files: number;
    deletes: number;
    bytes: number;
    canRollback: boolean;
}

interface RollbackResult {
    from: string;
    to: string;
    restored: number;
    removed: number;
}

interface UpdateInfo {
    available: boolean;
    upToDate: boolean;
    currentVersion: string;
    latestVersion: string;
    publishedAt?: string;
    changedFiles: number;
    patches: number;
    deletes: number;
    downloadBytes: number;
}

interface UpdateProgress {
    phase: string;
    done: number;
    total: number;
    filesDone: number;
    filesTotal: number;
    message?: string;
}

interface UpdateResult {
    version: string;
    updated: boolean;
    files: number;
    patches: number;
    deletes: number;
    downloadBytes: number;
}

const MEMORY_PRESETS = [4096, 6144, 8192, 12288, 16384];

const $ = <T extends HTMLElement>(selector: string): T => document.querySelector(selector) as T;

document.querySelector('#app')!.innerHTML = `
<div class="shell">
  <header class="hero">
    <div class="hero-glow"></div>
    <div class="hero-copy">
      <p class="eyebrow">CREATE · DELIGHT · REMAKE</p>
      <h1>Create: Delight Remake</h1>
      <p class="hero-meta">
        <span id="mc-version">1.20.1</span>
        <span class="dot"></span> Forge <span id="forge-version">47.4.23</span>
        <span class="dot"></span> <span id="mod-count">--</span> 个模组
      </p>
    </div>
    <div class="hero-badge" id="pack-badge">读取中</div>
  </header>

  <main class="layout">
    <section class="panel launch-panel">
      <div class="field">
        <div class="field-head">
          <label>登录账号</label>
          <button id="open-account" class="link-btn" type="button">管理账号</button>
        </div>
        <div class="account-card" id="account-card">
          <div class="account-avatar" id="account-avatar">P</div>
          <div class="account-main">
            <strong id="account-name">Player</strong>
            <small id="account-kind">离线登录</small>
          </div>
          <span class="account-tag" id="account-tag">离线</span>
        </div>
        <input id="player-name" class="text-input offline-name" maxlength="16" autocomplete="off" spellcheck="false" placeholder="离线昵称：3-16 位字母、数字或下划线"/>
        <small class="hint" id="account-hint">离线登录只在本机生效，服务器需为离线模式</small>
      </div>

      <div class="field">
        <div class="field-head">
          <label>Java 运行时</label>
          <button id="rescan" class="link-btn" type="button">重新检测</button>
        </div>
        <div id="java-status" class="status-box">
          <span class="status-dot pending"></span>
          <div class="status-copy">
            <strong>正在检测 Java…</strong>
            <small>扫描内置运行时与系统安装</small>
          </div>
        </div>
        <div id="java-progress" class="progress" hidden>
          <div class="progress-bar" id="java-bar"></div>
          <span class="progress-text" id="java-progress-text">0%</span>
        </div>
        <button id="download-java" class="ghost-btn" type="button" hidden>下载内置 Java 21（约 47 MB）</button>
      </div>

      <div class="field">
        <label>内存分配</label>
        <div class="memory-row" id="memory-row">
          ${MEMORY_PRESETS.map(mb => `<button class="chip" type="button" data-mb="${mb}">${mb / 1024} GB</button>`).join('')}
        </div>
      </div>

      <button id="launch" class="launch-btn" type="button">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5.14v13.72a1 1 0 0 0 1.5.87l11-6.86a1 1 0 0 0 0-1.72l-11-6.86A1 1 0 0 0 8 5.14z"/></svg>
        <span>开始游戏</span>
      </button>
      <button id="kill-game" class="ghost-btn wide danger" type="button" hidden>强制结束游戏</button>
      <p id="message" class="message" hidden></p>
    </section>

    <aside class="panel info-panel">
      <h2>实例信息</h2>
      <dl class="facts">
        <div><dt>整合包</dt><dd id="pack-name">Create-Delight-Remake</dd></div>
        <div><dt>版本</dt><dd id="info-pack-version">--</dd></div>
        <div><dt>Minecraft</dt><dd id="info-mc">--</dd></div>
        <div><dt>Forge</dt><dd id="info-forge">--</dd></div>
        <div><dt>模组数量</dt><dd id="info-mods">--</dd></div>
      </dl>
      <div class="path-box" id="instance-path">--</div>
      <p id="instance-error" class="inline-error" hidden></p>
      <div class="update-box">
        <div class="update-head">
          <span class="update-title">整合包更新</span>
          <button id="check-update" class="link-btn" type="button">检查更新</button>
        </div>
        <p id="update-status" class="update-status">尚未检查</p>
        <div id="update-progress" class="progress" hidden>
          <div class="progress-bar" id="update-bar"></div>
          <span class="progress-text" id="update-progress-text">0%</span>
        </div>
        <button id="install-update" class="ghost-btn wide" type="button" hidden>安装更新</button>
        <button id="cancel-update" class="link-btn" type="button" hidden>取消更新</button>
      </div>
      <button id="open-settings" class="ghost-btn wide" type="button">启动器设置</button>
      <div class="tool-row">
        <button id="open-mods" class="ghost-btn" type="button">模组</button>
        <button id="open-logs" class="ghost-btn" type="button">日志</button>
        <button id="open-history" class="ghost-btn" type="button">版本历史</button>
      </div>
    </aside>
  </main>

  <footer class="footer">
    <span id="footer-account">离线登录 · 首次启动会自动补齐缺失的库文件</span>
    <span id="footer-state">就绪</span>
  </footer>
</div>

<div class="drawer-mask" id="drawer-mask" hidden></div>
<section class="drawer" id="drawer" hidden aria-label="启动器设置">
  <header>
    <h2>启动器设置</h2>
    <button id="close-settings" class="icon-btn" type="button" aria-label="关闭">×</button>
  </header>
  <div class="drawer-body">
    <div class="field">
      <label for="instance-dir">实例目录</label>
      <div class="input-row">
        <input id="instance-dir" class="text-input mono" spellcheck="false"/>
        <button id="pick-instance" class="ghost-btn" type="button">浏览…</button>
      </div>
      <small class="hint">包含 versions、mods、libraries 的 .minecraft 目录</small>
    </div>
    <div class="field">
      <label for="java-select">Java 运行时</label>
      <select id="java-select" class="text-input"></select>
      <small class="hint">自动模式优先使用 Java 21，缺失时下载内置版本</small>
    </div>
    <div class="field">
      <label>下载并发</label>
      <div class="input-row">
        <input id="download-workers" class="text-input" type="number" min="1" max="1024" step="1" inputmode="numeric"/>
        <input id="download-segments" class="text-input" type="number" min="1" max="64" step="1" inputmode="numeric"/>
      </div>
      <small class="hint">可手动填写：文件并发 1–1024，大文件分片 1–64（≥8 MB 才分片）</small>
    </div>
    <div class="field">
      <label>启动器版本</label>
      <div class="input-row">
        <div class="readonly mono" id="launcher-version">v--</div>
        <button id="check-launcher" class="ghost-btn" type="button">检查更新</button>
      </div>
      <small class="hint" id="launcher-update-hint">更新源已内置，更新后会自动重启</small>
    </div>
    <div class="field">
      <label>版本通道</label>
      <div class="readonly" id="channel-value">stable</div>
    </div>
    <div class="field">
      <label>网络代理</label>
      <div class="readonly" id="proxy-value">检测中…</div>
      <small class="hint">自动跟随 Windows 系统代理，用于登录、更新、Java 下载与游戏进程</small>
    </div>
  </div>
  <footer class="drawer-footer">
    <button id="cancel-settings" class="ghost-btn" type="button">取消</button>
    <button id="save-settings" class="launch-btn compact" type="button">保存设置</button>
  </footer>
</section>

<div class="sheet-mask" id="sheet-mask" hidden></div>

<section class="sheet" id="mods-sheet" hidden aria-label="模组管理">
  <header class="sheet-head">
    <div>
      <h2>模组管理</h2>
      <p class="sheet-sub" id="mods-summary">加载中…</p>
    </div>
    <div class="sheet-head-actions">
      <button id="mods-folder" class="link-btn" type="button">打开文件夹</button>
      <button class="icon-btn" type="button" data-close-sheet aria-label="关闭">×</button>
    </div>
  </header>
  <div class="sheet-toolbar">
    <input id="mods-search" class="text-input" placeholder="搜索模组名称…" autocomplete="off" spellcheck="false"/>
    <div class="segmented" id="mods-filter">
      <button class="active" type="button" data-filter="all">全部</button>
      <button type="button" data-filter="enabled">已启用</button>
      <button type="button" data-filter="disabled">已禁用</button>
    </div>
  </div>
  <p id="mods-message" class="sheet-status" hidden></p>
  <div class="sheet-body">
    <div id="mods-list" class="mod-list"></div>
  </div>
</section>

<section class="sheet" id="logs-sheet" hidden aria-label="日志">
  <header class="sheet-head">
    <div>
      <h2>日志</h2>
      <p class="sheet-sub" id="log-meta">--</p>
    </div>
    <div class="sheet-head-actions">
      <button id="log-refresh" class="link-btn" type="button">刷新</button>
      <button id="log-folder" class="link-btn" type="button">打开文件夹</button>
      <button id="export-diagnostics" class="link-btn" type="button">导出诊断包</button>
      <button class="icon-btn" type="button" data-close-sheet aria-label="关闭">×</button>
    </div>
  </header>
  <div class="sheet-toolbar">
    <div class="segmented" id="log-tabs">
      <button class="active" type="button" data-kind="latest">游戏日志</button>
      <button type="button" data-kind="launcher">启动日志</button>
      <button type="button" data-kind="crash">崩溃报告</button>
    </div>
    <button id="log-bottom" class="link-btn" type="button">跳到末尾</button>
  </div>
  <p id="log-message" class="sheet-status" hidden></p>
  <div class="sheet-body log-body">
    <pre id="log-view" class="log-view"></pre>
  </div>
</section>

<section class="sheet" id="history-sheet" hidden aria-label="版本历史">
  <header class="sheet-head">
    <div>
      <h2>版本历史</h2>
      <p class="sheet-sub">每次更新会保留最近 3 份快照，回滚后仍可回滚回去</p>
    </div>
    <div class="sheet-head-actions">
      <button id="history-folder" class="link-btn" type="button">打开备份目录</button>
      <button class="icon-btn" type="button" data-close-sheet aria-label="关闭">×</button>
    </div>
  </header>
  <p id="history-message" class="sheet-status" hidden></p>
  <div class="sheet-body">
    <div id="history-list" class="history-list"></div>
  </div>
</section>

<section class="sheet narrow" id="account-sheet" hidden aria-label="登录账号">
  <header class="sheet-head">
    <div>
      <h2>登录账号</h2>
      <p class="sheet-sub">令牌用 Windows DPAPI 加密保存，皮肤站密码不会被存储</p>
    </div>
    <button class="icon-btn" type="button" data-close-sheet aria-label="关闭">×</button>
  </header>
  <div class="sheet-toolbar">
    <div class="segmented" id="account-tabs">
      <button class="active" type="button" data-tab="offline">离线</button>
      <button type="button" data-tab="microsoft">Microsoft</button>
      <button type="button" data-tab="yggdrasil">皮肤站</button>
    </div>
  </div>
  <p id="account-message" class="sheet-status" hidden></p>
  <div class="sheet-body">
    <div class="account-tab" data-panel="offline">
      <div class="field">
        <label for="offline-name">离线昵称</label>
        <input id="offline-name" class="text-input" maxlength="16" autocomplete="off" spellcheck="false" placeholder="3-16 位字母、数字或下划线"/>
        <small class="hint">UUID 由昵称按原版离线规则推导，服务器需为 online-mode=false</small>
      </div>
      <button id="use-offline" class="ghost-btn wide" type="button">使用离线登录</button>
    </div>

    <div class="account-tab" data-panel="microsoft" hidden>
      <p class="hint">使用内置应用登录，无需填写 Client ID。</p>
      <div id="device-box" class="device-box" hidden>
        <p class="device-tip">在浏览器打开登录页，输入下面的代码：</p>
        <div class="device-code" id="device-code">----</div>
        <button id="device-copy" class="ghost-btn wide" type="button">复制代码并打开登录页</button>
      </div>
      <button id="start-ms-login" class="launch-btn compact wide" type="button">使用 Microsoft 登录</button>
      <button id="cancel-ms-login" class="ghost-btn wide" type="button" hidden>取消登录</button>
    </div>

    <div class="account-tab" data-panel="yggdrasil" hidden>
      <div class="field">
        <label for="ygg-server">皮肤站 API 地址</label>
        <input id="ygg-server" class="text-input mono" spellcheck="false" placeholder="https://littleskin.cn/api/yggdrasil"/>
        <small class="hint">只填域名也可以，会自动补 /api/yggdrasil</small>
      </div>
      <div class="field">
        <label for="ygg-user">账号</label>
        <input id="ygg-user" class="text-input" autocomplete="username" spellcheck="false"/>
      </div>
      <div class="field">
        <label for="ygg-pass">密码</label>
        <input id="ygg-pass" class="text-input" type="password" autocomplete="current-password"/>
        <small class="hint">仅用于换取令牌；启动时自动注入 authlib-injector，皮肤与验证走该皮肤站</small>
      </div>
      <button id="ygg-login" class="launch-btn compact wide" type="button">登录皮肤站</button>
    </div>

    <div class="account-list-wrap">
      <p class="account-list-title">已保存账号</p>
      <div id="account-list" class="account-list"></div>
    </div>
  </div>
</section>
`;

let state: StateView | null = null;
let selectedMemory = 4096;
let syncedMemory = -1;
let selectedWorkers = 512;
let selectedSegments = 32;
let syncedWorkers = -1;
let syncedSegments = -1;
let busy = false;
let lastMessage = '';
let messageKind: 'error' | 'warn' | 'ok' = 'error';
let updateInfo: UpdateInfo | null = null;
let checkingUpdate = false;
let installingUpdate = false;
let updateMessage = '';
let rollingBack = false;
let confirmKill = false;
let killTimer: number | undefined;
let mods: ModView[] = [];
let modFilter: 'all' | 'enabled' | 'disabled' = 'all';
let modsLoading = false;
let logKind: 'latest' | 'launcher' | 'crash' = 'latest';
let history: BackupView[] = [];
let historyLoading = false;
let confirmRollback = '';
let rollbackTimer: number | undefined;
let msDevice: DeviceCodeView | null = null;
let msPending = false;
let lastAuthError = '';
let accountTab: 'offline' | 'microsoft' | 'yggdrasil' = 'offline';
let confirmRemove = '';
let removeTimer: number | undefined;
let launcherInfo: { available: boolean; currentVersion: string; latestVersion: string; notes?: string; size: number } | null = null;
let checkingLauncher = false;
let installingLauncher = false;
let launcherHint = '';

const playerInput = $<HTMLInputElement>('#player-name');
const launchButton = $<HTMLButtonElement>('#launch');
const messageEl = $<HTMLParagraphElement>('#message');

function formatBytes(bytes: number): string {
    if (!bytes || bytes < 0) return '0 B';
    if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GiB`;
    if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KiB`;
    return `${bytes} B`;
}

function normalizeError(err: unknown): string {
    if (typeof err === 'string') return err;
    if (err instanceof Error) return err.message;
    return String(err);
}

function escapeHtml(text: string): string {
    return text.replace(/[&<>"']/g, ch => ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
    }[ch] as string));
}

function showMessage(text: string, kind: 'error' | 'warn' | 'ok' = 'error') {
    lastMessage = text;
    messageKind = kind;
    messageEl.textContent = text;
    messageEl.hidden = !text;
    messageEl.classList.toggle('warn', kind === 'warn');
    messageEl.classList.toggle('ok', kind === 'ok');
}

function currentJava(): JavaView | undefined {
    if (!state) return undefined;
    const chosen = state.settings.javaPath;
    if (chosen) return state.java.find(j => j.path === chosen);
    return state.java.find(j => j.compatible);
}

function renderHero() {
    const inst = state?.instance;
    $('#mc-version').textContent = inst?.mcVersion || '1.20.1';
    $('#forge-version').textContent = inst?.forgeVersion || '47.4.23';
    $('#mod-count').textContent = inst?.modCount ? String(inst.modCount) : '--';
    $('#info-pack-version').textContent = inst?.packVersion || '未记录';
    $('#info-mc').textContent = inst?.mcVersion || '--';
    $('#info-forge').textContent = inst?.forgeVersion || '--';
    $('#info-mods').textContent = inst?.modCount ? String(inst.modCount) : '--';
    const gameDir = inst?.gameDir || inst?.path || '--';
    $('#instance-path').textContent = gameDir;
    $('#instance-path').title = gameDir;

    const badge = $('#pack-badge');
    if (!inst || inst.error) {
        badge.textContent = '未就绪';
        badge.classList.add('bad');
    } else {
        badge.textContent = '本地实例';
        badge.classList.remove('bad');
    }

    const err = $('#instance-error');
    err.textContent = inst?.error || '';
    err.hidden = !inst?.error;
}

function renderJava() {
    if (!state) return;
    const box = $('#java-status');
    const dot = box.querySelector('.status-dot')!;
    const strong = box.querySelector('strong')!;
    const small = box.querySelector('small')!;
    const downloadBtn = $<HTMLButtonElement>('#download-java');

    const java = currentJava();
    const compatible = state.java.find(j => j.compatible);
    const chosen = java || compatible;

    dot.className = 'status-dot';
    if (state.javaScanning) {
        dot.classList.add('pending');
        strong.textContent = '正在检测 Java…';
        small.textContent = '扫描内置运行时与系统安装';
    } else if (chosen) {
        dot.classList.add(chosen.compatible ? 'ok' : 'warn');
        strong.textContent = `Java ${chosen.major} · ${chosen.version}`;
        small.textContent = `${chosen.source} · ${chosen.path}`;
    } else {
        dot.classList.add('warn');
        strong.textContent = '未检测到 Java 21';
        small.textContent = state.javaError || '启动游戏时将自动下载内置 Java 21';
    }

    downloadBtn.hidden = !!compatible || busy;
    downloadBtn.disabled = busy;
}

function renderMemory() {
    document.querySelectorAll<HTMLButtonElement>('#memory-row .chip').forEach(btn => {
        const mb = Number(btn.dataset.mb);
        btn.classList.toggle('active', mb === selectedMemory);
    });
}

function renderLaunch() {
    const running = !!state?.running;
    const updating = installingUpdate || !!state?.updating;
    const rolling = rollingBack || !!state?.rollingBack;
    const label = launchButton.querySelector('span')!;
    if (running) {
        label.textContent = '游戏运行中';
    } else if (rolling) {
        label.textContent = '回滚中…';
    } else if (updating) {
        label.textContent = '更新中…';
    } else if (busy) {
        label.textContent = '准备中…';
    } else {
        label.textContent = '开始游戏';
    }
    launchButton.disabled = running || busy || updating || rolling;
    launchButton.classList.toggle('running', running);
}

function renderKill() {
    const button = $<HTMLButtonElement>('#kill-game');
    const running = !!state?.running;
    if (!running) {
        confirmKill = false;
        if (killTimer !== undefined) {
            window.clearTimeout(killTimer);
            killTimer = undefined;
        }
    }
    button.hidden = !running;
    button.disabled = !running;
    button.textContent = confirmKill ? '再次点击确认结束游戏' : '强制结束游戏';
    button.classList.toggle('armed', confirmKill);
}

function renderUpdate() {
    const status = $('#update-status');
    const checkBtn = $<HTMLButtonElement>('#check-update');
    const installBtn = $<HTMLButtonElement>('#install-update');
    const cancelBtn = $<HTMLButtonElement>('#cancel-update');
    const progress = $('#update-progress');
    const running = installingUpdate || !!state?.updating;
    const rolling = rollingBack || !!state?.rollingBack;

    checkBtn.disabled = running || checkingUpdate || rolling;
    checkBtn.textContent = checkingUpdate ? '检查中…' : '检查更新';
    installBtn.hidden = true;
    cancelBtn.hidden = true;

    if (rolling) {
        status.textContent = '正在回滚版本…';
        progress.hidden = true;
        return;
    }
    if (running) {
        status.textContent = updateMessage || '正在更新…';
        progress.hidden = false;
        cancelBtn.hidden = false;
        return;
    }
    if (checkingUpdate) {
        status.textContent = '正在检查更新…';
        progress.hidden = false;
        return;
    }
    progress.hidden = true;
    if (!updateInfo) {
        status.textContent = '尚未检查';
        return;
    }
    if (updateInfo.available) {
        const parts = [`${updateInfo.changedFiles} 个文件`];
        if (updateInfo.patches) parts.push(`${updateInfo.patches} 个补丁`);
        if (updateInfo.deletes) parts.push(`删除 ${updateInfo.deletes} 个`);
        status.textContent = `${parts.join(' · ')} · 下载 ${formatBytes(updateInfo.downloadBytes)}`;
        installBtn.textContent = `更新到 v${updateInfo.latestVersion}`;
        installBtn.hidden = false;
        installBtn.disabled = !!state?.running;
        return;
    }
    if (updateInfo.upToDate) {
        status.textContent = `已是最新（v${updateInfo.latestVersion}）`;
        return;
    }
    status.textContent = '尚未检查';
}

function renderLauncherUpdate() {
    if (!state) return;
    $('#launcher-version').textContent = `v${state.launcherVersion || '--'}`;
    const button = $<HTMLButtonElement>('#check-launcher');
    const busyLauncher = checkingLauncher || installingLauncher || !!state.launcherUpdating;
    button.disabled = busyLauncher;
    if (installingLauncher || state.launcherUpdating) {
        button.textContent = '更新中…';
    } else if (checkingLauncher) {
        button.textContent = '检查中…';
    } else if (launcherInfo?.available) {
        button.textContent = `更新到 v${launcherInfo.latestVersion}`;
    } else {
        button.textContent = '检查更新';
    }
    const hint = $('#launcher-update-hint');
    if (launcherHint) {
        hint.textContent = launcherHint;
    } else if (launcherInfo?.available) {
        hint.textContent = `新版本 v${launcherInfo.latestVersion} · ${formatBytes(launcherInfo.size)}，更新后自动重启`;
    } else {
        hint.textContent = '更新源已内置，更新后会自动重启';
    }
}

async function checkLauncherUpdate(silent = false) {
    if (!state || checkingLauncher || installingLauncher || state.launcherUpdating) return;
    checkingLauncher = true;
    if (!silent) {
        launcherHint = '正在检查启动器更新…';
        renderLauncherUpdate();
    }
    try {
        const info = await CheckLauncherUpdate();
        launcherInfo = info;
        if (!silent) {
            launcherHint = info.available
                ? `发现新版本 v${info.latestVersion}`
                : `已是最新版本 v${info.currentVersion || state.launcherVersion}`;
        } else if (info.available) {
            launcherHint = `发现新版本 v${info.latestVersion}，可在设置中更新`;
        }
    } catch (err) {
        launcherInfo = null;
        if (!silent) launcherHint = normalizeError(err);
    } finally {
        checkingLauncher = false;
        renderLauncherUpdate();
    }
}

async function installLauncherUpdate() {
    if (!state || installingLauncher || state.launcherUpdating) return;
    installingLauncher = true;
    launcherHint = '正在下载新版本…';
    renderLauncherUpdate();
    $('#footer-state').textContent = '更新启动器…';
    try {
        await InstallLauncherUpdate();
    } catch (err) {
        installingLauncher = false;
        launcherHint = normalizeError(err);
        renderLauncherUpdate();
    }
}

async function launcherUpdateClick() {
    if (!state || checkingLauncher || installingLauncher || state.launcherUpdating) return;
    if (launcherInfo?.available) {
        await installLauncherUpdate();
        return;
    }
    await checkLauncherUpdate();
}

function render() {
    if (!state) return;
    if (document.activeElement !== playerInput) {
        playerInput.value = state.settings.playerName;
    }
    if (state.settings.memoryMb !== syncedMemory) {
        syncedMemory = state.settings.memoryMb;
        selectedMemory = state.settings.memoryMb;
    }
    if (state.settings.downloadWorkers !== syncedWorkers) {
        syncedWorkers = state.settings.downloadWorkers;
        selectedWorkers = state.settings.downloadWorkers;
    }
    if (state.settings.downloadSegments !== syncedSegments) {
        syncedSegments = state.settings.downloadSegments;
        selectedSegments = state.settings.downloadSegments;
    }
    renderHero();
    renderJava();
    renderAccount();
    renderMemory();
    renderUpdate();
    renderLauncherUpdate();
    renderLaunch();
    renderKill();
    const rolling = rollingBack || !!state.rollingBack;
    $('#footer-state').textContent = state.running
        ? '游戏运行中'
        : (rolling ? '回滚中' : (installingUpdate || state.updating ? '更新中' : (state.javaScanning ? '检测中' : '就绪')));
    if (lastMessage) {
        showMessage(lastMessage, messageKind);
    }
}

async function refresh() {
    try {
        state = await GetState();
        render();
    } catch (err) {
        showMessage(normalizeError(err));
    }
}

let autoBootstrapDone = false;
async function autoBootstrap() {
    if (autoBootstrapDone || !state) return;
    autoBootstrapDone = true;
    if (state.running || state.updating || installingUpdate || checkingUpdate) return;
    // 空实例（缺少版本 json 且无已安装索引）时自动下载完整包。
    if (!state.instance.error || state.instance.packVersion) return;
    await checkUpdate();
    if (updateInfo?.available) await installUpdate();
}

function currentAccount(): AccountView | undefined {
    if (!state?.accountId) return undefined;
    return state.accounts.find(acc => acc.id === state!.accountId);
}

function accountLabel(acc: AccountView): string {
    return acc.kind === 'microsoft' ? 'Microsoft' : '皮肤站';
}

function renderAccount() {
    if (!state) return;
    const acc = currentAccount();
    const offline = !acc;
    const name = offline ? (state.settings.playerName || 'Player') : acc!.name;
    $('#account-avatar').textContent = (name.slice(0, 1) || 'P').toUpperCase();
    $('#account-name').textContent = name;
    $('#account-kind').textContent = offline
        ? '离线登录 · 仅本机'
        : (acc!.kind === 'microsoft' ? 'Microsoft 账号' : `皮肤站 · ${acc!.serverUrl || ''}`);
    $('#account-tag').textContent = offline ? '离线' : (acc!.kind === 'microsoft' ? '正版' : '皮肤站');
    playerInput.hidden = !offline;
    const hint = $('#account-hint');
    if (offline) {
        hint.textContent = '离线登录只在本机生效，服务器需为离线模式';
    } else if (acc!.kind === 'yggdrasil') {
        hint.textContent = '启动时自动注入 authlib-injector，皮肤与在线验证走该皮肤站';
    } else {
        hint.textContent = '使用 Microsoft 正版登录，令牌在启动前自动刷新';
    }
    $('#footer-account').textContent = offline
        ? `离线登录 · ${state.settings.playerName}`
        : `${accountLabel(acc!)} · ${acc!.name}`;
}

function collectSettings(instanceDir: string, javaPath: string, playerName?: string) {
    return {
        instanceDir,
        javaPath,
        playerName: playerName ?? playerInput.value.trim(),
        memoryMb: selectedMemory,
        downloadWorkers: selectedWorkers,
        downloadSegments: selectedSegments,
    };
}

async function launch() {
    if (!state || busy || state.running || installingUpdate || state.updating) return;
    busy = true;
    showMessage('');
    render();
    $('#java-progress').hidden = false;
    $('#java-bar').style.width = '0%';
    $('#java-progress-text').textContent = '准备中';
    $('#footer-state').textContent = '启动中';
    try {
        await SaveSettings(collectSettings(state.settings.instanceDir, state.settings.javaPath));
        await StartGame();
        showMessage('游戏已启动，日志写入 logs/ 目录', 'ok');
    } catch (err) {
        showMessage(normalizeError(err));
    } finally {
        busy = false;
        $('#java-progress').hidden = true;
        await refresh();
    }
}

async function downloadJava() {
    if (busy) return;
    busy = true;
    $('#download-java').setAttribute('disabled', '');
    showMessage('');
    render();
    $('#java-progress').hidden = false;
    $('#java-bar').style.width = '0%';
    $('#java-progress-text').textContent = '0%';
    try {
        await DownloadJava();
        showMessage('Java 21 安装完成', 'ok');
    } catch (err) {
        showMessage(normalizeError(err));
    } finally {
        busy = false;
        $('#java-progress').hidden = true;
        await refresh();
    }
}

async function rescan() {
    showMessage('');
    try {
        await RescanJava();
        await refresh();
    } catch (err) {
        showMessage(normalizeError(err));
    }
}

function phaseLabel(phase: string): string {
    switch (phase) {
        case 'scan': return '校验本地文件';
        case 'download': return '下载更新文件';
        case 'apply': return '应用更新';
        case 'commit': return '替换文件';
        case 'done': return '更新完成';
        default: return '更新中…';
    }
}

async function checkUpdate() {
    if (!state || checkingUpdate || installingUpdate || state.updating) return;
    checkingUpdate = true;
    updateInfo = null;
    updateMessage = '正在检查更新…';
    showMessage('');
    $('#update-progress').hidden = false;
    $('#update-bar').style.width = '0%';
    $('#update-progress-text').textContent = '…';
    render();
    try {
        const info = await CheckUpdate();
        updateInfo = info;
        if (info.available) {
            showMessage(`发现新版本 v${info.latestVersion}`, 'ok');
        } else if (info.upToDate) {
            showMessage(`已是最新版本 v${info.latestVersion}`, 'ok');
        }
    } catch (err) {
        updateInfo = null;
        showMessage(normalizeError(err));
    } finally {
        checkingUpdate = false;
        updateMessage = '';
        render();
    }
}

async function installUpdate() {
    if (!state || !updateInfo?.available || installingUpdate || state.updating) return;
    installingUpdate = true;
    updateMessage = '准备更新…';
    showMessage('');
    $('#update-progress').hidden = false;
    $('#update-bar').style.width = '0%';
    $('#update-progress-text').textContent = '0%';
    render();
    try {
        await InstallUpdate();
        updateMessage = '正在更新…';
        render();
    } catch (err) {
        installingUpdate = false;
        updateMessage = '';
        showMessage(normalizeError(err));
        render();
    }
}

async function cancelUpdate() {
    updateMessage = '正在取消更新…';
    render();
    try {
        await CancelUpdate();
    } catch (err) {
        showMessage(normalizeError(err));
    }
}

function openDrawer() {
    const current = state;
    if (!current) return;
    $<HTMLInputElement>('#instance-dir').value = current.settings.instanceDir;
    const select = $<HTMLSelectElement>('#java-select');
    select.innerHTML = '';
    const auto = document.createElement('option');
    auto.value = '';
    auto.textContent = '自动（优先 Java 21）';
    select.appendChild(auto);
    for (const j of current.java) {
        const option = document.createElement('option');
        option.value = j.path;
        option.textContent = `Java ${j.major} · ${j.source}${j.compatible ? '' : '（非 17）'}`;
        select.appendChild(option);
    }
    if (current.settings.javaPath && !current.java.some(j => j.path === current.settings.javaPath)) {
        const custom = document.createElement('option');
        custom.value = current.settings.javaPath;
        custom.textContent = `自定义 · ${current.settings.javaPath}`;
        select.appendChild(custom);
    }
    select.value = current.settings.javaPath;
    $<HTMLInputElement>('#download-workers').value = String(current.settings.downloadWorkers);
    $<HTMLInputElement>('#download-segments').value = String(current.settings.downloadSegments);
    $('#channel-value').textContent = current.settings.channel;
    $('#proxy-value').textContent = current.proxy || '直连';
    renderLauncherUpdate();
    $('#drawer').hidden = false;
    $('#drawer-mask').hidden = false;
}

function closeDrawer() {
    $('#drawer').hidden = true;
    $('#drawer-mask').hidden = true;
}

async function saveDrawer() {
    if (!state) return;
    const workersInput = $<HTMLInputElement>('#download-workers');
    const segmentsInput = $<HTMLInputElement>('#download-segments');
    const workersRaw = workersInput.value.trim();
    const segmentsRaw = segmentsInput.value.trim();
    if (workersRaw && Number.isFinite(Number(workersRaw))) {
        selectedWorkers = Math.min(1024, Math.max(1, Math.round(Number(workersRaw))));
    }
    if (segmentsRaw && Number.isFinite(Number(segmentsRaw))) {
        selectedSegments = Math.min(64, Math.max(1, Math.round(Number(segmentsRaw))));
    }
    workersInput.value = String(selectedWorkers);
    segmentsInput.value = String(selectedSegments);
    try {
        await SaveSettings(collectSettings(
            $<HTMLInputElement>('#instance-dir').value.trim(),
            $<HTMLSelectElement>('#java-select').value,
        ));
        closeDrawer();
        showMessage('设置已保存', 'ok');
        await refresh();
    } catch (err) {
        showMessage(normalizeError(err));
    }
}

async function pickInstance() {
    const current = $<HTMLInputElement>('#instance-dir').value.trim() || state?.settings.instanceDir || '';
    try {
        const dir = await PickDirectory(current);
        if (dir) $<HTMLInputElement>('#instance-dir').value = dir;
    } catch (err) {
        showMessage(normalizeError(err));
    }
}

// ---------- 账号面板 ----------

function setAccountMessage(text: string, kind: 'error' | 'ok' = 'error') {
    const el = $<HTMLParagraphElement>('#account-message');
    el.textContent = text;
    el.hidden = !text;
    el.classList.toggle('ok', kind === 'ok');
}

function selectAccountTab(tab: typeof accountTab) {
    accountTab = tab;
    document.querySelectorAll<HTMLButtonElement>('#account-tabs button').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.tab === tab);
    });
    document.querySelectorAll<HTMLElement>('.account-tab').forEach(panel => {
        panel.hidden = panel.dataset.panel !== tab;
    });
}

function initAccountSheet() {
    if (!state) return;
    setAccountMessage(lastAuthError);
    $<HTMLInputElement>('#offline-name').value = state.settings.playerName;
    $<HTMLInputElement>('#ygg-server').value = state.settings.yggdrasilServer || 'https://littleskin.cn/api/yggdrasil';
    msPending = state.microsoftLoggingIn;
    const active = currentAccount();
    selectAccountTab(active ? (active.kind === 'yggdrasil' ? 'yggdrasil' : 'microsoft') : 'offline');
    if (msPending && !msDevice) {
        setAccountMessage('正在等待 Microsoft 授权完成…');
    }
    renderDevice();
    renderAccountList();
}

function renderDevice() {
    const running = msPending || !!state?.microsoftLoggingIn;
    $('#device-box').hidden = !running || !msDevice;
    $<HTMLButtonElement>('#start-ms-login').hidden = running;
    $<HTMLButtonElement>('#cancel-ms-login').hidden = !running;
    if (msDevice) {
        $('#device-code').textContent = msDevice.userCode;
    }
}

function renderAccountList() {
    if (!state) return;
    const box = $('#account-list');
    if (!state.accounts.length) {
        box.innerHTML = '<p class="sheet-empty">还没有保存的账号</p>';
        return;
    }
    box.innerHTML = state.accounts.map(acc => {
        const active = acc.id === state!.accountId;
        const subtitle = acc.kind === 'microsoft'
            ? 'Microsoft 正版账号'
            : `皮肤站 · ${acc.serverUrl || ''}`;
        const armed = confirmRemove === acc.id;
        return `
        <div class="account-row${active ? ' active' : ''}">
          <div class="account-row-main">
            <strong>${escapeHtml(acc.name)}${active ? '<span class="account-current">使用中</span>' : ''}</strong>
            <small>${escapeHtml(subtitle)}</small>
          </div>
          <div class="account-row-actions">
            <button class="ghost-btn small" type="button" data-use="${escapeHtml(acc.id)}" ${active ? 'disabled' : ''}>使用</button>
            <button class="ghost-btn small danger${armed ? ' armed' : ''}" type="button" data-remove="${escapeHtml(acc.id)}">${armed ? '确认' : '删除'}</button>
          </div>
        </div>`;
    }).join('');
}

async function useOffline() {
    if (!state) return;
    const name = $<HTMLInputElement>('#offline-name').value.trim();
    if (!/^[A-Za-z0-9_]{3,16}$/.test(name)) {
        setAccountMessage('离线昵称需为 3-16 位字母、数字或下划线');
        return;
    }
    try {
        await SaveSettings(collectSettings(state.settings.instanceDir, state.settings.javaPath, name));
        await SwitchAccount('');
        playerInput.value = name;
        setAccountMessage('已切换到离线登录', 'ok');
        await refresh();
        renderAccountList();
    } catch (err) {
        setAccountMessage(normalizeError(err));
    }
}

async function startMsLogin() {
    if (!state) return;
    lastAuthError = '';
    setAccountMessage('正在申请登录码…');
    try {
        msDevice = await StartMicrosoftLogin();
        msPending = true;
        setAccountMessage('请打开登录页输入代码，完成后会自动返回');
        renderDevice();
        await ClipboardSetText(msDevice.userCode).catch(() => undefined);
    } catch (err) {
        msPending = false;
        setAccountMessage(normalizeError(err));
        renderDevice();
    }
}

async function cancelMsLogin() {
    msPending = false;
    msDevice = null;
    renderDevice();
    setAccountMessage('已取消 Microsoft 登录', 'ok');
    try {
        await CancelMicrosoftLogin();
    } catch (err) {
        setAccountMessage(normalizeError(err));
    }
}

function copyDeviceCode() {
    if (!msDevice) return;
    void ClipboardSetText(msDevice.userCode);
    BrowserOpenURL(msDevice.verificationUri);
}

async function loginYggdrasilAccount() {
    if (!state) return;
    const server = $<HTMLInputElement>('#ygg-server').value.trim();
    const username = $<HTMLInputElement>('#ygg-user').value.trim();
    const password = $<HTMLInputElement>('#ygg-pass').value;
    if (!username || !password) {
        setAccountMessage('请填写皮肤站账号与密码');
        return;
    }
    const button = $<HTMLButtonElement>('#ygg-login');
    button.disabled = true;
    setAccountMessage('正在登录皮肤站…');
    try {
        const acc = await LoginYggdrasil(username, password, server);
        $<HTMLInputElement>('#ygg-pass').value = '';
        setAccountMessage(`已登录为 ${acc.name}`, 'ok');
        await refresh();
        renderAccountList();
    } catch (err) {
        setAccountMessage(normalizeError(err));
    } finally {
        button.disabled = false;
    }
}

function handleAccountListClick(ev: MouseEvent) {
    const target = ev.target as HTMLElement;
    const useBtn = target.closest<HTMLButtonElement>('[data-use]');
    if (useBtn) {
        const id = useBtn.dataset.use || '';
        void (async () => {
            try {
                await SwitchAccount(id);
                await refresh();
                renderAccountList();
                setAccountMessage('已切换账号', 'ok');
            } catch (err) {
                setAccountMessage(normalizeError(err));
            }
        })();
        return;
    }
    const removeBtn = target.closest<HTMLButtonElement>('[data-remove]');
    if (!removeBtn) return;
    const id = removeBtn.dataset.remove || '';
    if (confirmRemove !== id) {
        confirmRemove = id;
        renderAccountList();
        if (removeTimer !== undefined) window.clearTimeout(removeTimer);
        removeTimer = window.setTimeout(() => {
            confirmRemove = '';
            removeTimer = undefined;
            renderAccountList();
        }, 5000);
        return;
    }
    confirmRemove = '';
    if (removeTimer !== undefined) {
        window.clearTimeout(removeTimer);
        removeTimer = undefined;
    }
    void (async () => {
        try {
            await RemoveAccount(id);
            await refresh();
            renderAccountList();
            setAccountMessage('账号已删除', 'ok');
        } catch (err) {
            setAccountMessage(normalizeError(err));
        }
    })();
}

// ---------- 浮层 ----------

function setSheetMessage(selector: string, text: string, kind: 'error' | 'ok' = 'error') {
    const el = $<HTMLParagraphElement>(selector);
    el.textContent = text;
    el.hidden = !text;
    el.classList.toggle('ok', kind === 'ok');
}

function openSheet(kind: 'mods' | 'logs' | 'history' | 'account') {
    $('#sheet-mask').hidden = false;
    for (const id of ['mods-sheet', 'logs-sheet', 'history-sheet', 'account-sheet']) {
        $('#' + id).hidden = id !== `${kind}-sheet`;
    }
    if (kind === 'mods') {
        void loadMods();
        $<HTMLInputElement>('#mods-search').focus();
    } else if (kind === 'logs') {
        void loadLog();
    } else if (kind === 'history') {
        void loadHistory();
    } else {
        initAccountSheet();
    }
}

function closeSheets() {
    $('#sheet-mask').hidden = true;
    for (const id of ['mods-sheet', 'logs-sheet', 'history-sheet', 'account-sheet']) {
        $('#' + id).hidden = true;
    }
}

async function openFolder(kind: string) {
    try {
        await OpenFolder(kind);
    } catch (err) {
        showMessage(normalizeError(err));
    }
}

// ---------- 模组 ----------

async function loadMods() {
    modsLoading = true;
    renderMods();
    try {
        mods = await ListMods();
        setSheetMessage('#mods-message', '');
    } catch (err) {
        mods = [];
        setSheetMessage('#mods-message', normalizeError(err));
    } finally {
        modsLoading = false;
        renderMods();
    }
}

function renderMods() {
    const total = mods.length;
    const enabled = mods.filter(m => m.enabled).length;
    $('#mods-summary').textContent = modsLoading && !total
        ? '正在读取 mods 目录…'
        : `共 ${total} 个模组 · 已启用 ${enabled} · 已禁用 ${total - enabled}`;

    const term = $<HTMLInputElement>('#mods-search').value.trim().toLowerCase();
    const visible = mods.filter(m => {
        if (modFilter === 'enabled' && !m.enabled) return false;
        if (modFilter === 'disabled' && m.enabled) return false;
        return !term || m.name.toLowerCase().includes(term);
    });

    document.querySelectorAll<HTMLButtonElement>('#mods-filter button').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.filter === modFilter);
    });

    const box = $('#mods-list');
    if (!visible.length) {
        box.innerHTML = `<p class="sheet-empty">${modsLoading ? '加载中…' : '没有匹配的模组'}</p>`;
        return;
    }
    box.innerHTML = visible.map(m => `
        <div class="mod-row${m.enabled ? '' : ' disabled'}">
          <div class="mod-info">
            <strong>${escapeHtml(m.name)}</strong>
            <small>${formatBytes(m.size)}${m.sha256 ? ` · ${escapeHtml(m.sha256.slice(0, 12))}` : ''}</small>
          </div>
          <label class="switch" title="${m.enabled ? '点击禁用' : '点击启用'}">
            <input type="checkbox" data-name="${escapeHtml(m.name)}" ${m.enabled ? 'checked' : ''}/>
            <span></span>
          </label>
        </div>`).join('');
}

async function toggleMod(input: HTMLInputElement) {
    const name = input.dataset.name;
    if (!name) return;
    const desired = input.checked;
    input.disabled = true;
    try {
        await SetModEnabled(name, desired);
        await loadMods();
        void refresh();
    } catch (err) {
        input.checked = !desired;
        input.disabled = false;
        setSheetMessage('#mods-message', normalizeError(err));
    }
}

// ---------- 日志 ----------

async function loadLog(kind: typeof logKind = logKind) {
    logKind = kind;
    document.querySelectorAll<HTMLButtonElement>('#log-tabs button').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.kind === kind);
    });
    $('#log-meta').textContent = '读取中…';
    $('#log-view').textContent = '';
    try {
        const view = await TailLog(kind);
        renderLog(view);
    } catch (err) {
        $('#log-meta').textContent = '读取失败';
        setSheetMessage('#log-message', normalizeError(err));
    }
}

function renderLog(view: LogView) {
    const hasLines = !!view.lines && view.lines.length > 0;
    $('#log-meta').textContent = view.path
        ? `${view.path} · ${formatBytes(view.size)} · ${view.modifiedAt}${view.truncated ? ' · 仅显示尾部' : ''}`
        : (view.message || '--');
    $('#log-view').textContent = hasLines ? view.lines.join('\n') : (view.message || '暂无日志内容');
    setSheetMessage('#log-message', view.message && !hasLines ? view.message : '');
    if (hasLines) {
        requestAnimationFrame(() => {
            const body = document.querySelector('.log-body') as HTMLElement;
            body.scrollTop = body.scrollHeight;
        });
    }
}

async function exportDiagnostics() {
    setSheetMessage('#log-message', '正在收集日志并打包…');
    try {
        const result: DiagnosticsView = await ExportDiagnostics();
        setSheetMessage('#log-message', `诊断包已导出（${formatBytes(result.size)}）：${result.path}`, 'ok');
        await OpenFolder('diagnostics');
    } catch (err) {
        setSheetMessage('#log-message', normalizeError(err));
    }
}

// ---------- 版本历史 ----------

async function loadHistory() {
    historyLoading = true;
    renderHistory();
    try {
        history = await ListBackups();
        setSheetMessage('#history-message', '');
    } catch (err) {
        history = [];
        setSheetMessage('#history-message', normalizeError(err));
    } finally {
        historyLoading = false;
        renderHistory();
    }
}

function renderHistory() {
    const box = $('#history-list');
    if (historyLoading && !history.length) {
        box.innerHTML = '<p class="sheet-empty">加载中…</p>';
        return;
    }
    if (!history.length) {
        box.innerHTML = '<p class="sheet-empty">暂无版本历史，每次更新成功后会自动生成快照</p>';
        return;
    }
    const rolling = rollingBack || !!state?.rollingBack;
    box.innerHTML = history.map(item => {
        const title = item.from || item.to ? `v${item.from || '?'} → v${item.to || '?'}` : '较早的备份';
        const time = item.createdAt ? item.createdAt.replace('T', ' ').slice(0, 19) : item.stamp;
        const parts = [time, `${item.files} 个文件`];
        if (item.deletes) parts.push(`删除 ${item.deletes} 个`);
        parts.push(formatBytes(item.bytes));
        const armed = confirmRollback === item.stamp;
        const disabled = !item.canRollback || rolling;
        const label = !item.canRollback ? '已回滚' : (armed ? '确认回滚' : '回滚到此版本');
        return `
        <div class="history-row">
          <div class="history-main">
            <strong>${escapeHtml(title)}</strong>
            <small>${escapeHtml(parts.join(' · '))}</small>
          </div>
          <button class="ghost-btn history-btn${armed ? ' armed' : ''}" type="button"
                  data-rollback="${escapeHtml(item.stamp)}" ${disabled ? 'disabled' : ''}>${label}</button>
        </div>`;
    }).join('');
}

async function doRollback(stamp: string) {
    rollingBack = true;
    render();
    setSheetMessage('#history-message', '正在回滚，请勿关闭启动器…');
    try {
        const result: RollbackResult = await Rollback(stamp);
        setSheetMessage('#history-message', `已回滚到 v${result.to}：恢复 ${result.restored} 个文件，移除 ${result.removed} 个`, 'ok');
        showMessage(`已回滚到 v${result.to}`, 'ok');
    } catch (err) {
        setSheetMessage('#history-message', normalizeError(err));
        showMessage(normalizeError(err));
    } finally {
        rollingBack = false;
        await refresh();
        await loadHistory();
    }
}

// ---------- 游戏进程 ----------

async function killGame() {
    if (!confirmKill) {
        confirmKill = true;
        renderKill();
        killTimer = window.setTimeout(() => {
            confirmKill = false;
            killTimer = undefined;
            renderKill();
        }, 5000);
        return;
    }
    confirmKill = false;
    if (killTimer !== undefined) {
        window.clearTimeout(killTimer);
        killTimer = undefined;
    }
    renderKill();
    try {
        await KillGame();
        showMessage('已结束游戏进程', 'warn');
    } catch (err) {
        showMessage(normalizeError(err));
    }
}

// ---------- 事件 ----------

EventsOn('launcher:changed', () => { void refresh(); });
EventsOn('launcher:warning', (msg: string) => showMessage(msg, 'warn'));
EventsOn('launcher:java-progress', (payload: { done: number; total: number }) => {
    if (!payload || !payload.total) return;
    const percent = Math.min(100, Math.round((payload.done / payload.total) * 100));
    $('#java-progress').hidden = false;
    $('#java-bar').style.width = `${percent}%`;
    $('#java-progress-text').textContent = `${percent}%`;
    $('#footer-state').textContent = `下载 Java ${percent}%`;
});
EventsOn('update:progress', (payload: UpdateProgress) => {
    if (!payload) return;
    if (!checkingUpdate && !installingUpdate && !state?.updating) return;
    const bar = $('#update-bar');
    const text = $('#update-progress-text');
    $('#update-progress').hidden = false;
    updateMessage = payload.message || phaseLabel(payload.phase);
    if (payload.phase === 'download' && payload.total > 0) {
        const percent = Math.min(100, Math.round((payload.done / payload.total) * 100));
        bar.style.width = `${percent}%`;
        text.textContent = `${percent}%`;
        updateMessage = `${payload.message || '下载更新文件'} ${formatBytes(payload.done)} / ${formatBytes(payload.total)}`;
    } else if (payload.filesTotal > 0) {
        const percent = Math.min(100, Math.round((payload.filesDone / payload.filesTotal) * 100));
        bar.style.width = `${percent}%`;
        text.textContent = `${percent}%`;
        updateMessage = `${payload.message || phaseLabel(payload.phase)}（${payload.filesDone}/${payload.filesTotal}）`;
    } else {
        bar.style.width = '0%';
        text.textContent = payload.filesDone ? `${payload.filesDone}` : '…';
    }
    $('#update-status').textContent = updateMessage;
    $('#footer-state').textContent = updateMessage;
});
EventsOn('update:done', (res: UpdateResult) => {
    installingUpdate = false;
    updateInfo = null;
    updateMessage = '';
    if (res?.updated) {
        showMessage(`已更新到 v${res.version}`, 'ok');
    } else {
        showMessage(`已是最新版本 v${res.version}`, 'ok');
    }
    if (!$('#history-sheet').hidden) void loadHistory();
    void refresh();
});
EventsOn('update:error', (payload: { message?: string }) => {
    installingUpdate = false;
    updateInfo = null;
    updateMessage = '';
    showMessage(payload?.message || '更新失败');
    void refresh();
});
EventsOn('launcher:update-progress', (payload: { done: number; total: number }) => {
    if (!payload || !payload.total) return;
    const percent = Math.min(100, Math.round((payload.done / payload.total) * 100));
    launcherHint = `正在下载新版本 ${formatBytes(payload.done)} / ${formatBytes(payload.total)}（${percent}%）`;
    $('#footer-state').textContent = `更新启动器 ${percent}%`;
    renderLauncherUpdate();
});
EventsOn('launcher:update-error', (msg: string) => {
    installingLauncher = false;
    launcherHint = msg || '启动器更新失败';
    showMessage(launcherHint);
    renderLauncherUpdate();
});
EventsOn('launcher:update-done', (payload: { version?: string }) => {
    launcherHint = `已更新到 v${payload?.version || ''}，正在重启启动器…`;
    $('#footer-state').textContent = '重启启动器…';
    renderLauncherUpdate();
});
EventsOn('auth:changed', (payload: { name?: string }) => {
    msPending = false;
    msDevice = null;
    lastAuthError = '';
    setAccountMessage(payload?.name ? `已登录为 ${payload.name}` : '登录成功', 'ok');
    void refresh().then(() => {
        renderAccountList();
        renderDevice();
    });
});
EventsOn('auth:stage', (payload: { message?: string }) => {
    if (payload?.message) setAccountMessage(payload.message);
});
EventsOn('auth:error', (payload: { message?: string }) => {
    msPending = false;
    lastAuthError = payload?.message || '登录失败';
    setAccountMessage(lastAuthError);
    showMessage(lastAuthError);
    renderDevice();
});
EventsOn('authlib:progress', (payload: { done: number; total: number }) => {
    if (!payload || !payload.total) return;
    const percent = Math.min(100, Math.round((payload.done / payload.total) * 100));
    $('#footer-state').textContent = `下载注入器 ${percent}%`;
});
EventsOn('game:exited', (payload: { code: number; durationMs: number; logPath: string }) => {
    if (!payload) return;
    if (payload.code === 0) {
        showMessage('游戏已正常退出', 'ok');
    } else {
        showMessage(`游戏异常退出（代码 ${payload.code}），日志：${payload.logPath}`);
    }
    if (!$('#logs-sheet').hidden) void loadLog();
    void refresh();
});

// ---------- 绑定 ----------

$('#launch').addEventListener('click', launch);
$('#kill-game').addEventListener('click', killGame);
$('#download-java').addEventListener('click', downloadJava);
$('#rescan').addEventListener('click', rescan);
$('#check-update').addEventListener('click', checkUpdate);
$('#install-update').addEventListener('click', installUpdate);
$('#cancel-update').addEventListener('click', cancelUpdate);
$('#open-settings').addEventListener('click', openDrawer);
$('#close-settings').addEventListener('click', closeDrawer);
$('#cancel-settings').addEventListener('click', closeDrawer);
$('#save-settings').addEventListener('click', saveDrawer);
$('#check-launcher').addEventListener('click', () => void launcherUpdateClick());
$('#pick-instance').addEventListener('click', pickInstance);
$('#drawer-mask').addEventListener('click', closeDrawer);

$('#open-mods').addEventListener('click', () => openSheet('mods'));
$('#open-logs').addEventListener('click', () => openSheet('logs'));
$('#open-history').addEventListener('click', () => openSheet('history'));
$('#open-account').addEventListener('click', () => openSheet('account'));
$('#account-tabs').addEventListener('click', ev => {
    const target = (ev.target as HTMLElement).closest<HTMLButtonElement>('button');
    if (!target || !target.dataset.tab) return;
    selectAccountTab(target.dataset.tab as typeof accountTab);
});
$('#use-offline').addEventListener('click', () => void useOffline());
$('#start-ms-login').addEventListener('click', () => void startMsLogin());
$('#cancel-ms-login').addEventListener('click', () => void cancelMsLogin());
$('#device-copy').addEventListener('click', copyDeviceCode);
$('#ygg-login').addEventListener('click', () => void loginYggdrasilAccount());
$('#account-list').addEventListener('click', handleAccountListClick);
$('#ygg-pass').addEventListener('keydown', ev => {
    if (ev.key === 'Enter') void loginYggdrasilAccount();
});
$('#sheet-mask').addEventListener('click', closeSheets);
document.querySelectorAll<HTMLButtonElement>('[data-close-sheet]').forEach(btn => {
    btn.addEventListener('click', closeSheets);
});

$('#mods-search').addEventListener('input', renderMods);
$('#mods-filter').addEventListener('click', ev => {
    const target = (ev.target as HTMLElement).closest<HTMLButtonElement>('button');
    if (!target || !target.dataset.filter) return;
    modFilter = target.dataset.filter as typeof modFilter;
    renderMods();
});
$('#mods-list').addEventListener('change', ev => {
    const input = (ev.target as HTMLElement).closest<HTMLInputElement>('input[type="checkbox"]');
    if (input) void toggleMod(input);
});
$('#mods-folder').addEventListener('click', () => void openFolder('mods'));

$('#log-tabs').addEventListener('click', ev => {
    const target = (ev.target as HTMLElement).closest<HTMLButtonElement>('button');
    if (!target || !target.dataset.kind) return;
    void loadLog(target.dataset.kind as typeof logKind);
});
$('#log-refresh').addEventListener('click', () => void loadLog());
$('#log-folder').addEventListener('click', () => void openFolder(logKind === 'crash' ? 'crash' : 'logs'));
$('#log-bottom').addEventListener('click', () => {
    const body = document.querySelector('.log-body') as HTMLElement;
    body.scrollTop = body.scrollHeight;
});
$('#export-diagnostics').addEventListener('click', exportDiagnostics);

$('#history-folder').addEventListener('click', () => void openFolder('backups'));
$('#history-list').addEventListener('click', ev => {
    const target = (ev.target as HTMLElement).closest<HTMLButtonElement>('[data-rollback]');
    if (!target || target.disabled) return;
    const stamp = target.dataset.rollback || '';
    if (!stamp) return;
    if (confirmRollback !== stamp) {
        confirmRollback = stamp;
        renderHistory();
        if (rollbackTimer !== undefined) window.clearTimeout(rollbackTimer);
        rollbackTimer = window.setTimeout(() => {
            confirmRollback = '';
            rollbackTimer = undefined;
            renderHistory();
        }, 5000);
        return;
    }
    confirmRollback = '';
    if (rollbackTimer !== undefined) {
        window.clearTimeout(rollbackTimer);
        rollbackTimer = undefined;
    }
    void doRollback(stamp);
});

document.addEventListener('keydown', ev => {
    if (ev.key !== 'Escape') return;
    closeSheets();
    closeDrawer();
});
$('#memory-row').addEventListener('click', ev => {
    const target = (ev.target as HTMLElement).closest<HTMLButtonElement>('.chip');
    if (!target) return;
    selectedMemory = Number(target.dataset.mb);
    renderMemory();
});

void refresh().then(autoBootstrap).then(() => checkLauncherUpdate(true));
