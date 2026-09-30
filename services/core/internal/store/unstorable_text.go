package store

import "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"

// UnstorableText reports PostgreSQL rejecting text it cannot store; see
// pgunit.IsUnstorableText. Store returns database errors untranslated, so api
// checks them with this until store is deleted.
func UnstorableText(err error) bool { return pgunit.IsUnstorableText(err) }
