`/workspace/sales.csv` lists orders with the columns `order_id,date,region,product,quantity,unit_price`. Dates are ISO `YYYY-MM-DD`. A negative `quantity` is a refund. Some product names are quoted because they contain commas.

Revenue of a row is `quantity × unit_price`, so refunds reduce revenue. Considering only orders dated from 2026-07-01 to 2026-09-30 inclusive, find the region with the highest total revenue.

Create `/workspace/outputs/` if it does not exist and write `/workspace/outputs/answer.json` containing a JSON object with the keys: `region` (the region name as written in the file) and `revenue` (that region's total revenue as a JSON number rounded to 2 decimals).
