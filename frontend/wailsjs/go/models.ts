export namespace main {
	
	export class AccountView {
	    id: string;
	    kind: string;
	    name: string;
	    uuid: string;
	    serverUrl?: string;
	    expiresAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new AccountView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.uuid = source["uuid"];
	        this.serverUrl = source["serverUrl"];
	        this.expiresAt = source["expiresAt"];
	    }
	}
	export class DeviceCodeView {
	    userCode: string;
	    verificationUri: string;
	    message: string;
	    expiresIn: number;
	
	    static createFrom(source: any = {}) {
	        return new DeviceCodeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.userCode = source["userCode"];
	        this.verificationUri = source["verificationUri"];
	        this.message = source["message"];
	        this.expiresIn = source["expiresIn"];
	    }
	}
	export class DiagnosticsView {
	    path: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new DiagnosticsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.size = source["size"];
	    }
	}
	export class InstanceView {
	    path: string;
	    gameDir: string;
	    versionId: string;
	    mcVersion: string;
	    forgeVersion: string;
	    modCount: number;
	    packVersion: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new InstanceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.gameDir = source["gameDir"];
	        this.versionId = source["versionId"];
	        this.mcVersion = source["mcVersion"];
	        this.forgeVersion = source["forgeVersion"];
	        this.modCount = source["modCount"];
	        this.packVersion = source["packVersion"];
	        this.error = source["error"];
	    }
	}
	export class JavaView {
	    path: string;
	    version: string;
	    major: number;
	    source: string;
	    compatible: boolean;
	
	    static createFrom(source: any = {}) {
	        return new JavaView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.version = source["version"];
	        this.major = source["major"];
	        this.source = source["source"];
	        this.compatible = source["compatible"];
	    }
	}
	export class LogView {
	    kind: string;
	    path: string;
	    size: number;
	    modifiedAt: string;
	    lines: string[];
	    truncated: boolean;
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new LogView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.path = source["path"];
	        this.size = source["size"];
	        this.modifiedAt = source["modifiedAt"];
	        this.lines = source["lines"];
	        this.truncated = source["truncated"];
	        this.message = source["message"];
	    }
	}
	export class ModView {
	    name: string;
	    enabled: boolean;
	    size: number;
	    sha256?: string;
	
	    static createFrom(source: any = {}) {
	        return new ModView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.size = source["size"];
	        this.sha256 = source["sha256"];
	    }
	}
	export class SettingsInput {
	    instanceDir: string;
	    javaPath: string;
	    playerName: string;
	    memoryMb: number;
	    downloadWorkers: number;
	    downloadSegments: number;
	
	    static createFrom(source: any = {}) {
	        return new SettingsInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instanceDir = source["instanceDir"];
	        this.javaPath = source["javaPath"];
	        this.playerName = source["playerName"];
	        this.memoryMb = source["memoryMb"];
	        this.downloadWorkers = source["downloadWorkers"];
	        this.downloadSegments = source["downloadSegments"];
	    }
	}
	export class SettingsView {
	    instanceDir: string;
	    javaPath: string;
	    playerName: string;
	    memoryMb: number;
	    channel: string;
	    yggdrasilServer: string;
	    downloadWorkers: number;
	    downloadSegments: number;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instanceDir = source["instanceDir"];
	        this.javaPath = source["javaPath"];
	        this.playerName = source["playerName"];
	        this.memoryMb = source["memoryMb"];
	        this.channel = source["channel"];
	        this.yggdrasilServer = source["yggdrasilServer"];
	        this.downloadWorkers = source["downloadWorkers"];
	        this.downloadSegments = source["downloadSegments"];
	    }
	}
	export class StateView {
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
	
	    static createFrom(source: any = {}) {
	        return new StateView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.settings = this.convertValues(source["settings"], SettingsView);
	        this.java = this.convertValues(source["java"], JavaView);
	        this.javaScanning = source["javaScanning"];
	        this.javaError = source["javaError"];
	        this.instance = this.convertValues(source["instance"], InstanceView);
	        this.accounts = this.convertValues(source["accounts"], AccountView);
	        this.accountId = source["accountId"];
	        this.microsoftLoggingIn = source["microsoftLoggingIn"];
	        this.proxy = source["proxy"];
	        this.launcherVersion = source["launcherVersion"];
	        this.launcherUpdating = source["launcherUpdating"];
	        this.running = source["running"];
	        this.updating = source["updating"];
	        this.rollingBack = source["rollingBack"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace selfupdate {
	
	export class Info {
	    available: boolean;
	    currentVersion: string;
	    latestVersion: string;
	    notes?: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.notes = source["notes"];
	        this.size = source["size"];
	    }
	}

}

export namespace updater {
	
	export class BackupView {
	    stamp: string;
	    from: string;
	    to: string;
	    createdAt: string;
	    files: number;
	    deletes: number;
	    bytes: number;
	    canRollback: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BackupView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stamp = source["stamp"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.createdAt = source["createdAt"];
	        this.files = source["files"];
	        this.deletes = source["deletes"];
	        this.bytes = source["bytes"];
	        this.canRollback = source["canRollback"];
	    }
	}
	export class Info {
	    available: boolean;
	    upToDate: boolean;
	    currentVersion: string;
	    latestVersion: string;
	    publishedAt?: string;
	    changedFiles: number;
	    patches: number;
	    deletes: number;
	    downloadBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.upToDate = source["upToDate"];
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.publishedAt = source["publishedAt"];
	        this.changedFiles = source["changedFiles"];
	        this.patches = source["patches"];
	        this.deletes = source["deletes"];
	        this.downloadBytes = source["downloadBytes"];
	    }
	}
	export class RollbackResult {
	    from: string;
	    to: string;
	    restored: number;
	    removed: number;
	
	    static createFrom(source: any = {}) {
	        return new RollbackResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.restored = source["restored"];
	        this.removed = source["removed"];
	    }
	}

}

