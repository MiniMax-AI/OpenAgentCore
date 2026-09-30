package sandboxwire

import (
	"crypto/rand"
	"encoding/hex"
)

// ID is an opaque 16-byte identifier, such as an OperationID, AttachmentID or
// ServerInstanceID. The zero ID is invalid wherever an ID is required.
type ID [16]byte

// NewID returns a random nonzero ID.
func NewID() ID {
	var id ID
	for id.IsZero() {
		rand.Read(id[:])
	}
	return id
}

func (id ID) IsZero() bool { return id == ID{} }

func (id ID) String() string { return hex.EncodeToString(id[:]) }

// Effect says whether a failed request may have taken effect. Transport loss
// after dispatch is EffectPossible unless the server later establishes the
// result.
type Effect uint16

const (
	EffectNone     Effect = 1
	EffectPossible Effect = 2
)

func (e Effect) Valid() bool { return e == EffectNone || e == EffectPossible }
