package proto

import "encoding/json"

// HarnessConfig is the shared JSON-object wire shape for native model parameters.
// The selected Harness owns its field semantics; no cross-Harness translation is
// implied. Omission and {} mean empty configuration; null, arrays, repeated members
// and encoded objects larger than 16 KiB of serialized UTF-8 bytes are invalid. It
// stays raw on the wire so internal/harnessconfig sees exactly what Core sent.
// Unknown and reserved fields must fail before model input, without echoing
// submitted keys or values. Connection, authentication, execution policy and
// lifecycle are not configurable here. Runtime validates the copied Session
// snapshot against shared native support before adapters configure the direct
// provider connection. The Executor retains that configuration across Turns and
// rebuilds. Incompatible snapshots fail explicitly; no protocol conversion,
// rewrite or migration is performed.
type HarnessConfig = json.RawMessage
