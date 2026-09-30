// Package sessions owns the Session vocabulary: Sessions, Turns and their
// statuses, inputs, Environments and their provisioning failures, function
// calls, Item and Artifact reads, executor credentials, and the errors Session
// operations return. It also decides what Session writes publish: the public
// changes that report Turn and Session transitions, what a Turn that ends
// settles, measured Turn usage and the Session activity each change reports.
// The Session writes that several operations share are procedures here, over
// the transaction interfaces declared beside them: cancelling work, failing
// and terminating an Environment, tracking input activity and the admission
// gates.
package sessions
