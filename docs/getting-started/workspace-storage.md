---
title: "Set up workspace storage"
---

Keep Session workspaces on an NFS filesystem outside the sandbox nodes, with or without per-workspace quotas. This guide is for operators who run microsandbox and want workspaces to outlive their compute.

The supported combination is microsandbox with the [kernel NFS adapter](../workspace-provider.md#kernel-nfs-adapter). Docker and E2B do not support independent workspace storage; see [Choose a sandbox backend](./sandbox-backends.md). The [Configuration reference](../configuration.md#independent-workspace-storage) lists where the setting lives.

```mermaid
flowchart LR
  A[Prepare NFS export] --> B[Prepare oac-node]
  B --> C[Mount into Core]
  C --> D[Select the storage]
  D --> E[Configure microsandbox]
  E --> F[Add nodes]
  F --> G[Validate with a Session]
```

## Prepare the NFS export

Mount the same NFSv4.2 export on the Linux Core host and on every participating node before starting their services, at the same absolute path, for example `/srv/oac-workspaces`. You manage the export, mount availability and service startup ordering. Use a trusted-client AUTH_SYS export with `root_squash`.

> [!WARNING]
> Do not enable `no_root_squash` or broaden permissions to make a check pass.

Prepare the namespace and service principals according to the adapter's [ownership requirements](../workspace-provider.md#kernel-nfs-adapter).

## Prepare the node account

Core's Compose service already runs as UID 65532. Before adding a node, prepare `oac-node` with the same UID, home `/var/lib/oac-node`, a nologin shell and a nonzero primary group. Its supplementary groups may contain only its primary group, `docker` and `kvm`; an existing home must belong to that account. The node installer adopts this account. Without a precreated account it allocates a system UID, which need not match Core.

Resolve an existing UID or account conflict as a host administration task before enrollment. Changing a running account's UID is not part of storage setup.

## Mount the export into Core

Add a read-write bind mount to the existing `core` service's `volumes` through the deployment platform's Compose customization, for example in `compose.override.yaml`. Keep the existing service volumes and user unchanged.

```yaml
services:
  core:
    volumes:
      - type: bind
        source: /srv/oac-workspaces
        target: /srv/oac-workspaces
        read_only: false
        bind:
          create_host_path: false
```

The host source must already be the NFS mount. `create_host_path: false` prevents creating a missing source directory but does not establish that NFS is mounted. Mount before creating the container; after a host unmount or remount, recreate it so its mount namespace uses the intended mount.

## Select the storage

Open **Settings → Workspace storage** and choose **Select new configuration**, or use `PUT /core/v1/workspace-storage` with the Core key as described in the [Core administration API](../../contracts/agents-api/admin-api.md#workspace-storage). The form accepts the adapter identifier, its parameters as a JSON object and an optional write-only credential JSON object. Core validates these adapter-owned values; Web defines no provider-specific storage fields.

For first-time setup, generate separate canonical UUIDs for the immutable configuration `id` and the namespace marker, then submit this shape with your actual values. Restores retain their existing identities.

```json
{
  "id": "11111111-1111-4111-8111-111111111111",
  "adapter": "nfs",
  "parameters": {
    "root": "/srv/oac-workspaces",
    "namespace_id": "22222222-2222-4222-8222-222222222222",
    "uid": 65532
  },
  "tenant_capacity_mib": 0
}
```

Selection runs the adapter's availability, ownership and xattr checks in Core. It does not prove that every node can resolve the mount. This configuration enforces no per-workspace quota, so `tenant_capacity_mib` must be `0`. For bounded workspaces, set up [quota mode](#quota-mode-storage-server) and select a quota mode configuration with a [tenant capacity](../configuration.md#tenant-capacity) instead.

**Edit tenant capacity** keeps the selected immutable configuration. Selecting another configuration requires a new ID and does not migrate existing workspaces.

## Configure microsandbox and add nodes

Create or update the microsandbox deployment through the [sandbox deployment API](../../contracts/agents-api/sandbox-deployment.md#routes), keeping the required compute resources, Runtime and provider configuration. `resources.environment_disk_mib` is each new workspace's capacity: `0` without quota mode, where a positive value is rejected, and at least 1024 MiB in quota mode, where `0` is refused at workspace admission. Existing workspaces keep the capacity they were admitted with when the deployment changes.

Deployment workspace requirements and each Session's attachment come from the selected database configuration. The selected configuration is the only storage setting.

Then use **Nodes → Add node** on each prepared host; see [Add and manage nodes](./nodes.md).

## Validate with a Session

Connected, ready compute alone does not qualify an external filesystem. Create a Session and read and write files through each participating node before admitting application traffic. Include the independent namespace in the [stopped-write backup and restore procedure](./operations.md#back-up).

## Quota mode storage server {#quota-mode-storage-server}

Quota mode bounds each workspace's allocated bytes and inodes with an XFS project quota on the NFS server, set by the `oac-workspace-quota` command that Core calls over a restricted SSH forced command. The [Workspace filesystem protocol](../workspace-provider.md#quota-mode) owns the protocol and enforcement semantics. Prepare the storage server as its administrator:

1. Put the export on a local XFS filesystem mounted with enforcing project quotas (`prjquota`, not `pqnoenforce`). The export root must be a subdirectory of that mount, for example mount `/srv/xfs` and export `/srv/xfs/oac-workspaces`; the command keeps its journal in `/srv/xfs/.oac-quota`, outside the export. Prepare the namespace root and marker as above.
2. Install `xfsprogs` and Python 3.9 or newer. Install `storage/oac-workspace-quota` from the matching distribution as `/usr/local/sbin/oac-workspace-quota`, owned by root with mode `0755`.
3. Write `/etc/oac/workspace-quota.json`, owned by root and not writable by group or others: `{"export_root": "/srv/xfs/oac-workspaces", "mount_point": "/srv/xfs"}`.
4. Reserve a project ID range that no other project on this filesystem uses, then initialize the journal once: `oac-workspace-quota --config /etc/oac/workspace-quota.json init --projid-start 100000 --projid-end 199999`. It refuses to run over an existing journal; never initialize restored storage.
5. Generate a dedicated key pair, for example `ssh-keygen -t ed25519 -N '' -f oac-workspace-quota`. Authorize its public key for one account with a working shell, such as `oac-quota`, in that account's `authorized_keys`: `restrict,from="<Core address>",command="sudo -n /usr/local/sbin/oac-workspace-quota --config /etc/oac/workspace-quota.json serve" ssh-ed25519 AAAA...`, with the sudoers entry `oac-quota ALL=(root) NOPASSWD: /usr/local/sbin/oac-workspace-quota --config /etc/oac/workspace-quota.json serve`. Alternatively authorize the key for root with the same options and the command without `sudo -n` under `PermitRootLogin forced-commands-only`. Keep `PermitUserEnvironment no`.
6. Read the server's public host key from `/etc/ssh/` on the server itself, not from an unauthenticated network scan.

Select the quota mode configuration with the private key as its write-only credential. Delete the local private key file after a successful selection; Core keeps it encrypted with `secrets/core/credential.key`:

```json
{
  "id": "33333333-3333-4333-8333-333333333333",
  "adapter": "nfs",
  "parameters": {
    "root": "/srv/oac-workspaces",
    "namespace_id": "22222222-2222-4222-8222-222222222222",
    "uid": 65532,
    "quota": {
      "endpoint": "storage.internal:22",
      "user": "oac-quota",
      "host_key": "ssh-ed25519 AAAA..."
    }
  },
  "credential": {
    "ssh_private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----\n"
  },
  "tenant_capacity_mib": 65536
}
```

Selection calls the command's read-only check through the pinned host key. Then update the deployment with the per-workspace capacity in `resources.environment_disk_mib`. Until that update, an installation that switched from a configuration without quotas refuses new workspaces; existing workspaces continue with their own configuration.

The credential belongs to the immutable configuration. To rotate it, authorize a new key, select a new configuration ID with it, and keep the old key authorized until no workspace bound to the old configuration is still being created.

Supported recovery for quota mode is a block-level copy of the XFS filesystem; see [Back up](./operations.md#back-up).
