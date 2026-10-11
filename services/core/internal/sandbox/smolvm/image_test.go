package smolvm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func digestFor(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func TestVerifyOCIRelease(t *testing.T) {
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	imageID := digestFor(config)
	layer := []byte("live-data")
	layerID := digestFor(layer)
	manifest, _ := json.Marshal(map[string]any{"config": map[string]string{"digest": imageID}, "layers": []map[string]any{{"digest": layerID, "size": len(layer)}}})
	for _, nested := range []bool{false, true} {
		manifestID := digestFor(manifest)
		selected := manifest
		mediaType := "application/vnd.oci.image.manifest.v1+json"
		if nested {
			selected, _ = json.Marshal(map[string]any{"manifests": []map[string]string{{"digest": manifestID, "mediaType": mediaType}}})
			mediaType = "application/vnd.oci.image.index.v1+json"
			manifestID = digestFor(selected)
		}
		index, _ := json.Marshal(map[string]any{"manifests": []map[string]string{{"digest": manifestID, "mediaType": mediaType}}})
		archive := new(bytes.Buffer)
		writer := tar.NewWriter(archive)
		items := []struct {
			name    string
			content []byte
		}{{"blobs/sha256/" + imageID[7:], config}, {"blobs/sha256/" + manifestID[7:], selected}, {"blobs/sha256/" + layerID[7:], layer}, {"index.json", index}}
		if nested {
			items = append(items, struct {
				name    string
				content []byte
			}{"blobs/sha256/" + digestFor(manifest)[7:], manifest})
		}
		for _, item := range items {
			if err := writer.WriteHeader(&tar.Header{Name: item.name, Size: int64(len(item.content)), Mode: 0600}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(item.content); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		altered := filepath.Join(t.TempDir(), "altered.tar")
		if err := os.WriteFile(altered, bytes.Replace(archive.Bytes(), layer, []byte("dead-data"), 1), 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyOCIRelease(altered, manifestID, imageID); err == nil {
			t.Fatal("replaced release layer accepted")
		}
		for _, compressed := range []bool{false, true} {
			data := archive.Bytes()
			suffix := ".tar"
			if compressed {
				compressedData := new(bytes.Buffer)
				gz := gzip.NewWriter(compressedData)
				if _, err := gz.Write(data); err != nil {
					t.Fatal(err)
				}
				if err := gz.Close(); err != nil {
					t.Fatal(err)
				}
				data = compressedData.Bytes()
				suffix = ".tar.gz"
			}
			filename := filepath.Join(t.TempDir(), "runtime"+suffix)
			if err := os.WriteFile(filename, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := verifyOCIRelease(filename, manifestID, imageID); err != nil {
				t.Fatalf("verify nested=%t %s: %v", nested, suffix, err)
			}
			if err := verifyOCIRelease(filename, manifestID, digestFor([]byte("other"))); err == nil {
				t.Fatalf("mismatched image ID accepted for %s", suffix)
			}
			if err := verifyOCIRelease(filename, digestFor([]byte("other")), imageID); err == nil {
				t.Fatalf("mismatched manifest accepted for %s", suffix)
			}
		}
	}
}

func TestVerifyOCIReleaseBindsDockerSaveImage(t *testing.T) {
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	layer := []byte("trusted image layer")
	imageID, layerID := digestFor(config), digestFor(layer)
	manifest, _ := json.Marshal(map[string]any{
		"config": map[string]string{"digest": imageID},
		"layers": []map[string]any{{"digest": layerID, "size": len(layer)}},
	})
	manifestID := digestFor(manifest)
	index, _ := json.Marshal(map[string]any{"manifests": []map[string]string{{"digest": manifestID, "mediaType": "application/vnd.oci.image.manifest.v1+json"}}})
	for _, tc := range []struct {
		name, dockerConfig, dockerLayer string
		contents                        []byte
		valid                           bool
	}{
		{"original", "blobs/sha256/" + imageID[7:], "blobs/sha256/" + layerID[7:], nil, true},
		{"matching alias", "config-alias.json", "layer-alias", layer, true},
		{"other config", "other-config.json", "blobs/sha256/" + layerID[7:], []byte("wrong"), false},
		{"other layer", "blobs/sha256/" + imageID[7:], "other-layer", []byte("wrong"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved, _ := json.Marshal([]map[string]any{{"Config": tc.dockerConfig, "Layers": []string{tc.dockerLayer}}})
			file := filepath.Join(t.TempDir(), "image.tar")
			f, err := os.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			writer := tar.NewWriter(f)
			entries := map[string][]byte{
				"blobs/sha256/" + imageID[7:]:    config,
				"blobs/sha256/" + layerID[7:]:    layer,
				"blobs/sha256/" + manifestID[7:]: manifest,
				"index.json":                     index, "manifest.json": saved,
			}
			if tc.dockerConfig != "blobs/sha256/"+imageID[7:] {
				value := tc.contents
				if tc.valid {
					value = config
				}
				entries[tc.dockerConfig] = value
			}
			if tc.dockerLayer != "blobs/sha256/"+layerID[7:] {
				entries[tc.dockerLayer] = tc.contents
			}
			for name, data := range entries {
				if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))}); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := verifyOCIRelease(file, manifestID, imageID); (err == nil) != tc.valid {
				t.Fatalf("verified=%t want=%t: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestLiveReleaseIdentity(t *testing.T) {
	archive := os.Getenv("OAC_SMOLVM_IMAGE")
	manifest := os.Getenv("OAC_SMOLVM_MANIFEST")
	config := os.Getenv("OAC_SMOLVM_IMAGE_ID")
	if archive == "" || manifest == "" || config == "" {
		t.Skip("set archive and expected release digests")
	}
	if err := verifyOCIRelease(archive, manifest, config); err != nil {
		t.Fatal(err)
	}
}

// Docker-save may reference the unpacked tar while OCI stores a gzip layer.
// The image config's diff_id binds both representations to the same rootfs.
func TestVerifyOCIReleaseDockerUncompressedLayer(t *testing.T) {
	layer := []byte("uncompressed filesystem layer")
	encoded := new(bytes.Buffer)
	gzipWriter := gzip.NewWriter(encoded)
	if _, err := gzipWriter.Write(layer); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := encoded.Bytes()
	layerID := digestFor(compressed)
	config, _ := json.Marshal(map[string]any{
		"architecture": "amd64", "os": "linux", "rootfs": map[string]any{"diff_ids": []string{digestFor(layer)}},
	})
	imageID := digestFor(config)
	manifest, _ := json.Marshal(map[string]any{
		"config": map[string]string{"digest": imageID},
		"layers": []map[string]any{{"digest": layerID, "size": len(compressed)}},
	})
	manifestID := digestFor(manifest)
	index, _ := json.Marshal(map[string]any{"manifests": []map[string]string{{"digest": manifestID, "mediaType": "application/vnd.oci.image.manifest.v1+json"}}})
	saved, _ := json.Marshal([]map[string]any{{"Config": "blobs/sha256/" + imageID[7:], "Layers": []string{"unpacked/layer.tar"}}})
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"unpacked", layer, true},
		{"compressed alias", compressed, true},
		{"tampered unpacked", []byte("tampered filesystem layer"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "image.tar")
			file, err := os.Create(filename)
			if err != nil {
				t.Fatal(err)
			}
			writer := tar.NewWriter(file)
			for _, item := range []struct {
				name string
				data []byte
			}{
				{"index.json", index}, {"manifest.json", saved},
				{"blobs/sha256/" + manifestID[7:], manifest},
				{"blobs/sha256/" + imageID[7:], config},
				{"blobs/sha256/" + layerID[7:], compressed},
				{"unpacked/layer.tar", tc.data},
			} {
				if err := writer.WriteHeader(&tar.Header{Name: item.name, Size: int64(len(item.data)), Mode: 0600}); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(item.data); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := verifyOCIRelease(filename, manifestID, imageID); (err == nil) != tc.valid {
				t.Fatalf("verified=%t want=%t: %v", err == nil, tc.valid, err)
			}
		})
	}
}
