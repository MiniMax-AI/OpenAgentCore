package runtimedevice

import "time"

// ArchivedCancellationReceiptLimit bounds receipt draining after an administrator
// archive. It is not a model/tool timeout and never restarts on heartbeat or retry.
const ArchivedCancellationReceiptLimit = 20 * time.Second

// ArchivedCancellationReceipt authorizes only the original delivery's receipt
// drain. It grants neither Runtime credentials nor permission to start work.
type ArchivedCancellationReceipt struct {
	RunID    string
	Deadline time.Time
}
