// Package sessions owns the Session vocabulary: Sessions, Turns and their
// statuses, inputs, Environments and their provisioning failures, function
// calls, Item and Artifact reads, executor credentials, and the errors Session
// operations return. It also decides what Session writes publish: the public
// changes that report Turn and Session transitions, what a Turn that ends
// settles, measured Turn usage and the Session activity each change reports.
// The Session writes that several operations share are procedures here, over
// the transaction interfaces declared beside them: cancelling work, failing
// and terminating an Environment, tracking input activity, the admission
// gates, admitting a function result, reading a Turn's required actions,
// appending to a Turn's execution journal and projecting its observations and
// admitted inputs into Items, Turn usage and Subagents. Service runs the pooled
// Session use cases, such as staging a Turn's Artifacts, over Storage, and
// Reader declares the Session reads. ExecutionOperations runs the Session
// writes only the execution owner makes, such as recording function calls and
// their application receipts and journaling a Turn's observations, over the
// lease-bound ExecutionStorage.
package sessions
