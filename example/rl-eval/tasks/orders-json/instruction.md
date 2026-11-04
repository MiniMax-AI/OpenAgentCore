`/workspace/orders.json` holds `customers`, each with an `id`, a `name` and a list of `orders`. Each order has a list of `lines` with `sku`, `qty` and `unit_price`.

Answer two questions about SKU `B-104`:

1. How many distinct customers have at least 3 orders that include a line for `B-104`? An order counts once however many `B-104` lines it has.
2. Which customer spent the most on `B-104`, where spend is the sum of `qty × unit_price` over all of that customer's `B-104` lines?

Create `/workspace/outputs/` if it does not exist and write `/workspace/outputs/answer.json` containing a JSON object with the keys: `customers` (the count as a JSON integer) and `top_customer` (the customer `id` as a string).
