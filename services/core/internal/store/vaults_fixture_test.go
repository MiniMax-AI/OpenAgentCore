package store_test

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/vaultpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// fixtureVaults builds the Vault adapter and service on db, as cmd/server does.
// A keyless db leaves the operations that need no credential key available.
func fixtureVaults(db fixtureDB) (*vaultpg.Store, *vaults.Service, error) {
	refresher, err := oauthrefresh.NewClient(nil)
	if err != nil {
		return nil, nil, err
	}
	vaultStore := vaultpg.New(pgunit.NewPool(db.pool), db.cipher)
	vaultService, err := vaults.NewService(vaultStore, refresher)
	return vaultStore, vaultService, err
}
