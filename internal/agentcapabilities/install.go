package agentcapabilities

import (
	"encoding/json"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

// Tree is one directory of an installation, relative to the installation,
// and the files it holds.
type Tree struct {
	Root  string
	Files []agentbundle.File
}

// SkillTree is the tree that stages a Skill archive.
func SkillTree(archive []byte, metadata agentskill.Metadata) (Tree, error) {
	files, err := agentskill.Read(archive, metadata)
	if err != nil || !validRelative(metadata.Name) || strings.Contains(metadata.Name, "/") {
		return Tree{}, ErrInvalid
	}
	return Tree{Root: "skills/" + metadata.Name, Files: files}, nil
}

// PluginTree is the tree that stages a Plugin archive in slot.
func PluginTree(slot int, archive []byte, metadata agentplugin.Metadata) (Tree, error) {
	if slot < 0 || slot >= 50 {
		return Tree{}, ErrInvalid
	}
	bundle, err := agentplugin.Read(archive, metadata)
	if err != nil {
		return Tree{}, ErrInvalid
	}
	return Tree{Root: "plugins/" + strconv.Itoa(slot), Files: bundle.Files}, nil
}

// Snapshot completes a staged installation once setup has run: it captures
// the declared directories and binds the manifest to the selection. It is an
// installation artifact, not another lifecycle or public catalog.
type Snapshot struct {
	manifest    Manifest
	directories int
	captured    int
	total       int
}

// NewSnapshot checks the staged installation against input. list lists one
// of the installation's directories, and read reads one of its trees, whose
// files must be read-only. Only selected, fully imported bundles may precede
// finalization: a leftover directory capture or temporary manifest is an
// incomplete installation, not a reason to silently recapture mutable
// sources.
func NewSnapshot(input Input, identity Identity, list func(string) ([]fs.DirEntry, error), read func(string) ([]agentbundle.File, error)) (*Snapshot, error) {
	digest, err := selectionHash(input)
	if err != nil || validateIdentity(identity) != nil || validateStaged(list, input) != nil {
		return nil, ErrInvalid
	}
	s := &Snapshot{manifest: Manifest{Version: 1, Identity: identity, SelectionSHA256: digest, Skills: []InstalledSkill{}}, directories: len(input.Directories)}
	for _, metadata := range input.Skills {
		if !validRelative(metadata.Name) || strings.Contains(metadata.Name, "/") {
			return nil, ErrInvalid
		}
		root := "skills/" + metadata.Name
		files, err := read(root)
		if err != nil || s.count(files) != nil {
			return nil, ErrInvalid
		}
		body, ok := fileData(files, "SKILL.md")
		if !ok || agentskill.ValidateManifest(body, metadata) != nil || s.manifest.add(metadata, root, root) != nil {
			return nil, ErrInvalid
		}
	}
	for slot, expected := range input.Plugins {
		root := "plugins/" + strconv.Itoa(slot)
		files, err := read(root)
		if err != nil || s.count(files) != nil {
			return nil, ErrInvalid
		}
		bundle, err := agentplugin.Inspect(files)
		if err != nil || bundle.Metadata != expected || addPlugin(&s.manifest, root, bundle) != nil {
			return nil, ErrInvalid
		}
	}
	return s, nil
}

// Capture records the files of the next selected directory, in input order,
// and returns the tree to write before the next capture.
func (s *Snapshot) Capture(files []agentbundle.File) (Tree, error) {
	if s.captured >= s.directories || s.count(files) != nil {
		return Tree{}, ErrInvalid
	}
	root := "directories/" + strconv.Itoa(s.captured)
	if discover(&s.manifest, root, files) != nil {
		return Tree{}, ErrInvalid
	}
	s.captured++
	return Tree{Root: root, Files: files}, nil
}

// Manifest is the manifest to publish, last, as ManifestName once every
// directory is captured.
func (s *Snapshot) Manifest() ([]byte, error) {
	if s.captured != s.directories {
		return nil, ErrInvalid
	}
	body, err := json.Marshal(s.manifest)
	if err != nil || len(body) > MaxManifestBytes {
		return nil, ErrInvalid
	}
	return body, nil
}

func (s *Snapshot) count(files []agentbundle.File) error {
	for _, file := range files {
		s.total += len(file.Data)
	}
	if s.total > MaxSnapshotBytes {
		return ErrInvalid
	}
	return nil
}

func addPlugin(manifest *Manifest, root string, bundle agentplugin.Bundle) error {
	if len(bundle.MCP) != 0 && manifest.addMCPPackage(root) != nil {
		return ErrInvalid
	}
	for _, skill := range bundle.Skills {
		if err := manifest.add(skill.Metadata, path.Join(root, skill.RelativeRoot), root); err != nil {
			return err
		}
	}
	return nil
}

func discover(manifest *Manifest, root string, files []agentbundle.File) error {
	for _, file := range files {
		if file.Path == ".codex-plugin/plugin.json" {
			bundle, err := agentplugin.Inspect(files)
			if err != nil {
				return ErrInvalid
			}
			return addPlugin(manifest, root, bundle)
		}
	}
	before := len(manifest.Skills)
	for _, file := range files {
		if path.Base(file.Path) != "SKILL.md" {
			continue
		}
		metadata, err := agentskill.InspectManifest(file.Data)
		if err != nil || manifest.add(metadata, path.Join(root, path.Dir(file.Path)), root) != nil {
			return ErrInvalid
		}
	}
	if before == len(manifest.Skills) {
		return ErrInvalid
	}
	return nil
}

// Check decodes a published manifest and checks the packages it names, which
// read reads as NewSnapshot's read does. It never reads the original sources,
// even after a native or Core restart. The manifest it returns carries the
// MCP servers its plugin packages declare.
func Check(body []byte, read func(string) ([]agentbundle.File, error)) (Manifest, error) {
	manifest, err := decodeManifest(body)
	if err != nil {
		return Manifest{}, err
	}
	packages := map[string][]agentbundle.File{}
	total := 0
	load := func(name string) ([]agentbundle.File, error) {
		if files, ok := packages[name]; ok {
			return files, nil
		}
		files, err := read(name)
		if err != nil {
			return nil, ErrInvalid
		}
		for _, file := range files {
			total += len(file.Data)
		}
		if total > MaxSnapshotBytes {
			return nil, ErrInvalid
		}
		packages[name] = files
		return files, nil
	}
	for _, skill := range manifest.Skills {
		files, err := load(skill.PackageRoot)
		if err != nil {
			return Manifest{}, err
		}
		// decodeManifest checked that the Skill lies in its package.
		inner := strings.TrimPrefix(strings.TrimPrefix(skill.RelativeRoot, skill.PackageRoot), "/")
		body, ok := fileData(files, path.Join(inner, "SKILL.md"))
		if !ok || agentskill.ValidateManifest(body, skill.Metadata) != nil {
			return Manifest{}, ErrInvalid
		}
	}
	for _, name := range manifest.Plugins {
		files, err := load(name)
		if err != nil {
			return Manifest{}, err
		}
		bundle, err := agentplugin.Inspect(files)
		if err != nil || len(bundle.MCP) == 0 {
			return Manifest{}, ErrInvalid
		}
		for _, server := range bundle.MCP {
			manifest.MCP = append(manifest.MCP, InstalledMCP{PackageRoot: name, Server: server})
		}
	}
	return manifest, nil
}

func validateStaged(list func(string) ([]fs.DirEntry, error), input Input) error {
	expected := map[string]map[string]bool{"skills": {}, "plugins": {}}
	for _, skill := range input.Skills {
		expected["skills"][skill.Name] = true
	}
	for slot := range input.Plugins {
		expected["plugins"][strconv.Itoa(slot)] = true
	}
	entries, err := list(".")
	if err != nil {
		return ErrInvalid
	}
	for _, entry := range entries {
		selected, ok := expected[entry.Name()]
		if !ok || !entry.IsDir() {
			return ErrInvalid
		}
		children, err := list(entry.Name())
		if err != nil || len(children) != len(selected) {
			return ErrInvalid
		}
		for _, child := range children {
			if !child.IsDir() || !selected[child.Name()] {
				return ErrInvalid
			}
		}
	}
	return nil
}

func fileData(files []agentbundle.File, name string) ([]byte, bool) {
	for _, file := range files {
		if file.Path == name {
			return file.Data, true
		}
	}
	return nil, false
}
