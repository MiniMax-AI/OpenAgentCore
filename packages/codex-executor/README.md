# Runtime filesystem helpers

This package provides three bounded local filesystem helpers reused by the V1
Runtime: directory listing, atomic file installation and workspace output export.
The daemon, native harness, tools and workspace are colocated. The former separate
Codex executor launcher and registry/Noise route are retired; source is retained
in Git history. The package and helper names remain to avoid unrelated renaming.

## Build and installation

Install Rust 1.95.0 with rustfmt and Clippy and a C toolchain. Build with the
committed Cargo lock:

```sh
make check-agents-executor
make build-agents-executor
```

Build state stays under `~/.oac/`. `CARGO_HOME`, `CARGO_TARGET_DIR` and
`AGENTS_EXECUTOR_BUILD_DIR` select absolute cache/output locations. The build
copies only this package and produces Linux x86_64 GNU binaries. Install helpers
at operator-controlled paths outside the writable workspace. Runtime packaging
supplies the selected harness and its native isolation independently.

## Scoped directory helper

The build emits `oac-codex-directory` for the current directory-listing
adapter gap. Install it at an operator-controlled absolute path on the executor,
outside the writable workspace. Its three argv values are the authorized absolute
workspace root, a relative directory (empty for the root), and a limit of 1–4096.
Runtime invokes it locally with the authorized filesystem policy. No shell or model is involved.
The helper itself is a local program, not an authorization service: the caller
must bind the root to the exact authorized owner and validate the installation.

It opens every directory component without following symlinks, retains directory
descriptors for enumeration and metadata, and stops after the limit plus one
entry. It returns one version-1 JSON response: `directory.entries` contains
`name`, `kind` (`file`, `directory`, `symlink`, `other`) and nullable `size_bytes`;
`directory.truncated` reports lookahead. Only regular files have sizes. Errors
use `error` with no partial entries. Invalid paths/names and oversized responses
are rejected. Descriptor cleanup precedes output; exit zero alone is not success,
since a settled error also returns JSON. Require valid complete JSON, a successful
exit and native output-close receipt before accepting an observation. Unknown
start/termination/transport results remain uncertain and must not be retried as
settled reads.

This one-level observation has no order, paging or snapshot guarantee. Renames may
leave an operation reading the directory it already opened; workspace replacement
and cross-tenant placement remain the caller's responsibility. The helper does
not change stock native filesystem methods, create a daemon connection, or enable
public Files. The package tests qualify the bounded helper; public Runtime acceptance separately
checks authorization and Files behavior.

## Scoped output exporter

`oac-workspace-export` takes one authorized absolute workspace root and
streams regular files beneath its `outputs` directory as a standard tar archive
on stdout. It reuses the directory helper's descriptor-relative path protection;
it does not invoke a shell, model or provider command. The caller must supply the
exact Environment's frozen root and independently authorize the operation.

Require both a complete validated archive and successful process exit before
publishing anything. On failure the stdout prefix may still look like a valid
archive. Never extract it into Core's filesystem. Consumers must bound and stream
individual entries into private storage, then publish only after the entire
capture succeeds. A missing `outputs` directory produces an empty archive.

The exporter rejects symlinks, multiply linked files, special files, device
changes and detected concurrent modifications. It limits each file to 200 MiB
and aggregate bytes to 500 MiB, following the current official Files guide.
Traversal is additionally bounded at 4096 entries, 64 directory levels and
4096-byte relative paths; those are implementation limits, not upstream promises.
This is not a filesystem-wide point-in-time snapshot. Immutable publication,
tenant isolation, Turn ordering and storage cleanup remain Core/Runtime duties;
the helper alone does not enable public Artifacts.

## Scoped file installer

The optional `oac-codex-write` helper addresses two pinned native write
limitations: hard-link targets are modified in place, and base64 encoding a
50 MiB file exceeds the native 64 MiB message bound. Install this helper outside
the writable workspace and invoke it locally through the existing Runtime installer, with restricted network
and the required helper/runtime reads. The qualified
installer policy grants write access to one dedicated per-Environment parent
containing only workspace and staging; ordinary native tools can write only the
workspace. Keep credentials, native history and other Environments outside that
parent. Separate writable mount entries can make rename fail with EXDEV even
when their backing filesystem matches; the helper must reject that layout.
It does not authorize callers or enable Files.create.

Arguments are the authorized absolute root, a nonempty relative file path,
its declared byte count (0–50 MiB), and an existing absolute staging directory,
optionally followed by the mode `create`. The staging directory must be outside
the workspace, not its ancestor, and on the destination filesystem. Symlink
traversal and cross-filesystem replacement are rejected; there is no copy
fallback. The former three-argument private CLI is no longer accepted. Stream those bytes in bounded native stdin
chunks, followed by their 32-byte binary SHA-256 digest. This is one private frame;
there is no second request on that process. The digest terminates the private frame without waiting for EOF.
Extra bytes after the frame are not consumed. A process/write accepted receipt
means queued input, not committed file contents.

Each caller selects its mode explicitly. The four-argument form is the replace
mode of trusted initialization: Core's initial Session file installer and the
Skill installer use it, and older Runtime images understand only this form.
Only the daemon's public Files.create writer passes `create`.

The helper reuses held-directory no-follow traversal. It writes a fresh mode-0600
temporary file in the held staging directory, checks the declared byte count and
digest and syncs the file. In replace mode it requires an existing parent,
rejects existing nonregular targets, then replaces the destination directory
entry with renameat and syncs both directories. Existing hard links retain their
original inode and contents; destination mode/ownership metadata is not preserved.

Create mode never replaces anything. Before reading input it walks the existing
parent components without following links and checks the destination when the
parent exists. After the body is verified it creates each missing parent with
mkdirat (mode 0700, as the initial-file installer does) relative to the held
parent, syncs that parent and reopens the new directory without following links.
It then installs with renameat2(RENAME_NOREPLACE); where the filesystem lacks that
flag, linkat installs the staging inode without replacement and the staging name
is unlinked as best-effort cleanup. An existing symlink or non-directory component,
or an existing destination that is not a directory, reports `unsafe_destination`;
an existing directory reports `destination_directory`. A destination or link that
appears concurrently is never replaced or followed and reports
`unsafe_destination`. A normal rejection happens before anything is created and
leaves nothing behind; only such a race, an I/O error or a device mismatch after
parent creation can leave empty mode-0700 directories.

The operator must protect staging and
its ancestors from native tools and background processes. A dedicated staging
directory per Environment, with native tool write access limited to the workspace
and separate installer access, is the qualified mechanism. Directory naming or
mode 0700 alone does not isolate processes running as the same user. The helper
cannot verify other processes' policies; public admission must bind and validate
this condition. Broad read permission may still expose staging bytes; confidentiality
requires its own placement policy. Concurrent workspace changes do not gain access
to protected staging, but later writers can change the installed file. No snapshot
or exactly-once guarantee is implied.

One version-1 JSON response reports `outcome: completed` with `size_bytes`,
`failed` before installation, or `unknown` if either directory sync fails after installation.
Errors contain only a fixed safe code: `invalid_input` or `write_failed`, and in
create mode also `destination_directory` or `unsafe_destination`. Require a complete response plus observed
native exit/output close; exit zero alone is insufficient. Input errors preserve
the old destination provided staging remains protected; independent workspace
writers can still change that destination themselves. Temporary-file cleanup
is best effort: permission or I/O errors, as well as forced termination, can leave
a `.oac-upload-*` file in the private staging directory. Never interpret it as a
completed upload.
A missing receipt remains unknown and must not trigger automatic replay. This
helper does not fence a replacement owner after remote transport or service loss;
public admission still needs operation ownership and recovery handling.

## Verification boundaries

`make check-agents-executor` runs helper tests, formatting and Clippy. The shared
[public acceptance](../../services/agents-api/tests/official_user_runtime.py)
checks execution, Files/Artifacts, cancellation and recovery through an enrolled
Runtime. It requires real model APIs and independently deployed Core/Runtime;
helper unit tests alone do not establish deployment or complete protocol compatibility.
