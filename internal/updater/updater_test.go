package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cdr-launcher/internal/manifest"
)

func TestUpdateDownloadPatchDeleteAndRollback(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	serverRoot := t.TempDir()

	oldBig := makeContent(1<<20, "old")
	newBig := makeContent(1<<20, "new")
	write(t, oldRoot, "mods/changed.jar", oldBig)
	write(t, newRoot, "mods/changed.jar", newBig)
	write(t, oldRoot, "mods/deleted.jar", []byte("deleted content"))
	write(t, newRoot, "mods/new.jar", []byte("brand new"))
	write(t, oldRoot, "mods/same.jar", []byte("same"))
	write(t, newRoot, "mods/same.jar", []byte("same"))
	write(t, oldRoot, "config/app.cfg", []byte("original config"))
	write(t, newRoot, "config/app.cfg", []byte("pack config v2"))

	newFiles, err := manifest.Build(manifest.BuildOptions{Root: newRoot, BlobsDir: filepath.Join(serverRoot, "blobs")})
	if err != nil {
		t.Fatal(err)
	}
	patches, deletes, err := manifest.Diff(manifest.DiffOptions{
		FromRoot: oldRoot,
		ToRoot:   newRoot,
		From:     "1.0.0",
		To:       "1.1.0",
		BlobsDir: filepath.Join(serverRoot, "blobs"),
		MinSize:  1024,
		MaxRatio: 0.95,
	})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.FileServer(http.Dir(serverRoot)))
	defer server.Close()
	m := &manifest.Manifest{
		Format:      manifest.FormatVersion,
		PackID:      manifest.DefaultPackID,
		Channel:     "stable",
		Version:     "1.1.0",
		PublishedAt: "2026-10-24T00:00:00Z",
		BaseURL:     server.URL + "/",
		Files:       newFiles,
		Patches:     patches,
		Deletes:     deletes,
	}
	if err := m.Write(filepath.Join(serverRoot, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	write(t, oldRoot, "config/app.cfg", []byte("user modified config"))
	idx, err := Refresh(oldRoot, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	idx.Version = "1.0.0"
	if err := SaveIndex(oldRoot, idx); err != nil {
		t.Fatal(err)
	}

	var phases []string
	opts := Options{
		Root:           oldRoot,
		ManifestURL:    server.URL,
		CurrentVersion: "1.0.0",
		PackID:         manifest.DefaultPackID,
		Channel:        "stable",
		Progress: func(p Progress) {
			if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
				phases = append(phases, p.Phase)
			}
		},
	}
	info, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.UpToDate {
		t.Fatalf("expected update available, got %+v", info)
	}
	if info.ChangedFiles < 2 || info.Patches < 1 || info.Deletes < 1 {
		t.Fatalf("unexpected check info: %+v", info)
	}

	m2, idx2, actions, err := prepare(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = m2
	var hasPatch bool
	for _, action := range actions {
		if action.Path == "mods/changed.jar" {
			if action.Kind != KindPatch {
				t.Fatalf("mods/changed.jar should use patch, got %s", action.Kind)
			}
			hasPatch = true
		}
		if action.Path == "mods/deleted.jar" && action.Kind != KindDelete {
			t.Fatalf("deleted file action = %s", action.Kind)
		}
		if action.Path == "config/app.cfg" && action.Kind != KindSkip {
			t.Fatalf("user editable file action = %s", action.Kind)
		}
	}
	if !hasPatch {
		t.Fatal("missing patch action")
	}
	if idx2.Version != "1.0.0" {
		t.Fatalf("index version changed unexpectedly: %s", idx2.Version)
	}

	res, err := Update(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.Version != "1.1.0" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := read(t, oldRoot, "mods/changed.jar"); !bytes.Equal(got, newBig) {
		t.Fatal("changed file was not patched")
	}
	if got := read(t, oldRoot, "mods/new.jar"); string(got) != "brand new" {
		t.Fatalf("new file content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(oldRoot, "mods/deleted.jar")); !os.IsNotExist(err) {
		t.Fatal("deleted file still exists")
	}
	if got := read(t, oldRoot, "config/app.cfg"); string(got) != "user modified config" {
		t.Fatalf("user editable file overwritten: %q", got)
	}
	if got := read(t, oldRoot, "mods/same.jar"); string(got) != "same" {
		t.Fatalf("unchanged file modified: %q", got)
	}

	final, err := LoadIndex(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	if final.Version != "1.1.0" {
		t.Fatalf("installed version = %s", final.Version)
	}
	if _, ok := final.Files["mods/new.jar"]; !ok {
		t.Fatal("new file missing from index")
	}
	if _, ok := final.Files["mods/deleted.jar"]; ok {
		t.Fatal("deleted file still in index")
	}
	if _, err := os.Stat(filepath.Join(StateDir(oldRoot), "journal.jsonl")); !os.IsNotExist(err) {
		t.Fatal("journal should be removed")
	}
	if _, err := os.Stat(filepath.Join(StateDir(oldRoot), stagingDirName)); !os.IsNotExist(err) {
		t.Fatal("staging should be removed")
	}

	backups, err := os.ReadDir(filepath.Join(StateDir(oldRoot), backupsDirName))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	backupDeleted := filepath.Join(StateDir(oldRoot), backupsDirName, backups[0].Name(), "mods", "deleted.jar")
	if got := readAbs(t, backupDeleted); string(got) != "deleted content" {
		t.Fatalf("backup content = %q", got)
	}

	if !containsPhase(phases, "scan") || !containsPhase(phases, "download") || !containsPhase(phases, "commit") {
		t.Fatalf("missing progress phases: %v", phases)
	}

	again, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if again.Available || !again.UpToDate {
		t.Fatalf("expected up to date, got %+v", again)
	}
	if err := Recover(oldRoot); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverJournal(t *testing.T) {
	root := t.TempDir()
	write(t, root, "mods/a.jar", []byte("new content"))
	write(t, root, "mods/b.jar", []byte("old b"))
	write(t, root, "mods/c.jar", []byte("new c"))
	stateDir := StateDir(root)
	backupRoot := filepath.Join(stateDir, "backups", "20260101-000000")
	if err := os.MkdirAll(filepath.Join(backupRoot, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAbs(t, filepath.Join(backupRoot, "mods", "a.jar"), []byte("old content"))
	writeAbs(t, filepath.Join(backupRoot, "mods", "b.jar"), []byte("old b"))
	if err := os.Remove(filepath.Join(root, "mods", "b.jar")); err != nil {
		t.Fatal(err)
	}

	header := journalHeader{Version: 1, BackupDir: "backups/20260101-000000", From: "1.0.0", To: "1.1.0"}
	entries := []journalEntry{
		{Op: "replace", Path: "mods/a.jar", Backup: "mods/a.jar", HadBackup: true},
		{Op: "delete", Path: "mods/b.jar", Backup: "mods/b.jar", HadBackup: true},
		{Op: "replace", Path: "mods/c.jar", Backup: "mods/c.jar", HadBackup: false},
	}
	if err := writeJournal(stateDir, header, entries); err != nil {
		t.Fatal(err)
	}
	if err := Recover(root); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root, "mods/a.jar"); string(got) != "old content" {
		t.Fatalf("a.jar = %q", got)
	}
	if got := read(t, root, "mods/b.jar"); string(got) != "old b" {
		t.Fatalf("b.jar = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "mods", "c.jar")); !os.IsNotExist(err) {
		t.Fatal("c.jar should have been removed")
	}
	if _, err := os.Stat(filepath.Join(stateDir, journalName)); !os.IsNotExist(err) {
		t.Fatal("journal should be removed")
	}
	// 幂等：再次回滚不应报错。
	if err := Recover(root); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadResumeWithRange(t *testing.T) {
	payload := makeContent(512<<10, "range")
	sum := sha256Hex(payload)
	var gotRange string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/blobs/"+sum {
			http.NotFound(w, r)
			return
		}
		gotRange = r.Header.Get("Range")
		http.ServeContent(w, r, sum, testModTime, bytes.NewReader(payload))
	}))
	defer server.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sum+".part"), payload[:200<<10], 0o644); err != nil {
		t.Fatal(err)
	}
	var lastDone, lastTotal int64
	dl, err := NewDownloader(server.URL+"/", dir, func(done, total int64) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dl.Fetch(context.Background(), []Blob{{Sha256: sum, Size: int64(len(payload))}}); err != nil {
		t.Fatal(err)
	}
	if gotRange != fmt.Sprintf("bytes=%d-", 200<<10) {
		t.Fatalf("Range header = %q", gotRange)
	}
	if got := readAbs(t, filepath.Join(dir, sum)); !bytes.Equal(got, payload) {
		t.Fatal("downloaded content mismatch")
	}
	if _, err := os.Stat(filepath.Join(dir, sum+".part")); !os.IsNotExist(err) {
		t.Fatal("part file should be removed")
	}
	if lastDone != lastTotal || lastTotal != int64(len(payload)) {
		t.Fatalf("progress = %d/%d", lastDone, lastTotal)
	}
}

func TestDownloadRejectsPlainHTTP(t *testing.T) {
	if _, err := NewDownloader("http://example.com/", t.TempDir(), nil); err == nil {
		t.Fatal("plain http to remote host should be rejected")
	}
	if _, err := ManifestURL("http://example.com/stable/"); err == nil {
		t.Fatal("plain http manifest should be rejected")
	}
	if _, err := ManifestURL("http://127.0.0.1:8080/stable/"); err != nil {
		t.Fatalf("loopback http should be allowed: %v", err)
	}
}

func TestRefreshSkipsProtectedAndStateDir(t *testing.T) {
	root := t.TempDir()
	write(t, root, "mods/a.jar", []byte("a"))
	write(t, root, "saves/world/level.dat", []byte("save"))
	write(t, root, ".cdrlauncher/installed.json", []byte("{}"))
	idx, err := Refresh(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Files["mods/a.jar"]; !ok {
		t.Fatal("mod file missing")
	}
	if _, ok := idx.Files["saves/world/level.dat"]; ok {
		t.Fatal("save file should be skipped")
	}
	if _, ok := idx.Files[".cdrlauncher/installed.json"]; ok {
		t.Fatal("state file should be skipped")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.1.0", "1.0.9", 1},
		{"v5.2", "5.2.0", 0},
		{"2.0", "10.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Fatalf("compareVersions(%s,%s)=%d", c.a, c.b, got)
		}
	}
}

func TestRollbackRestoresSnapshotAndCanRollForward(t *testing.T) {
	root := t.TempDir()
	nextRoot := t.TempDir()
	serverRoot := t.TempDir()

	write(t, root, "mods/changed.jar", []byte("old content"))
	write(t, root, "mods/deleted.jar", []byte("deleted content"))
	write(t, nextRoot, "mods/changed.jar", []byte("new content"))
	write(t, nextRoot, "mods/added.jar", []byte("added content"))

	files, err := manifest.Build(manifest.BuildOptions{Root: nextRoot, BlobsDir: filepath.Join(serverRoot, "blobs")})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(serverRoot)))
	defer server.Close()
	m := &manifest.Manifest{
		Format:  manifest.FormatVersion,
		PackID:  manifest.DefaultPackID,
		Channel: "stable",
		Version: "1.1.0",
		BaseURL: server.URL + "/",
		Files:   files,
		Deletes: []string{"mods/deleted.jar"},
	}
	if err := m.Write(filepath.Join(serverRoot, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	idx, err := Refresh(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	idx.Version = "1.0.0"
	if err := SaveIndex(root, idx); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Root:           root,
		ManifestURL:    server.URL,
		CurrentVersion: "1.0.0",
		PackID:         manifest.DefaultPackID,
		Channel:        "stable",
	}
	if _, err := Update(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root, "mods/changed.jar"); string(got) != "new content" {
		t.Fatalf("changed file = %q", got)
	}

	backups, err := ListBackups(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %+v", backups)
	}
	snapshot := backups[0]
	if !snapshot.CanRollback || snapshot.From != "1.0.0" || snapshot.To != "1.1.0" || snapshot.Files != 2 || snapshot.Deletes != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	res, err := Rollback(context.Background(), root, snapshot.Stamp)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "1.1.0" || res.To != "1.0.0" || res.Restored != 2 || res.Removed != 1 {
		t.Fatalf("unexpected rollback result: %+v", res)
	}
	if got := read(t, root, "mods/changed.jar"); string(got) != "old content" {
		t.Fatalf("rolled back file = %q", got)
	}
	if got := read(t, root, "mods/deleted.jar"); string(got) != "deleted content" {
		t.Fatalf("deleted file not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "mods", "added.jar")); !os.IsNotExist(err) {
		t.Fatal("added file should be removed")
	}
	idx, err = LoadIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Version != "1.0.0" {
		t.Fatalf("index version = %s", idx.Version)
	}
	if _, ok := idx.Files["mods/deleted.jar"]; !ok {
		t.Fatal("restored file missing from index")
	}
	if _, ok := idx.Files["mods/added.jar"]; ok {
		t.Fatal("removed file still in index")
	}
	if _, err := Rollback(context.Background(), root, snapshot.Stamp); err == nil {
		t.Fatal("consumed snapshot should not be reusable")
	}

	backups, err = ListBackups(root)
	if err != nil {
		t.Fatal(err)
	}
	var forward *BackupView
	for i := range backups {
		if backups[i].From == "1.1.0" && backups[i].To == "1.0.0" && backups[i].CanRollback {
			forward = &backups[i]
		}
	}
	if forward == nil {
		t.Fatalf("forward snapshot missing: %+v", backups)
	}
	again, err := Rollback(context.Background(), root, forward.Stamp)
	if err != nil {
		t.Fatal(err)
	}
	if again.To != "1.1.0" {
		t.Fatalf("unexpected roll-forward result: %+v", again)
	}
	if got := read(t, root, "mods/changed.jar"); string(got) != "new content" {
		t.Fatalf("roll-forward changed file = %q", got)
	}
	if got := read(t, root, "mods/added.jar"); string(got) != "added content" {
		t.Fatalf("roll-forward added file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "mods", "deleted.jar")); !os.IsNotExist(err) {
		t.Fatal("file should be deleted again")
	}
	idx, err = LoadIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Version != "1.1.0" {
		t.Fatalf("index version = %s", idx.Version)
	}
}

var testModTime = mustTime("2026-10-24T00:00:00Z")

func mustTime(s string) (t time.Time) {
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return parsed
}

func makeContent(size int, tag string) []byte {
	out := make([]byte, size)
	chunk := []byte(strings.Repeat("0123456789abcdef", 64))
	for i := range out {
		out[i] = chunk[i%len(chunk)]
	}
	if len(tag) > 0 {
		copy(out[size/2:], []byte(strings.Repeat(tag, 1024)))
	}
	return out
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func write(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeAbs(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) []byte {
	t.Helper()
	return readAbs(t, filepath.Join(root, filepath.FromSlash(rel)))
}

func readAbs(t *testing.T, p string) []byte {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func containsPhase(phases []string, want string) bool {
	for _, p := range phases {
		if p == want {
			return true
		}
	}
	return false
}
