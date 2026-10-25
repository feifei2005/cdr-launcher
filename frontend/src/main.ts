import './style.css';
import './app.css';

import {
    CancelUpdate,
    CheckUpdate,
    DownloadJava,
    ExportDiagnostics,
    GetState,
    InstallUpdate,
    KillGame,
    ListBackups,
    ListMods,
    OpenFolder,
    PickDirectory,
    RescanJava,
    Rollback,
    SaveSettings,
    SetModEnabled,
    StartGame,
    TailLog
} from '../wailsjs/go/main/App';
import {EventsOn} from '../wailsjs/runtime/runtime';

interface SettingsView {
    instanceDir: string;
    javaPath: string;
    playerName: string;
    memoryMb: number;
    channel: string;
    updateUrl: string;
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
        <label for="player-name">游戏昵称</label>
        <input id="player-name" class="text-input" maxlength="16" autocomplete="off" spellcheck="false" placeholder="3-16 位字母、数字或下划线"/>
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
    <span>离线昵称登录 · 首次启动会自动补齐缺失的库文件</span>
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
      <label for="update-url">更新地址</label>
      <input id="update-url" class="text-input mono" spellcheck="false" placeholder="https://update.example.com/stable/"/>
      <small class="hint">整合包更新源，留空则不检查更新</small>
    </div>
    <div class="field">
      <label>版本通道</label>
      <div class="readonly" id="channel-value">stable</div>
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
`;

let state: StateView | null = null;
let selectedMemory = 4096;
let syncedMemory = -1;
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
    const configured = !!state?.settings.updateUrl;

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
    if (!configured) {
        status.textContent = '未配置更新地址';
        return;
    }
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

function render() {
    if (!state) return;
    if (document.activeElement !== playerInput) {
        playerInput.value = state.settings.playerName;
    }
    if (state.settings.memoryMb !== syncedMemory) {
        syncedMemory = state.settings.memoryMb;
        selectedMemory = state.settings.memoryMb;
    }
    renderHero();
    renderJava();
    renderMemory();
    renderUpdate();
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

function collectSettings(instanceDir: string, javaPath: string, updateUrl: string) {
    return {
        instanceDir,
        javaPath,
        playerName: playerInput.value.trim(),
        memoryMb: selectedMemory,
        updateUrl,
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
        await SaveSettings(collectSettings(state.settings.instanceDir, state.settings.javaPath, state.settings.updateUrl));
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
    if (!state.settings.updateUrl) {
        showMessage('未配置更新地址，请在启动器设置中填写', 'warn');
        return;
    }
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
    $<HTMLInputElement>('#update-url').value = current.settings.updateUrl;
    $('#channel-value').textContent = current.settings.channel;
    $('#drawer').hidden = false;
    $('#drawer-mask').hidden = false;
}

function closeDrawer() {
    $('#drawer').hidden = true;
    $('#drawer-mask').hidden = true;
}

async function saveDrawer() {
    if (!state) return;
    try {
        await SaveSettings(collectSettings(
            $<HTMLInputElement>('#instance-dir').value.trim(),
            $<HTMLSelectElement>('#java-select').value,
            $<HTMLInputElement>('#update-url').value.trim(),
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

// ---------- 浮层 ----------

function setSheetMessage(selector: string, text: string, kind: 'error' | 'ok' = 'error') {
    const el = $<HTMLParagraphElement>(selector);
    el.textContent = text;
    el.hidden = !text;
    el.classList.toggle('ok', kind === 'ok');
}

function openSheet(kind: 'mods' | 'logs' | 'history') {
    $('#sheet-mask').hidden = false;
    for (const id of ['mods-sheet', 'logs-sheet', 'history-sheet']) {
        $('#' + id).hidden = id !== `${kind}-sheet`;
    }
    if (kind === 'mods') {
        void loadMods();
        $<HTMLInputElement>('#mods-search').focus();
    } else if (kind === 'logs') {
        void loadLog();
    } else {
        void loadHistory();
    }
}

function closeSheets() {
    $('#sheet-mask').hidden = true;
    for (const id of ['mods-sheet', 'logs-sheet', 'history-sheet']) {
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
$('#pick-instance').addEventListener('click', pickInstance);
$('#drawer-mask').addEventListener('click', closeDrawer);

$('#open-mods').addEventListener('click', () => openSheet('mods'));
$('#open-logs').addEventListener('click', () => openSheet('logs'));
$('#open-history').addEventListener('click', () => openSheet('history'));
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

void refresh();
