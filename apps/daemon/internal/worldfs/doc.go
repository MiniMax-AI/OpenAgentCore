// Package worldfs serves a Session's world, the sandbox file system, as the FUSE file system a sessionview launcher mounts at the view's root. Every kernel request becomes at most one File request (see internal/sandboxfs), apart from the redial and lock recovery described below, so processes in the view read and write sandbox files natively; nothing on the agent host shows through, and nothing is created in the sandbox to support the view.
//
// [World.Serve] implements [sessionview.World]. Within the context sessionview.Start passes, it dials the attachment's File stream, checks Describe, attaches the export and presents the view's mountpoints; it then serves the launcher's /dev/fuse connection with go-fuse's raw API.
//
// # Mapping
//
// A kernel node ID names a NodeRef, and each kernel lookup holds one server lookup reference; FORGET and BATCH_FORGET release the same counts with Forget. A kernel file handle names a HandleID, and a kernel lock owner is the attachment's LockOwner.
//
//	LOOKUP                   Lookup
//	FORGET, BATCH_FORGET     Forget, batched
//	GETATTR                  GetAttr of the handle when the kernel names one, else of the node
//	SETATTR                  SetAttr; a ctime-only change is GetAttr
//	ACCESS                   Access
//	READLINK                 Readlink
//	MKDIR                    Mkdir
//	UNLINK, RMDIR            Unlink, Rmdir
//	RENAME, RENAME2          Rename: Replace, NoReplace or Exchange; other flags EINVAL
//	LINK, SYMLINK            Link, Symlink
//	CREATE                   Create; O_EXCL is Exclusive
//	OPEN                     Open
//	READ, WRITE              Read, Write; a write that stopped after a prefix is a short write
//	FLUSH                    Flush with the closing lock owner
//	FSYNC, FSYNCDIR          Fsync of the file or directory handle
//	RELEASE, RELEASEDIR      Release, ReleaseDir
//	OPENDIR, READDIR         OpenDir, ReadDir
//	STATFS                   StatFS
//	GETLK                    GetLock
//	SETLK, SETLKW            SetLock, POSIX or flock; SETLKW waits (see Locks)
//	MKNOD                    EOPNOTSUPP
//	GETXATTR, LISTXATTR      ENOSYS: the kernel stops asking and answers EOPNOTSUPP
//	SETXATTR, REMOVEXATTR    ENOSYS, as above
//	FALLOCATE                ENOSYS, as above
//	LSEEK                    ENOSYS: the kernel seeks itself
//	COPY_FILE_RANGE          ENOSYS: the kernel copies with READ and WRITE
//	STATX                    ENOSYS: the kernel uses GETATTR
//	IOCTL                    ENOTTY
//	READDIRPLUS              never negotiated
//
// FIFOs and device nodes in the world are opened by the kernel itself and never reach the sandbox; the world is mounted nodev.
//
// # Locks
//
// Lock requests the service does not declare fail with ENOLCK, so the kernel never locks only within the view. A lock request reports what the service did. An interrupt that arrives before the request is sent, while it waits for its turn, for the stream or for a redial, is EINTR and sends nothing. Once the request is sent, the frontend answers an interrupt with CancelRequest and waits for the request's own response: a lock acquired before the cancellation arrived is reported acquired, and a cancelled request is EINTR. When the service answers that a failed request may have changed the lock (EffectPossible), the frontend unlocks the same owner and range before it reports the failure. When it cannot learn what the request did, because the stream failed, or the unlock fails, it fails the handle and the request returns EIO: every later request on the handle fails with EIO too, and its Release drops any lock the service holds on it.
//
// The requests of one owner on one handle take turns, so that a recovery unlock never removes a lock another request reported. A non-blocking request keeps its turn until its recovery is done. A waiting request (SETLKW) gives up its turn while it waits, so unlocks and interrupts get through, and takes it again only to recover. A request unlocks to recover only when no other acquisition of the owner registered after it and no waiting request was out when it registered; otherwise the unlock could remove a lock another request reported, so the handle fails and the request returns EIO.
//
// # Uncached profile
//
// Entry, attribute and negative timeouts are zero, every open returns FOPEN_DIRECT_IO, and the frontend never asks for the writeback cache, KEEP_CACHE or CACHE_DIR. A private mapping of a world file works, and the kernel reads its pages with READ when they fault; a shared mapping fails with ENODEV, since the frontend does not negotiate DIRECT_IO_ALLOW_MMAP. default_permissions stays off: the service decides access. The frontend negotiates only BIG_WRITES, MAX_PAGES (requests up to the service's read and write limit), PARALLEL_DIROPS, ATOMIC_O_TRUNC, POSIX_LOCKS and FLOCK_LOCKS.
//
// # Errors
//
// A sandboxfs errno maps to its Linux errno through one table, StaleNode and StaleHandle map to ESTALE, and every other failure maps to EIO. Nothing a request did not do is reported as done.
//
// # Identity
//
// Attributes owned by the service's uid or gid, from Describe, show the view's uid or gid from [sessionview.WorldMount]; SetAttr maps them back. Other owners pass through unchanged, and the service decides every change.
//
// # Presentation
//
// The launcher mounts private directories, overlays and shims over mountpoints in the world. The frontend presents each one without touching the sandbox:
//
//   - The mountpoint itself is a synthetic empty read-only directory or regular file that hides any sandbox entry of that name.
//   - A missing ancestor, confirmed by NotFound, is a synthetic directory, 0555 and root-owned, holding only synthetic children.
//   - An existing ancestor directory stays the sandbox directory, and Lookup of a presented name returns the presented node. ReadDir lists every sandbox entry at its own position, a presented name as the presented node, and then the presented names the sandbox does not list.
//   - An ancestor symlink, such as /bin -> usr/bin, stays a symlink. The frontend resolves it with Walk and Readlink inside the world root, at most 40 hops, and presents the mountpoint at the resolved target: a shim declared at /bin/sh is mounted at /usr/bin/sh.
//
// Every entry on the way to a mountpoint is pinned for the view's lifetime: Lookup returns the same node ID, a pinned symlink is read from its pinned target, and creating, removing or renaming a presented name returns EPERM. When the sandbox replaces or removes a pinned entry, the view keeps the pinned entry and [World.Lost] reports [ErrTopologyChanged]. Lookup finds any replacement or removal by its node. ReadDir finds an entry listed with another type, and a removal when an enumeration from offset 0 reaches the end without the name; it compares no inode numbers, so a replacement of the same type is found on Lookup. A loop, a permission failure, a non-directory ancestor or two mountpoints that meet fail Serve with [ErrMountpoint]. Serve returns the resolved, symlink-free path of each mountpoint and the links and directories it presented. Synthetic nodes have frontend-local node IDs and never reach the service.
//
// # Link loss
//
// When the stream fails, requests in flight fail with EIO. The next request redials, sends Describe and continues with the same node and handle tables only when ServerInstanceID is unchanged; a request is sent again only when the stream never sent it. A Forget, Release or ReleaseDir that certainly did nothing, because the stream never sent it or the service refused it with ResourceExhausted and EffectNone, is queued and sent again after the next redial, or after a backoff of up to 2 seconds; one that may have reached the service is never sent again. A new incarnation or an ended attachment marks the world lost: every later request fails with EIO and [World.Lost] closes. The attachment has ended when the service answers StaleAttachment, or when the redial fails with a Link failure that is not retryable (sandboxlink.Code.Retryable), such as LeaseExpired or StaleGeneration; a redial refused with InstanceChanged is a new incarnation.
//
// Detach cannot overtake a request still running on a failed stream, so a Serve that fails detaches only when the export was attached on the stream still in use and nothing else is in doubt. When Attach may have taken effect without a response, when a request may still run on a failed stream, or when Detach fails, Serve fails with [ErrAttachmentDirty], and the owner of the Link attachment ends it; the service releases everything the attachment holds when its lease ends. [World.Stop] detaches without sending what is queued, since Detach drops every reference and handle the attachment holds.
package worldfs
