---
title: "Choose a sandbox backend"
---

Compare microsandbox, Docker and E2B, then select one in Web. This guide is for operators who finished [Install Core and Web](./install.md) and need compute for Core-hosted Sessions.

The installer selects no backend. One installation uses one backend. To change it later, reset the deployment first; see [Change the sandbox configuration](./nodes.md#change-the-sandbox-configuration).

## Compare the backends

| | microsandbox (recommended) | Docker | E2B |
| --- | --- | --- | --- |
| Where sandboxes run | A microVM on your Linux nodes | A container on your Linux nodes | E2B's cloud |
| Nodes to add | Yes | Yes | None |
| You provide | Linux amd64 hosts with systemd and `/dev/kvm` | Linux amd64 hosts with systemd and rootful Docker Engine | An E2B API key and a template; a public HTTPS URL for Core |
| Isolation | Each sandbox has its own kernel | Containers share the node's kernel | E2B's |
| Idle sandboxes suspend and resume | Yes, with checkpoints | No | No |
| Workspace storage | Required, on NFS | Own disk | Own disk |
| Disk limits | Root disk and workspace quota | None | Set by the template |

The field-level differences are in the [deployment contract](../../contracts/agents-api/sandbox-deployment.md#what-each-field-means-per-sandbox-provider). Host requirements for nodes are in [Add and manage nodes](./nodes.md#host-requirements).

Docker nodes are [root-equivalent](./nodes.md#what-the-installer-sets-up) on their host: add them only on hosts dedicated to sandboxes.

Sandbox nodes run on Linux amd64. When Core runs on macOS or Windows, connect a Linux node or use E2B. Applications can also run [self-hosted Sessions](./self-hosted.md) on their own machines, with no backend.

## Select the backend in Web

1. Open **Settings → Sandbox and scheduling**. The setup wizard asks **Where should sandboxes run?**: **Own machines** (microsandbox or Docker) or **E2B**.
2. For **Own machines**, choose the backend and the sandbox size. Web proposes the [default size the Provider declares](../sandbox-provider.md#register-the-provider-kind) as **Standard**, and **Advanced settings** on the review page holds the exact CPU, memory and disk values. E2B declares no default size, so its review page has no **Advanced settings**.
3. Choose **Save configuration**. The choice is stored in Core's database.

For **Own machines**, continue with [Add and manage nodes](./nodes.md). For microsandbox, first [Set up workspace storage](./workspace-storage.md). For checkpoint recovery across microsandbox nodes, see [Checkpoint recovery across nodes](./nodes.md#checkpoint-recovery-across-nodes).

## Use E2B

E2B needs no nodes, but its sandboxes call Core from E2B's cloud, so Core needs a public HTTPS URL that is not loopback; see [Configure the public address](./public-address.md).

1. Build a template from the Runtime image with [Build a template](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md#build-a-template). Its output records the immutable `templateID:build_UUID` value.
2. In the setup wizard, choose **E2B**. Under **Connect E2B**, pick a **Service** or enter a custom **Sandbox API URL** and **Sandbox data-plane domain**, enter the **API key**, then select the **Template build** from the list Web loads with that key, template first and then its ready build. The key must belong to the team that owns the template.
3. Choose **Save configuration**. Core stores the key encrypted and never returns it.

When you edit a saved E2B configuration, leave the **API key** blank to keep the saved key. The **Template build** then stays an editable text field holding the saved `templateID:build_UUID`. Entering a key makes Web load the list again.

The key and template rules, including key replacement, are in [Initial setup and same-provider changes](../../contracts/agents-api/sandbox-deployment.md#initial-setup-and-same-provider-changes).

## Next steps

- microsandbox or Docker: [Add and manage nodes](./nodes.md).
- Workspaces on NFS: [Set up workspace storage](./workspace-storage.md).
- Models and keys: continue with [Install Core and Web](./install.md#set-a-default-model).
