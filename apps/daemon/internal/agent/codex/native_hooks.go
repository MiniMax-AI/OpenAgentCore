package codex

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type nativeToolHook struct {
	EventName   string `json:"eventName"`
	Command     string `json:"command"`
	Matcher     string `json:"matcher"`
	Enabled     bool   `json:"enabled"`
	IsManaged   bool   `json:"isManaged"`
	Async       bool   `json:"async"`
	SourcePath  string `json:"sourcePath"`
	TrustStatus string `json:"trustStatus"`
}

func readNativeToolHooks(ctx context.Context, rpc *JSONRPCClient, cwd string) ([]nativeToolHook, error) {
	operation, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := rpc.Request(operation, "hooks/list", map[string]any{"cwds": []string{cwd}})
	if err != nil {
		return nil, errors.New("codex: initialized tool hook unavailable")
	}
	var response struct {
		Data []struct {
			CWD    string            `json:"cwd"`
			Hooks  []nativeToolHook  `json:"hooks"`
			Errors []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Data) != 1 || response.Data[0].CWD != cwd || len(response.Data[0].Errors) != 0 {
		return nil, errors.New("codex: initialized tool hook configuration unavailable")
	}
	return response.Data[0].Hooks, nil
}
