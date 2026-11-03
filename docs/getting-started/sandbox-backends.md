---
title: "Choose a sandbox backend"
---

Compare microsandbox, E2B and OpenSandbox, then select one in Web. This guide is for operators who finished [Install Core and Web](./install.md) and need compute for Core-hosted Sessions.

The installer selects no backend. One installation uses one backend. To change it later, reset the deployment first; see [Change the sandbox configuration](./nodes.md#change-the-sandbox-configuration).

## Compare the backends

| | microsandbox | E2B | OpenSandbox |
| --- | --- | --- | --- |
| Where sandboxes run | A microVM on your Linux nodes | E2B's cloud | Your OpenSandbox server, on Docker or Kubernetes |
| Nodes to add | Yes | None | None |
| You provide | Linux amd64 hosts with systemd and `/dev/kvm` | An E2B API key and a template; a public HTTPS URL for Core | The server's URL and API key and a Runtime image built for it; a Core address its sandboxes reach |
| Isolation | Each sandbox has its own kernel | E2B's | OpenSandbox's runtime: containers, or a secure container runtime |
| Idle sandboxes suspend and resume | Yes, with checkpoints | Yes, paused in place by E2B | No; they keep running |
| Workspace storage | Required, on NFS | Own disk | Own disk |
| Disk limits | Root disk and workspace quota | Set by the template | Set by OpenSandbox's runtime |

The field-level differences are in the [deployment contract](../../contracts/agents-api/sandbox-deployment.md#what-each-field-means-per-sandbox-provider). Host requirements for nodes are in [Add and manage nodes](./nodes.md#host-requirements).

Sandbox nodes run on Linux amd64. When Core runs on macOS or Windows, connect a Linux node or use E2B or OpenSandbox. Applications can also run [self-hosted Sessions](./self-hosted.md) on their own machines, with no backend.

## Select the backend in Web

1. Open **Settings → Sandbox and scheduling**. The setup wizard asks **Where should sandboxes run?**: **Own machines** (microsandbox), **E2B** or **OpenSandbox**.
2. For **Own machines**, choose the sandbox size. Web proposes the [default size the Provider declares](../sandbox-provider.md#register-the-provider-kind) as **Standard**, and **Advanced settings** on the review page holds the exact CPU, memory and disk values. E2B declares no default size, so its review page has no **Advanced settings**; OpenSandbox's review page proposes 2 CPUs and 4 GiB of memory and has **Advanced settings**.
3. Choose **Save configuration**. The choice is stored in Core's database.

For **Own machines**, continue with [Add and manage nodes](./nodes.md). For microsandbox, first [Set up workspace storage](./workspace-storage.md). For checkpoint recovery across microsandbox nodes, see [Checkpoint recovery across nodes](./nodes.md#checkpoint-recovery-across-nodes).

## Use E2B

E2B needs no nodes, but its sandboxes call Core from E2B's cloud, so Core needs a public HTTPS URL that is not loopback; see [Configure the public address](./public-address.md).

1. Build a template from the Runtime image with [Build a template](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md#build-a-template). Its output records the immutable `templateID:build_UUID` value.
2. In the setup wizard, choose **E2B**. Under **Connect E2B**, pick a **Service** or enter a custom **Sandbox API URL** and **Sandbox data-plane domain**, enter the **API key**, then select the **Template build** from the list Web loads with that key, template first and then its ready build. The key must belong to the team that owns the template.
3. Choose **Save configuration**. Core stores the key encrypted and never returns it.

When you edit a saved E2B configuration, leave the **API key** blank to keep the saved key. The **Template build** then stays an editable text field holding the saved `templateID:build_UUID`. Entering a key makes Web load the list again.

The key and template rules, including key replacement, are in [Initial setup and same-provider changes](../../contracts/agents-api/sandbox-deployment.md#initial-setup-and-same-provider-changes).

## Use OpenSandbox

OpenSandbox needs no nodes. Its sandboxes call Core's public address, which must not be loopback; see [Configure the public address](./public-address.md). Sandboxes with public internet access need it at a global address ([network cap](../../contracts/agents-api/sandbox-deployment.md#network)).

1. Prepare the server and build the Runtime image as the [OpenSandbox adapter guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/opensandbox/README.md) describes.
2. In the setup wizard, choose **OpenSandbox**. Under **Connect OpenSandbox**, enter the **Server URL**, the **Runtime image** and the **API key**.
3. On **Review and save**, keep the proposed size or change it under **Advanced settings**, then choose **Save configuration**. Core stores the key encrypted and never returns it.

Core does not contact the server when it saves. A wrong URL, key or image shows up when the first Session's sandbox is created. When you edit a saved OpenSandbox configuration, leave the **API key** blank to keep the saved key.

## Next steps

- microsandbox: [Add and manage nodes](./nodes.md).
- Workspaces on NFS: [Set up workspace storage](./workspace-storage.md).
- Models and keys: continue with [Install Core and Web](./install.md#set-a-default-model).
