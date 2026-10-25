---
title: "Performance"
---

These measurements cover the sandbox lifecycle on microsandbox: starting a sandbox, preparing its environment, suspending it when idle and resuming it. E2B's create, pause and resume are measured with its SDK for comparison, and [E2B](#e2b) lists how the two sides differ. [`scripts/benchmark.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/scripts/benchmark.py) produced every number on this page from the [records](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance) published beside it.

## Results

| Measurement | Median | Range | n |
| --- | --- | --- | --- |
| Sandbox create | 1.22 s | 1.18–1.37 s | 30 |
| Sandbox created → initialization start | 0.98 s | 0.75–1.48 s | 20 |
| Session created → environment ready, preparation cache miss | 7.35 s | 5.90–16.20 s | 10 |
| npm install, preparation cache miss | 4.67 s | 3.09–13.54 s | 10 |
| Session created → environment ready, preparation cache hit | 2.50 s | 2.30–2.70 s | 10 |
| Session created → environment ready, free slot | 2.50 s | 2.10–2.60 s | 3 |
| Session created → environment ready, full node | 5.90 s | 5.50–15.40 s | 7 |
| Suspend | 3.33 s | 2.88–4.53 s | 10 |
| Resume → sandbox running | 1.75 s | 1.16–4.70 s | 10 |
| Turn first text, resumed Session | 3.51 s | 2.11–6.91 s | 10 |
| Turn first text, running Session | 1.26 s | 1.11–2.31 s | 10 |
| E2B create | 0.60 s | 0.54–1.51 s | 10 |
| E2B pause | 1.04 s | 0.69–1.41 s | 10 |
| E2B resume → first command | 0.87 s | 0.58–31.10 s | 10 |
| E2B API round trip | 0.20 s | 0.19–0.21 s | 10 |

A second run dropped the node's page cache after every Session had suspended, so each resume read its checkpoint from disk:

| Measurement | Median | Range | n |
| --- | --- | --- | --- |
| Resume → sandbox running | 1.99 s | 1.69–5.26 s | 10 |
| Turn first text, resumed Session | 3.11 s | 2.71–8.41 s | 10 |
| Turn first text, running Session | 1.26 s | 0.71–2.81 s | 10 |

## What each measurement covers

### Sandbox start

- **Sandbox create**: Core's `create` step, from the Provider call to a booted sandbox.
- **Sandbox created → initialization start**: from the end of `create` until Core starts preparing the environment. It covers the Runtime starting, connecting to Core and Core claiming the initialization. Core logs that start only for an Environment with files or setup work, so this row covers the cold-start Sessions below.

### Environment ready

**Session created → environment ready** runs from `agent.session.created` to `agent.session.environment.ready` as an application on the Core host receives them on its stream, so it includes queueing, sandbox start and preparation. Core sends stream events in 100 ms steps.

Each cold-start sample uses a new Template with one npm package (`prettier@3.3.3`), one file and one setup command. Its first Session misses the [preparation cache](../contracts/agents-api/environments.md#preparation-reuse) and installs everything; **npm install** is that installation's `npm` step as the Runtime timed it, and it depends on the package registry. Its second Session restores the prepared result.

**Free slot** and **full node** cover Sessions without a Template, started one after another. The first ones found a free slot. Once the node ran its maximum number of sandboxes, each waited for Core to [suspend](./sandbox-provider.md#suspension) an idle sandbox, which Core does once that sandbox has been inactive for 15 s.

### Suspend and resume

- **Suspend**: Core's `suspend` step. The node captures the sandbox's memory and disk to a checkpoint and stops it. A Session suspends after the idle timeout, or earlier when a new Session needs its slot.
- **Resume → sandbox running**: from the start of Core's `resume` step until the sandbox is running again. It covers restoring the checkpoint, waking the Runtime, confirming that the Environment resumed and deleting the checkpoint. The first run leaves the node's page cache as it is.
- **Turn first text**: from sending input to the first output text delta, measured by the application like Environment ready, once for the Turn that resumes a suspended Session and once for the next Turn on the same running Session. The difference is the wait a resume adds; the rest is the model.

### E2B

The E2B rows are durations of SDK calls on the client: `Sandbox.create`, `pause`, and `connect` plus one command for a resume. Each call includes at least one API round trip from the client, listed as **E2B API round trip** on a kept-alive connection. Sandboxes on both sides have 2 CPUs and 4 GiB of memory and resume after the same idle wait. They differ in:

- **Clock**: the E2B rows are timed on the client. The sandbox rows for OpenAgentCore come from Core's log, and Core reaches its node over a 0.2 ms network.
- **Workload**: before it pauses, each E2B sandbox holds 650 MiB in a process and has written 100 MiB to disk. Each OpenAgentCore sandbox has run one Codex Turn.
- **End of a resume**: E2B's ends when the first command returns. OpenAgentCore's ends when the Runtime has reconnected and the Environment has resumed.

## Setup

- **Revision**: Core at `c5d4205d`; the sandbox node and Runtime at `170a4983`, whose later changes do not touch these steps. Measured on 2026-10-25.
- **Hosts**: Core and one microsandbox node on two Intel Xeon 6982P-C cloud VMs with Ubuntu 22.04 and Linux 5.15, 0.2 ms apart. Core has 8 vCPUs and 62 GiB; the node has 4 vCPUs and 15 GiB and runs at most 3 sandboxes and retains at most 14.
- **Sandboxes**: 2 CPUs, 4 GiB of memory and an 8 GiB root disk each, with the workspace on NFSv4.2. Idle sandboxes suspend after 300 s.
- **Sessions**: Codex with MiniMax-M2.7 at `https://api.minimaxi.com/v1`, driven from the Core host with `openai` 3.13.0, 10 samples of each phase.
- **E2B**: a template built from the same Runtime image with 2 vCPUs and 4 GiB, driven with `e2b` 2.51.0 from a client 0.20 s from the API, 10 sandboxes.

## Reproduce

Run on an installation with no other activity: the report reads every lifecycle step Core logs during the run.

1. Install OpenAgentCore, add a microsandbox node with workspace storage, set a default model and issue a Project API key, as in [Install Core and Web](./getting-started/install.md).
2. On the Core host, with Python 3.10 or later and `pip install openai==3.13.0`, set `OPENAI_BASE_URL` and `OPENAI_API_KEY` and run the Sessions. It takes about 15 minutes, 7 of them waiting for the idle timeout:

   ```sh
   python3 scripts/benchmark.py sessions results --model MiniMax-M2.7 --harness codex
   ```

3. Save the Core log and build the table. The report keeps the log lines it uses in `results/core.jsonl`:

   ```sh
   docker compose -f ~/.oac/core/compose.yaml logs --no-log-prefix core > core.log
   python3 scripts/benchmark.py report results core.log
   ```

4. To resume from disk, repeat step 2 into `results/cold`. About 6 minutes after it prints `waiting 420 s for the idle timeout`, when every Session has suspended, drop the page cache on the node with `sync; echo 3 | sudo tee /proc/sys/vm/drop_caches`. Then repeat step 3 for `results/cold`.
5. For E2B, `pip install e2b==2.51.0`, set `E2B_API_KEY` and give a template built from the Runtime image with 4 GiB of memory, then run `report` again:

   ```sh
   python3 scripts/benchmark.py e2b results --template <template>
   python3 scripts/benchmark.py report results
   ```

## Records

The [records](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance) are the inputs of the tables above; `python3 scripts/benchmark.py report docs/performance` prints the first again, and `report docs/performance/cold` the second.

| File | Contents |
| --- | --- |
| `sessions.jsonl` | One line per Turn: `phase` (`cold`, `start`, `resume` or `warm`), `sample`, `cache` for cold starts, the Session ID, `created` (Core's creation time in Unix seconds) for starts, and the seconds after the request at which each event type first arrived |
| `core.jsonl` | The Core log lines of the run that the report reads: lifecycle steps with their durations, phase changes, preparation cache results and initialization steps, with allocation IDs shortened to 8 characters |
| `window.json` | The start and end of the run on Core's clock, in Unix seconds |
| `e2b.jsonl` | One line per E2B sandbox with its create, pause and resume durations, and one per API round trip |
| `cold/` | `sessions.jsonl`, `core.jsonl` and `window.json` of the run that read checkpoints from disk |
