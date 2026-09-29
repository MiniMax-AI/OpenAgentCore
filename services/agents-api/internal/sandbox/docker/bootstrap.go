package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"io"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/moby/moby/client"
)

type entry struct {
	name      string
	content   []byte
	directory bool
}

func (p *Provider) bootstrap(ctx context.Context, id string, b sandbox.Bootstrap) error {
	// Deliver the Runtime-owned connection contract before native work can start.
	auth, e := b.RuntimeConnection().Marshal()
	if e != nil {
		return e
	}
	if e = copyRuntimeFiles(ctx, p.client, id, "/home", []entry{
		{name: "runtime", directory: true}, {name: "runtime/.oac", directory: true},
		{name: "runtime/runtime-bootstrap.json", content: auth},
	}); e != nil {
		return e
	}
	return copyRuntimeFiles(ctx, p.client, id, "/environment", []entry{{name: "workspace", directory: true}, {name: "staging", directory: true}, {name: "initialization", directory: true}, {name: "packages", directory: true}})
}
func copyRuntimeFiles(ctx context.Context, c *client.Client, id, path string, entries []entry) error {
	var content bytes.Buffer
	writer := tar.NewWriter(&content)
	for _, file := range entries {
		header := &tar.Header{Name: file.name, Uid: 1000, Gid: 1000, Mode: 0600, Size: int64(len(file.content)), Typeflag: tar.TypeReg}
		if file.directory {
			header.Mode = 0700
			header.Typeflag = tar.TypeDir
		}
		if e := writer.WriteHeader(header); e != nil {
			return e
		}
		if _, e := writer.Write(file.content); e != nil {
			return e
		}
	}
	if e := writer.Close(); e != nil {
		return e
	}
	_, e := c.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: path, Content: io.Reader(&content), CopyUIDGID: true})
	return e
}
