package smolvm

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const maxOCIIndexBytes = 1 << 20

// verifyOCIRelease binds the actual archive contents to the Runtime release
// selected by Core. The archive hash alone is insufficient: it has no place in
// the distribution specification and would accept a different Runtime image.
func verifyOCIRelease(filename, manifestDigest, imageID string) error {
	if !validDigest(manifestDigest) || !validDigest(imageID) {
		return errors.New("invalid expected OCI release digests")
	}
	manifestName := "blobs/sha256/" + strings.TrimPrefix(manifestDigest, "sha256:")
	configName := "blobs/sha256/" + strings.TrimPrefix(imageID, "sha256:")
	needed, err := readOCIReleaseBlobs(filename, "index.json", "manifest.json", manifestName, configName)
	if err != nil {
		return err
	}
	manifest, config, index := needed[manifestName], needed[configName], needed["index.json"]
	if !matchingDigest(manifest, manifestDigest) || !matchingDigest(config, imageID) || len(index) == 0 {
		return errors.New("OCI archive does not contain the selected Runtime release")
	}
	var top struct {
		Manifests []ociDescriptor `json:"manifests"`
	}
	if err := json.Unmarshal(index, &top); err != nil {
		return fmt.Errorf("invalid OCI index: %w", err)
	}
	if len(top.Manifests) != 1 || top.Manifests[0].Digest != manifestDigest {
		return errors.New("OCI index does not select the expected Runtime manifest")
	}
	switch top.Manifests[0].MediaType {
	case "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json":
		var platform struct {
			Manifests []ociDescriptor `json:"manifests"`
		}
		if err := json.Unmarshal(manifest, &platform); err != nil {
			return fmt.Errorf("invalid OCI platform index: %w", err)
		}
		if len(platform.Manifests) != 1 || !validDigest(platform.Manifests[0].Digest) || !imageManifestType(platform.Manifests[0].MediaType) {
			return errors.New("OCI release does not select exactly one image manifest")
		}
		inner, err := readOCIReleaseBlobs(filename, "blobs/sha256/"+strings.TrimPrefix(platform.Manifests[0].Digest, "sha256:"))
		if err != nil {
			return err
		}
		manifest = inner["blobs/sha256/"+strings.TrimPrefix(platform.Manifests[0].Digest, "sha256:")]
		if !matchingDigest(manifest, platform.Manifests[0].Digest) {
			return errors.New("OCI release platform manifest digest mismatch")
		}
	default:
		if !imageManifestType(top.Manifests[0].MediaType) {
			return errors.New("OCI release does not select an image manifest")
		}
	}
	var image struct {
		Config ociDescriptor   `json:"config"`
		Layers []ociDescriptor `json:"layers"`
	}
	if err := json.Unmarshal(manifest, &image); err != nil {
		return fmt.Errorf("invalid OCI manifest: %w", err)
	}
	if image.Config.Digest != imageID {
		return errors.New("OCI manifest does not select the expected Runtime image")
	}
	var savedLayers []string
	if raw := needed["manifest.json"]; len(raw) != 0 {
		var saved []struct {
			Config string   `json:"Config"`
			Layers []string `json:"Layers"`
		}
		if err := json.Unmarshal(raw, &saved); err != nil || len(saved) != 1 || !safeTarMember(saved[0].Config) || len(saved[0].Layers) != len(image.Layers) {
			return errors.New("Docker archive manifest does not select the expected Runtime image")
		}
		if saved[0].Config != configName {
			alias, err := readOCIReleaseBlobs(filename, saved[0].Config)
			if err != nil || !matchingDigest(alias[saved[0].Config], imageID) {
				return errors.New("Docker archive configuration differs from the selected Runtime image")
			}
		}
		savedLayers = saved[0].Layers
	}
	var configRootfs struct {
		Rootfs struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err := json.Unmarshal(config, &configRootfs); err != nil {
		return fmt.Errorf("invalid OCI image configuration: %w", err)
	}
	return verifyOCIReleaseLayers(filename, image.Layers, savedLayers, configRootfs.Rootfs.DiffIDs)
}

func safeTarMember(name string) bool {
	return name != "" && name != "." && !path.IsAbs(name) && path.Clean(name) == name && !strings.ContainsRune(name, '\\')
}

type ociDescriptor struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}

func imageManifestType(value string) bool {
	return value == "application/vnd.oci.image.manifest.v1+json" || value == "application/vnd.docker.distribution.manifest.v2+json"
}

// Inspect only explicitly selected metadata blobs. OCI layer data can be many
// gigabytes and must never be buffered in node memory.
func readOCIReleaseBlobs(filename string, names ...string) (map[string][]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var source io.Reader = reader
	if header, err := reader.Peek(2); err == nil && header[0] == 0x1f && header[1] == 0x8b {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		source = gz
	}
	archive := tar.NewReader(source)
	needed := make(map[string][]byte, len(names))
	for _, name := range names {
		needed[name] = nil
	}
	for {
		entry, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read OCI archive: %w", err)
		}
		if _, wanted := needed[entry.Name]; !wanted {
			continue
		}
		if (entry.Typeflag != tar.TypeReg && entry.Typeflag != tar.TypeRegA) || entry.Size < 1 || entry.Size > maxOCIIndexBytes || needed[entry.Name] != nil {
			return nil, errors.New("duplicate, empty or oversized OCI release descriptor")
		}
		data, err := io.ReadAll(io.LimitReader(archive, maxOCIIndexBytes+1))
		if err != nil {
			return nil, err
		}
		needed[entry.Name] = data
	}
	return needed, nil
}

// Check every selected layer against the manifest. The metadata must be read
// first to learn which layer names belong to this release, so stream the layers
// in a second pass instead of retaining potentially multi-GiB blobs in memory.
func verifyOCIReleaseLayers(filename string, layers []ociDescriptor, saved, diffIDs []string) error {
	if len(layers) > 4096 {
		return errors.New("OCI release contains too many layers")
	}
	type expectedLayer struct {
		ociDescriptor
		diffID string
		alias  bool
	}
	wanted := make(map[string]expectedLayer, len(layers)*2)
	for i, layer := range layers {
		if !validDigest(layer.Digest) || layer.Size < 0 {
			return errors.New("invalid OCI release layer descriptor")
		}
		name := "blobs/sha256/" + strings.TrimPrefix(layer.Digest, "sha256:")
		if previous, exists := wanted[name]; exists && previous.ociDescriptor != layer {
			return errors.New("conflicting OCI release layer descriptors")
		}
		wanted[name] = expectedLayer{ociDescriptor: layer}
		if saved != nil {
			if !safeTarMember(saved[i]) {
				return errors.New("invalid Docker archive layer path")
			}
			if saved[i] != name {
				expect := expectedLayer{ociDescriptor: layer, alias: true}
				if len(diffIDs) == len(layers) && validDigest(diffIDs[i]) {
					expect.diffID = diffIDs[i]
				}
				if previous, exists := wanted[saved[i]]; exists && previous != expect {
					return errors.New("Docker archive layer differs from selected Runtime image")
				}
				wanted[saved[i]] = expect
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var source io.Reader = reader
	if header, err := reader.Peek(2); err == nil && header[0] == 0x1f && header[1] == 0x8b {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return err
		}
		defer gz.Close()
		source = gz
	}
	archive := tar.NewReader(source)
	seen := make(map[string]bool, len(wanted))
	for {
		entry, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read OCI release layers: %w", err)
		}
		layer, selected := wanted[entry.Name]
		if !selected {
			continue
		}
		if seen[entry.Name] {
			return errors.New("duplicate OCI release layer blob")
		}
		if entry.Typeflag != tar.TypeReg && entry.Typeflag != tar.TypeRegA {
			return errors.New("OCI release layer has the wrong type")
		}
		compressed := entry.Size == layer.Size
		sha := sha256.New()
		if _, err := io.Copy(sha, archive); err != nil {
			return fmt.Errorf("hash OCI release layer: %w", err)
		}
		actual := "sha256:" + hex.EncodeToString(sha.Sum(nil))
		if !(compressed && actual == layer.Digest) && !(layer.alias && layer.diffID != "" && actual == layer.diffID) {
			return errors.New("OCI release layer digest mismatch")
		}
		seen[entry.Name] = true
	}
	if len(seen) != len(wanted) {
		return errors.New("OCI release layer is missing")
	}
	return nil
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
func matchingDigest(blob []byte, want string) bool {
	if len(blob) == 0 {
		return false
	}
	hash := sha256.Sum256(blob)
	return "sha256:"+hex.EncodeToString(hash[:]) == want
}
