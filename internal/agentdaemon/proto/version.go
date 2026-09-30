package proto

// Version identifies the complete Core–Runtime wire contract. Change it when
// removing or changing a payload or its semantics; deploy both endpoints together.
const Version = "0.10.0"

// VersionCompatible accepts only this contract. Patch drift, prerelease suffixes
// and malformed versions do not select an implicit compatibility path.
func VersionCompatible(clientVersion string) bool {
	return clientVersion == Version
}
