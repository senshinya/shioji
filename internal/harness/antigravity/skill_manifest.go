package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
)

type skillFilter struct {
	includes [][]string
	excludes []string
}

type skillManifestEntry struct {
	Path    string
	Include []string `json:"include_only"`
	Exclude []string
}

func (f skillFilter) narrow(e skillManifestEntry) skillFilter {
	if len(e.Include) != 0 {
		f.includes = append(slices.Clone(f.includes), e.Include)
	}
	f.excludes = append(slices.Clone(f.excludes), e.Exclude...)
	return f
}

func (f skillFilter) matches(names ...string) bool {
	matches := func(entries []string) bool {
		for _, name := range names {
			if slices.Contains(entries, name) {
				return true
			}
		}
		return false
	}
	for _, group := range f.includes {
		if !matches(group) {
			return false
		}
	}
	return !matches(f.excludes)
}

type skillExclusion struct {
	base     string
	patterns []string
}

func skillAliases(path string, bases ...string) []string {
	var aliases []string
	for _, base := range bases {
		if rel, err := filepath.Rel(base, path); err == nil && filepath.IsLocal(rel) {
			aliases = append(aliases, filepath.ToSlash(rel), filepath.ToSlash(filepath.Dir(rel)))
		}
	}
	return aliases
}

func excludedNativeSkill(exclusions []skillExclusion, entry domain.ManagedSkill, folder, linkPath string) bool {
	for _, exclusion := range exclusions {
		aliases := append(skillAliases(entry.Path, exclusion.base), entry.Name, folder)
		if linkPath != "" {
			aliases = append(aliases, skillAliases(filepath.Join(linkPath, folder, "SKILL.md"), exclusion.base)...)
		}
		if !(skillFilter{excludes: exclusion.patterns}).matches(aliases...) {
			return true
		}
	}
	return false
}

type skillManifestSource struct {
	path, relativeBase, fallback string
	scope                        domain.SkillScope
	roots                        []string
}

func skillRelative(path string, roots []string) (base, relative string, ok bool) {
	for _, root := range roots {
		if rel, err := filepath.Rel(root, path); filepath.IsAbs(root) && err == nil && filepath.IsLocal(rel) && len(root) >= len(base) {
			base, relative = root, rel
		}
	}
	return base, relative, base != ""
}

func (s skillManifestSource) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	target := filepath.Join(s.relativeBase, path)
	if s.fallback != "" && s.fallback != s.relativeBase {
		if base, rel, ok := skillRelative(target, s.roots); ok {
			if root, err := os.OpenRoot(base); err == nil {
				_, statErr := root.Stat(rel)
				root.Close()
				if os.IsNotExist(statErr) {
					target = filepath.Join(s.fallback, path)
				}
			}
		}
	}
	return target
}

func (r *runtime) manifestSkills(launch domain.LaunchContext, bases []string, inventory *domain.SkillInventory) ([]skillDirectory, []skillExclusion) {
	var sources []skillManifestSource
	roots := append([]string{launch.CWD}, launch.AdditionalDirectories...)
	shared := r.sharedSkillRoot()
	for _, base := range bases {
		fallback := ""
		for _, ancestor := range bases {
			if rel, err := filepath.Rel(ancestor, base); err == nil && filepath.IsLocal(rel) {
				if info, err := os.Lstat(filepath.Join(ancestor, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
					fallback = ancestor
					break
				}
			}
		}
		for _, config := range projectNativeConfigDirectories {
			sources = append(sources, skillManifestSource{path: filepath.Join(base, config, "skills.json"),
				relativeBase: base, fallback: fallback, scope: domain.SkillProject, roots: roots})
		}
	}
	if r.skillProfile != "" {
		base := filepath.Join(r.skillProfile, "config")
		sources = append(sources, skillManifestSource{path: filepath.Join(base, "skills.json"),
			relativeBase: base, scope: domain.SkillUser, roots: []string{r.skillProfile}})
	}
	var dirs []skillDirectory
	var excluded []skillExclusion
	stack := map[string]bool{}
	budget := 256
	warn := func(message string) {
		if !slices.Contains(inventory.Warnings, message) {
			inventory.Warnings = append(inventory.Warnings, message)
		}
	}
	var scan func(skillManifestSource, skillFilter, int)
	scan = func(source skillManifestSource, filter skillFilter, depth int) {
		if depth > 12 || budget <= 0 || stack[source.path] {
			warn("部分 Skill 清单继承无法展开")
			return
		}
		base, rel, ok := skillRelative(source.path, source.roots)
		if !ok {
			warn("已接纳目录之外的 Skill 来源由 Antigravity 管理")
			return
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			return
		}
		raw, err := readSkillFile(root, rel)
		root.Close()
		if err != nil {
			if !os.IsNotExist(err) {
				warn("部分 Antigravity Skill 清单无法读取")
			}
			return
		}
		var manifest struct {
			Entries, Inherits []skillManifestEntry
			Exclude           []string
		}
		if json.Unmarshal(raw, &manifest) != nil {
			warn("部分 Antigravity Skill 清单无法读取")
			return
		}
		budget--
		stack[source.path] = true
		defer delete(stack, source.path)
		if depth == 0 && len(manifest.Exclude) != 0 {
			excluded = append(excluded, skillExclusion{base: source.relativeBase, patterns: manifest.Exclude})
		} else if depth != 0 {
			filter = filter.narrow(skillManifestEntry{Exclude: manifest.Exclude})
		}
		for _, inherited := range manifest.Inherits {
			if inherited.Path == "" {
				continue
			}
			child := source
			child.path = source.resolve(inherited.Path)
			// AGY keeps the originating project or personal base when
			// resolving paths listed by inherited manifests.
			scan(child, filter.narrow(inherited), depth+1)
		}
		entryRoots := source.roots
		if source.scope == domain.SkillUser && shared != "" {
			entryRoots = append(slices.Clone(entryRoots), shared)
		}
		for _, entry := range manifest.Entries {
			if entry.Path == "" || budget <= 0 {
				continue
			}
			path := source.resolve(entry.Path)
			if root, relative, ok := skillRelative(path, entryRoots); ok {
				dirs = append(dirs, skillDirectory{base: root, relative: relative, scope: source.scope, explicit: true,
					filter: filter.narrow(entry), filterBases: []string{source.relativeBase, path}})
				budget--
			} else {
				warn("已接纳目录之外的 Skill 来源由 Antigravity 管理")
			}
		}
	}
	for _, source := range sources {
		scan(source, skillFilter{}, 0)
	}
	return dirs, excluded
}

func (r *runtime) pluginSkills() []skillDirectory {
	if !filepath.IsAbs(r.skillProfile) {
		return nil
	}
	const path = "config/plugins"
	root, err := os.OpenRoot(r.skillProfile)
	if err != nil {
		return nil
	}
	defer root.Close()
	var preferences struct {
		Plugins map[string]struct{ Enabled *bool }
	}
	if raw, err := readSkillFile(root, "config/config.json"); err == nil {
		if json.Unmarshal(raw, &preferences) != nil {
			return nil
		}
	}
	if _, err := root.Stat(path); err != nil {
		return nil
	}
	folder, err := root.Open(path)
	if err != nil {
		return nil
	}
	items, _ := folder.ReadDir(128)
	folder.Close()
	slices.SortFunc(items, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var dirs []skillDirectory
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		dir := filepath.Join(path, item.Name())
		name := nativeSkillPluginName(root, dir)
		if name == "" {
			continue
		}
		named, installed := preferences.Plugins[name], preferences.Plugins[item.Name()]
		if named.Enabled != nil && !*named.Enabled || installed.Enabled != nil && !*installed.Enabled {
			continue
		}
		dirs = append(dirs, skillDirectory{base: r.skillProfile, relative: filepath.Join(dir, "skills"), scope: domain.SkillManaged})
	}
	return dirs
}

func nativeSkillPluginName(root *os.Root, dir string) string {
	for _, rel := range []string{"plugin.json", ".antigravity-plugin/plugin.json", ".gemini-plugin/plugin.json"} {
		raw, err := readSkillFile(root, filepath.Join(dir, rel))
		if os.IsNotExist(err) {
			continue
		}
		var manifest struct{ Name string }
		if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Name == "" || len(manifest.Name) > 160 ||
			strings.TrimSpace(manifest.Name) != manifest.Name || strings.ContainsAny(manifest.Name, "\x00\r\n") {
			return ""
		}
		return manifest.Name
	}
	return ""
}
