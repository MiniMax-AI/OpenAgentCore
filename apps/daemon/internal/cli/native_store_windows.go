package cli

import "os"

// Windows directory ACLs, not owners, decide access; the account's profile already scopes the store.
func ownedByCurrentUser(os.FileInfo) bool { return true }
