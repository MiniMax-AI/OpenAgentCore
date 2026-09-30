package agentcapabilities

import (
	"encoding/json"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

func InstallSkill(root *os.Root, archive []byte, metadata agentskill.Metadata) error {
	if unfinished(root) != nil {
		return ErrInvalid
	}
	files, err := agentskill.Read(archive, metadata)
	if err != nil {
		return ErrInvalid
	}
	return writeTree(root, "skills/"+metadata.Name, files)
}

func InstallPlugin(root *os.Root, slot int, archive []byte, metadata agentplugin.Metadata) error {
	if slot < 0 || slot >= 50 || unfinished(root) != nil {
		return ErrInvalid
	}
	bundle, err := agentplugin.Read(archive, metadata)
	if err != nil {
		return ErrInvalid
	}
	return writeTree(root, "plugins/"+strconv.Itoa(slot), bundle.Files)
}

// Finalize captures declared directories once after setup and binds the snapshot.
// It emits an installation artifact, not another lifecycle or public catalog.
func Finalize(installed *os.Root, input Input, identity Identity, resolve DirectoryResolver) error {
	digest, err := selectionHash(input)
	if err != nil || validateIdentity(identity) != nil || (len(input.Directories) > 0 && resolve == nil) {
		return ErrInvalid
	}
	if _, err := installed.Lstat(ManifestName); !os.IsNotExist(err) {
		return ErrInvalid
	}
	if validateStaged(installed, input) != nil {
		return ErrInvalid
	}
	manifest := Manifest{Version: 1, Identity: identity, SelectionSHA256: digest, Skills: []InstalledSkill{}}
	total := 0
	count := func(files []agentbundle.File) error {
		for _, file := range files {
			total += len(file.Data)
		}
		if total > MaxSnapshotBytes {
			return ErrInvalid
		}
		return nil
	}
	for _, metadata := range input.Skills {
		if !validRelative(metadata.Name) || strings.Contains(metadata.Name, "/") {
			return ErrInvalid
		}
		root := "skills/" + metadata.Name
		files, err := ReadTree(installed, root, true)
		if err != nil || count(files) != nil {
			return ErrInvalid
		}
		body, err := installed.ReadFile(root + "/SKILL.md")
		if err != nil || agentskill.ValidateManifest(body, metadata) != nil || manifest.add(metadata, root, root) != nil {
			return ErrInvalid
		}
	}
	for slot, expected := range input.Plugins {
		root := "plugins/" + strconv.Itoa(slot)
		files, err := ReadTree(installed, root, true)
		if err != nil || count(files) != nil {
			return ErrInvalid
		}
		bundle, err := agentplugin.Inspect(files)
		if err != nil || bundle.Metadata != expected || addPlugin(&manifest, root, bundle) != nil {
			return ErrInvalid
		}
	}
	for slot, source := range input.Directories {
		directory, err := resolve(source)
		if err != nil || directory == nil {
			if directory != nil {
				directory.Close()
			}
			return ErrInvalid
		}
		files, err := ReadTree(directory, ".", false)
		closeErr := directory.Close()
		if closeErr != nil {
			return ErrInvalid
		}
		if err != nil || count(files) != nil {
			return ErrInvalid
		}
		root := "directories/" + strconv.Itoa(slot)
		if discover(&manifest, root, files) != nil || writeTree(installed, root, files) != nil {
			return ErrInvalid
		}
	}
	body, err := json.Marshal(manifest)
	if err != nil || len(body) > 256<<10 || writeFile(installed, ManifestName+".tmp", body, 0400) != nil || installed.Rename(ManifestName+".tmp", ManifestName) != nil {
		return ErrInvalid
	}
	return syncDirectory(installed, ".")
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

// Load checks the protected installation; it never reads the original workspace
// directories, even after a native/Core restart.
func Load(root *os.Root) (Manifest, error) {
	info, err := root.Lstat(ManifestName)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || info.Size() > 256<<10 {
		return Manifest{}, ErrInvalid
	}
	body, err := root.ReadFile(ManifestName)
	if err != nil {
		return Manifest{}, ErrInvalid
	}
	manifest, err := decodeManifest(body)
	if err != nil {
		return Manifest{}, err
	}
	packages := map[string][]agentbundle.File{}
	total := 0
	loadPackage := func(name string) ([]agentbundle.File, error) {
		if files, ok := packages[name]; ok {
			return files, nil
		}
		files, err := ReadTree(root, name, true)
		if err != nil {
			return nil, err
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
		if _, err := loadPackage(skill.PackageRoot); err != nil {
			return Manifest{}, err
		}
		body, err := root.ReadFile(skill.RelativeRoot + "/SKILL.md")
		if err != nil || agentskill.ValidateManifest(body, skill.Metadata) != nil {
			return Manifest{}, ErrInvalid
		}
	}
	for _, name := range manifest.Plugins {
		files, err := loadPackage(name)
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

// Only selected, fully imported bundles may precede finalization. A leftover
// directory capture or temporary manifest is an incomplete installation, not a
// reason to silently recapture mutable sources.
func validateStaged(root *os.Root, input Input) error {
	expected := map[string]map[string]bool{"skills": {}, "plugins": {}}
	for _, skill := range input.Skills {
		expected["skills"][skill.Name] = true
	}
	for slot := range input.Plugins {
		expected["plugins"][strconv.Itoa(slot)] = true
	}
	file, err := root.Open(".")
	if err != nil {
		return ErrInvalid
	}
	entries, err := file.ReadDir(-1)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrInvalid
	}
	for _, entry := range entries {
		selected, ok := expected[entry.Name()]
		if !ok || !entry.IsDir() {
			return ErrInvalid
		}
		directory, err := root.Open(entry.Name())
		if err != nil {
			return ErrInvalid
		}
		children, err := directory.ReadDir(-1)
		closeErr := directory.Close()
		if err != nil || closeErr != nil || len(children) != len(selected) {
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

func unfinished(root *os.Root) error {
	for _, name := range []string{ManifestName, ManifestName + ".tmp"} {
		if _, err := root.Lstat(name); !os.IsNotExist(err) {
			return ErrInvalid
		}
	}
	return nil
}
