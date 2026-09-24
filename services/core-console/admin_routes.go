package main

import (
	"net/http"
	"strings"
)

// adminAPIRequest exposes only the management contract; asset creation and
// execution remain on the direct Core API.
func adminAPIRequest(r *http.Request) bool {
	const prefix = "/core/v1/admin/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	remove := r.Method == http.MethodDelete
	if len(parts) == 1 {
		switch parts[0] {
		case "projects":
			return read || r.Method == http.MethodPost
		case "copies":
			return r.Method == http.MethodPost
		case "summary", "runtime-observations", "startup-configuration", "audit-log", "core-metrics":
			return read
		}
	}
	if len(parts) == 2 && parts[0] == "runtime-history" && parts[1] == "capabilities" {
		return read
	}
	if len(parts) < 2 || parts[0] != "projects" {
		return false
	}
	if len(parts) == 2 {
		return r.Method == http.MethodPost
	}
	if len(parts) == 3 && parts[2] == "archive" {
		return r.Method == http.MethodPost
	}
	if parts[2] == "keys" {
		return len(parts) == 3 && (read || r.Method == http.MethodPost) || len(parts) == 4 && remove
	}
	resource := parts[2]
	if len(parts) == 3 {
		switch resource {
		case "agents", "skills", "environment-templates", "files", "vaults", "sessions", "resource-owners", "write-operations":
			return read
		}
	}
	if len(parts) == 4 {
		switch resource {
		case "agents", "skills", "environment-templates", "files", "vaults", "sessions":
			return read || remove
		}
	}
	switch resource {
	case "skills":
		if len(parts) == 5 && (parts[4] == "content" || parts[4] == "versions") {
			return read
		}
		if len(parts) == 6 && parts[4] == "versions" {
			return read || remove
		}
		return len(parts) == 7 && parts[4] == "versions" && parts[6] == "content" && read
	case "vaults":
		if len(parts) == 5 && parts[4] == "credentials" {
			return read
		}
		return len(parts) == 6 && parts[4] == "credentials" && (read || remove)
	case "sessions":
		if len(parts) == 5 {
			switch parts[4] {
			case "turns", "items", "artifacts", "execution-configuration", "runtime-observation", "runtime-history":
				return read
			}
		}
		if len(parts) == 6 {
			if parts[4] == "turns" {
				return read
			}
			if parts[4] == "artifacts" {
				return read || remove
			}
		}
		return len(parts) == 7 && parts[4] == "artifacts" && parts[6] == "content" && read
	}
	return false
}
