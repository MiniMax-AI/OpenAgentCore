//go:build !linux

package cli

func execRuntimeMCP(mcpInvocation) error { return errRuntimeMCP }
