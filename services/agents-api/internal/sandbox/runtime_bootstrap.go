package sandbox

import "github.com/MiniMax-AI-Dev/parsar/internal/runtimebootstrap"

// RuntimeConnection projects provider allocation input into Runtime's public
// startup contract. Providers must never construct a private auth profile.
func (b Bootstrap) RuntimeConnection() runtimebootstrap.Connection {
	return runtimebootstrap.Connection{
		Version: runtimebootstrap.Version, CoreURL: b.CoreURL,
		DeviceID: b.DeviceID, Credential: b.Credential,
	}
}
