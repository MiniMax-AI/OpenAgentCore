# Stock valuation rules

1. The value of a stock line is `qty x unit price`.
2. Clearance lines are valued at 60% of the unit price.
3. Round each line's value to whole cents, once, after all multiplication. Never round the unit price.
4. Lines with zero quantity contribute nothing.
5. The total stock value is the sum of the rounded line values, reported with two decimals.
