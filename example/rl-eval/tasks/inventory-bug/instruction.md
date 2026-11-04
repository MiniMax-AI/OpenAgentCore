`/workspace/report.py` prints the total stock value computed by `/workspace/inventory.py` from `/workspace/stock.json`. The valuation rules are in `/workspace/PRICING.md`, and the total it prints currently violates them.

Fix the bug in `/workspace/inventory.py` so the code follows the rules, without changing `stock.json` or `PRICING.md`. Then run `python3 report.py` from `/workspace` and read the total it prints.

Create `/workspace/outputs/` if it does not exist and write `/workspace/outputs/answer.json` containing a JSON object with the keys: `total` (the printed total stock value as a JSON number).
