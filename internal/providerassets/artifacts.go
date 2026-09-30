// Package providerassets exposes the generated node distribution declarations.
package providerassets

import (
	_ "embed"
	"encoding/json"
)

// Artifact identifies an immutable distribution payload and its installation role.
type Artifact struct {
	Path   string `json:"path"`
	Suffix string `json:"suffix"`
	Role   string `json:"role"`
}

//go:embed catalog.json
var catalogJSON []byte

// Catalog returns an independent copy of the adapter-owned declarations.
func Catalog() map[string][]Artifact {
	var catalog map[string][]Artifact
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic(err)
	}
	return catalog
}
