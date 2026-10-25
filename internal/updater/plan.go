package updater

import (
	"fmt"
	"sort"

	"cdr-launcher/internal/manifest"
)

// Kind 是更新动作类型。
type Kind string

const (
	KindKeep     Kind = "keep"
	KindDownload Kind = "download"
	KindPatch    Kind = "patch"
	KindDelete   Kind = "delete"
	KindSkip     Kind = "skip"
)

// Action 是对单个文件的操作决定。
type Action struct {
	Kind   Kind
	Path   string
	Target manifest.File
	Patch  *manifest.Patch
	Local  FileState
	Reason string
}

// BuildPlan 根据目标清单与本地索引计算更新动作。
func BuildPlan(m *manifest.Manifest, idx *Index) ([]Action, error) {
	if m == nil {
		return nil, fmt.Errorf("缺少 manifest")
	}
	if m.Format != manifest.FormatVersion {
		return nil, fmt.Errorf("不支持的 manifest 格式: %d", m.Format)
	}
	if idx == nil || idx.Files == nil {
		idx = &Index{Files: map[string]FileState{}}
	}

	protected := append(append([]string{}, manifest.DefaultProtected...), m.Protected...)
	editable := m.UserEditable
	targets := make(map[string]manifest.File, len(m.Files))
	for _, f := range m.Files {
		rel, err := manifest.CleanRelPath(f.Path)
		if err != nil {
			return nil, err
		}
		if _, dup := targets[rel]; dup {
			return nil, fmt.Errorf("manifest 文件重复: %s", rel)
		}
		if !validSHA(f.Sha256) {
			return nil, fmt.Errorf("%s: sha256 非法", rel)
		}
		if f.Size < 0 {
			return nil, fmt.Errorf("%s: 文件大小非法", rel)
		}
		targets[rel] = manifest.File{Path: rel, Size: f.Size, Sha256: f.Sha256}
	}

	type patchKey struct {
		path   string
		source string
	}
	patches := map[patchKey]manifest.Patch{}
	for _, p := range m.Patches {
		rel, err := manifest.CleanRelPath(p.Path)
		if err != nil {
			return nil, err
		}
		target, ok := targets[rel]
		if !ok {
			continue
		}
		if p.Algo != manifest.PatchAlgo || !validSHA(p.Sha256) || !validSHA(p.SourceSha256) || !validSHA(p.TargetSha256) {
			continue
		}
		if p.TargetSha256 != target.Sha256 || p.TargetSize != target.Size {
			continue
		}
		key := patchKey{path: rel, source: p.SourceSha256}
		if prev, exists := patches[key]; !exists || p.Size < prev.Size {
			p.Path = rel
			patches[key] = p
		}
	}

	actions := make([]Action, 0, len(targets)+len(m.Deletes))
	for rel, target := range targets {
		action := Action{Path: rel, Target: target}
		local, exists := idx.Files[rel]
		action.Local = local
		switch {
		case isProtected(rel, protected):
			action.Kind = KindSkip
			action.Reason = "受保护路径"
		case exists && local.Sha256 == target.Sha256:
			action.Kind = KindKeep
		case exists && isUserEditable(rel, manifest.DefaultUserEditable, editable):
			action.Kind = KindSkip
			action.Reason = "玩家已修改"
		default:
			action.Kind = KindDownload
			if exists {
				if p, ok := patches[patchKey{path: rel, source: local.Sha256}]; ok {
					copied := p
					action.Kind = KindPatch
					action.Patch = &copied
				}
			}
		}
		actions = append(actions, action)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Path < actions[j].Path })

	seenDelete := map[string]bool{}
	for _, raw := range m.Deletes {
		rel, err := manifest.CleanRelPath(raw)
		if err != nil {
			return nil, err
		}
		if seenDelete[rel] {
			continue
		}
		seenDelete[rel] = true
		if _, isTarget := targets[rel]; isTarget {
			continue
		}
		if isProtected(rel, protected) || isUserEditable(rel, manifest.DefaultUserEditable, editable) {
			continue
		}
		local, exists := idx.Files[rel]
		if !exists {
			continue
		}
		actions = append(actions, Action{Kind: KindDelete, Path: rel, Local: local})
	}
	return actions, nil
}

// Blob 描述一个需要从服务器获取的 blob。
type Blob struct {
	Sha256 string
	Size   int64
}

// RequiredBlobs 返回计划所需的下载与补丁 blob，同哈希只保留一份。
func RequiredBlobs(actions []Action) ([]Blob, error) {
	seen := map[string]int64{}
	add := func(sum string, size int64) error {
		if !validSHA(sum) {
			return fmt.Errorf("blob sha256 非法: %s", sum)
		}
		if size < 0 {
			return fmt.Errorf("blob 大小非法: %s", sum)
		}
		if prev, ok := seen[sum]; ok {
			if prev != size {
				return fmt.Errorf("blob %s 大小冲突: %d != %d", sum, prev, size)
			}
			return nil
		}
		seen[sum] = size
		return nil
	}
	for _, action := range actions {
		switch action.Kind {
		case KindDownload:
			if err := add(action.Target.Sha256, action.Target.Size); err != nil {
				return nil, err
			}
		case KindPatch:
			if action.Patch == nil {
				return nil, fmt.Errorf("%s: 补丁动作缺少补丁信息", action.Path)
			}
			if err := add(action.Patch.Sha256, action.Patch.Size); err != nil {
				return nil, err
			}
		}
	}
	out := make([]Blob, 0, len(seen))
	for sum, size := range seen {
		out = append(out, Blob{Sha256: sum, Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sha256 < out[j].Sha256 })
	return out, nil
}

// PlanStats 汇总计划中的变更数量与预计下载体积。
func PlanStats(actions []Action) (files, patchFiles, deletes int, downloadBytes int64) {
	blobs, err := RequiredBlobs(actions)
	if err == nil {
		for _, b := range blobs {
			downloadBytes += b.Size
		}
	}
	for _, action := range actions {
		switch action.Kind {
		case KindDownload:
			files++
		case KindPatch:
			files++
			patchFiles++
		case KindDelete:
			deletes++
		}
	}
	return
}
