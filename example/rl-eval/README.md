# RL and evaluation rollouts

[简体中文](README.zh.md)

A single Python script that uses OpenAgentCore's public `/v1` Agents API as a rollout engine. It runs batches of tasks in hosted sandboxes, with any Harness and model, and grades each result outside the sandbox. The output is a JSONL file with one rollout per line: reward, answer and the full trajectory. It shows that Core serves RL training and evaluation as well as chat products.

## How a rollout works

A rollout is one Session for one (task, Harness, model, sample). The script:

1. Reads the task's `files/` and sends them as inline `files` of an `openai_hosted` Environment under `/workspace/`, with network `access` set to `disabled`, so the sandbox reaches only Core and the model provider.
2. Creates the Session with the task's `instruction.md` as `input`. A new hosted Session reads `idle` while Core prepares the sandbox, and the input starts the first Turn when it is ready.
3. Polls the Turn list until the Turn is `completed`, `failed` or `cancelled`. When the timeout passes, it cancels the Turn and records `timeout`.
4. Reads the answer from the Artifact `/workspace/outputs/answer.json`, which Core captures when the Turn completes.
5. Grades the answer, reads the Items and Turn usage, and deletes the Session.

Grading runs in the script. `expected.json` never enters the sandbox, so the agent cannot read the expected answer. The grade is binary: the answer must be a JSON object with every expected key and a matching value. Numbers match within a relative tolerance of 1e-6, strings after trimming, and extra keys are ignored.

The [Agents API guide](../../docs/api/public-agent-api.md) describes each call: [Create a Session](../../docs/api/public-agent-api.md#create-a-session), [Cancel](../../docs/api/public-agent-api.md#cancel), [Turns and Items](../../docs/api/public-agent-api.md#turns-and-items), [Artifacts](../../docs/api/public-agent-api.md#artifacts) and [Core extensions](../../docs/api/public-agent-api.md#core-extensions). The [Environment contract](../../contracts/agents-api/environments.md) owns the initial file limits and the restricted network.

## Run

Use Python 3.9+. Configure [Core](../../docs/getting-started/install.md), a model, and a Project API key as in the [quickstart](../../docs/getting-started/quickstart.md):

```sh
cd example/rl-eval
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export OPENAI_BASE_URL=https://core.example/v1
read -rs OPENAI_API_KEY && export OPENAI_API_KEY
python3 rl_eval.py --harness codex --harness claude_sdk --samples 4
```

Without `--harness` and `--model`, rollouts use the deployment default Harness and the installation default model, labelled `default`.

| Option | Meaning |
| --- | --- |
| `--harness`, `--model` | Repeatable. Every combination runs |
| `--samples K` | Rollouts per task, Harness and model. Default 1 |
| `--concurrency N` | Parallel rollouts. Default 4 |
| `--tasks DIR`, `--task ID` | Task directory (default `tasks/`) and a repeatable filter on task ids |
| `--timeout SECONDS` | Per rollout, including sandbox preparation. Default 900 |
| `--out PATH` | JSONL file. Default `runs/<UTC timestamp>.jsonl` |
| `--model-base-url`, `--model-protocol` | Your own model endpoint and its protocol (`anthropic`, `responses` or `chat_completions`). The key comes only from `MODEL_API_KEY`. Requires `--model` |

The run ends with one Markdown table per Harness and model: rollouts, mean reward, pass@k (the share of tasks with at least one reward-1 sample), errors and timeouts, and mean duration. The exit code is 0 even when every reward is 0.

## Output

Each finished rollout appends one JSON line to the output file:

| Field | Meaning |
| --- | --- |
| `rollout_id`, `task`, `harness`, `model`, `sample` | Identity of the rollout. `harness` and `model` read `default` when not set |
| `session_id` | The Core Session, deleted after the rollout |
| `status` | `completed`, `failed`, `cancelled`, `timeout` or `error` |
| `reward` | `1.0` or `0.0` |
| `answer` | The parsed `answer.json`, or `null` |
| `duration_s`, `usage` | Wall time and the last Turn's token usage. Usage is `null` for Harnesses that report none, see [Usage](../../contracts/agents-api/sessions-events.md#usage) |
| `items` | The trajectory: every Session Item in order, as returned by the API |
| `error` | The exception message when `status` is `error` |

## Tasks

| Task | The agent must |
| --- | --- |
| `sales-by-region` | Aggregate a CSV with refunds and quoted fields by region and quarter |
| `failed-logins` | Count failed SSH password attempts per address in a syslog file |
| `inventory-bug` | Fix a pricing bug against `PRICING.md`, run the report and read its total |
| `orders-json` | Query nested JSON for customers and spend on one SKU |
| `salaries-sqlite` | Compare yearly average salaries per department in a SQLite database |

They need only bash, git, python3, node and ripgrep from the managed sandbox image, and no network.

To add a task, create `tasks/<id>/` with:

- `instruction.md`, which states the question precisely and ends with the output contract: write `/workspace/outputs/answer.json` with the given keys.
- `expected.json`, a JSON object with the expected values. It stays outside the sandbox.
- `files/`, copied recursively to `/workspace/` with relative paths kept. Core accepts at most 50 inline files, 5 MiB each and 10 MiB in total.

## Connect training

Each Harness calls the model natively, so a training-side inference server sees every request and records token ids and logprobs. Core records none of them. Point rollouts at that server:

```sh
export MODEL_API_KEY=<key-of-the-inference-server>
python3 rl_eval.py --model my-policy --model-protocol responses \
  --model-base-url 'https://infer.example/rollouts/{rollout_id}/v1' --samples 8
```

`{rollout_id}` is replaced per rollout with the `rollout_id` in the output, so the server can attribute each request to its rollout. The K samples of a task form a GRPO group, and `reward` is the group's outcome reward. With network `disabled`, the sandbox still reaches the model provider on its `base_url` port at public addresses. A private inference host needs the deployment's [network cap](../../contracts/agents-api/environments.md#restricted-network) to cover its name.

## Scope

The script covers tasks whose answer is graded outside the sandbox. Tasks graded inside their own environment, such as SWE-bench and Terminal-Bench, need a container you own with a `self_hosted` Environment, and are not covered.

## Results

Recorded on 2026-10-29 on a test deployment built from `main`, with microsandbox nodes and MiniMax-M2.7 as the default model of both Harnesses:

```sh
python3 rl_eval.py --harness codex --harness claude_sdk --samples 2 --concurrency 2
```

| harness | model | rollouts | mean reward | pass@k | errors/timeouts | mean duration (s) |
| --- | --- | --- | --- | --- | --- | --- |
| claude_sdk | default | 10 | 1.000 | 1.000 | 0 | 25.7 |
| codex | default | 10 | 1.000 | 1.000 | 0 | 17.6 |

Both Harnesses solve every bundled task, so the tasks show the loop, not a training signal. A GRPO group teaches only when its samples disagree: train on tasks your policy solves some of the time.

## Validation

```sh
python3 rl_eval_test.py
```

The tests need no network and no `openai` package. They cover grading, the pass@k summary and loading every bundled task.
