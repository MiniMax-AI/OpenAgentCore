---
title: "Performance"
---

These measurements cover the sandbox lifecycle on microsandbox: starting a sandbox, preparing its environment, suspending it when idle and resuming it, with E2B measured the same way for comparison. [`scripts/benchmark.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/scripts/benchmark.py) produced every number on this page from the [records](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance) published beside it.

## Results

| Measurement | Median | Range | n |
| --- | --- | --- | --- |
| Sandbox create | 1.20 s | 1.16–1.22 s | 30 |
| Sandbox created → initialization start | 0.80 s | 0.76–1.07 s | 20 |
| Session created → environment ready, preparation cache miss | 8.15 s | 5.90–18.90 s | 10 |
| Session created → environment ready, preparation cache hit | 2.30 s | 2.30–2.60 s | 10 |
| Suspend | 2.92 s | 2.87–2.97 s | 10 |
| Resume → sandbox running | 1.24 s | 1.13–1.29 s | 10 |
| Turn first text, resumed Session | 3.06 s | 2.11–7.81 s | 10 |
| Turn first text, running Session | 1.51 s | 1.01–5.41 s | 10 |
| E2B create | 0.59 s | 0.28–1.68 s | 10 |
| E2B pause | 0.60 s | 0.49–0.87 s | 10 |
| E2B resume → first command | 0.75 s | 0.52–1.99 s | 10 |
| E2B API round trip | 0.20 s | 0.19–0.23 s | 5 |

## What each measurement covers

### Sandbox start

- **Sandbox create**: Core's `create` step, from the Provider call to a booted sandbox.
- **Sandbox created → initialization start**: from the end of `create` until Core starts preparing the environment. It covers the Runtime starting, connecting to Core and Core claiming the initialization.

### Environment ready

**Session created → environment ready** runs from `agent.session.created` to `agent.session.environment.ready` on the application's stream, so it includes queueing, sandbox start and preparation. Each sample uses a new Template with one npm package (`prettier@3.3.3`), one file and one setup command. Its first Session misses the [preparation cache](../contracts/agents-api/environments.md#preparation-reuse) and installs everything; its second Session restores the prepared result. Restoring and saving the prepared result take under 0.4 s, so installing the package takes most of a miss, and a miss depends on the package registry.

### Suspend and resume

- **Suspend**: Core's `suspend` step. The node captures the sandbox's memory and disk to a checkpoint and stops it. A Session suspends after the idle timeout, or earlier when a new Session needs its slot.
- **Resume → sandbox running**: from the start of Core's `resume` step until the sandbox is running again. It covers restoring the checkpoint, waking the Runtime and confirming that the Environment resumed.
- **Turn first text**: from sending input to the first output text delta, once for the Turn that resumes a suspended Session and once for the next Turn on the same running Session. The difference is the wait a resume adds; the rest is the model.

### E2B

The E2B rows are durations of SDK calls on the client: `Sandbox.create`, `pause`, and `connect` plus one command for a resume. Before it pauses, each sandbox holds 650 MiB in a process and has written 100 MiB to disk, close to a sandbox after one Harness Turn; each resumes after the same idle wait as the Sessions. Each call includes at least one API round trip from the client, listed as **E2B API round trip** on a kept-alive connection. Core measures the OpenAgentCore rows itself, and reaches its node over the local network.

## Setup

- **Revision**: Core at `c5d4205d`; the sandbox node and Runtime at `170a4983`, whose later changes do not touch these steps. Measured on 2026-10-25.
- **Hosts**: Core and one microsandbox node on two Intel Xeon 6982P-C cloud VMs with Ubuntu 22.04 and Linux 5.15, 0.2 ms apart. Core has 8 vCPUs and 62 GiB; the node has 4 vCPUs and 15 GiB and runs at most 3 sandboxes and retains at most 14.
- **Sandboxes**: 2 CPUs, 4 GiB of memory and an 8 GiB root disk each, with the workspace on NFSv4.2. Idle sandboxes suspend after 300 s.
- **Sessions**: Codex with MiniMax-M2.7 at `https://api.minimaxi.com/v1`, driven from the Core host with `openai` 3.13.0, 10 samples of each phase.
- **E2B**: a template built from the same Runtime image with 2 vCPUs and 2 GiB, driven with `e2b` 2.51.0 from a client 0.20 s from the API, 10 sandboxes.

## Reproduce

Run on an installation with no other activity: the report reads every lifecycle step Core logs during the run.

1. Install OpenAgentCore, add a microsandbox node with workspace storage, set a default model and issue a Project API key, as in [Install Core and Web](./getting-started/install.md).
2. On any host that reaches the API, with Python 3.10 or later and `pip install openai==3.13.0`, set `OPENAI_BASE_URL` and `OPENAI_API_KEY` and run the Sessions. It takes about 15 minutes, half of it waiting for the idle timeout:

   ```sh
   python3 scripts/benchmark.py sessions results --model MiniMax-M2.7 --harness codex
   ```

3. On the Core host, save the Core log and build the table. The report keeps the log lines it uses in `results/core.jsonl`:

   ```sh
   docker compose -f ~/.oac/core/compose.yaml logs --no-log-prefix core > core.log
   python3 scripts/benchmark.py report results core.log
   ```

4. For E2B, `pip install e2b`, set `E2B_API_KEY` and give a template with at least 2 GiB of memory, then run `report` again:

   ```sh
   python3 scripts/benchmark.py e2b results --template <template>
   python3 scripts/benchmark.py report results
   ```

## Records

The [records](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/docs/performance) are the inputs of the table above; `python3 scripts/benchmark.py report docs/performance` prints it again.

| File | Contents |
| --- | --- |
| `sessions.jsonl` | One line per Turn: `phase` (`cold`, `start`, `resume` or `warm`), `sample`, `cache` for cold starts, the Session ID, and the seconds after the request at which each event type first arrived |
| `core.jsonl` | The Core log lines of the run that the report reads: lifecycle steps with their durations, phase changes and preparation cache results, with allocation IDs shortened to 8 characters |
| `window.json` | The start and end of the run on Core's clock, in Unix seconds |
| `e2b.jsonl` | One line per E2B sandbox with its create, pause and resume durations, and one per API round trip |
