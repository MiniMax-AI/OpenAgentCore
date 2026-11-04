"""Stock valuation helpers."""
import json

CLEARANCE_FACTOR = 0.6


def load_stock(path):
    with open(path) as f:
        return json.load(f)


def line_value(item):
    """Return the value of one stock line in dollars."""
    unit = item["unit_price_cents"] / 100
    if item["clearance"]:
        unit = round(unit * CLEARANCE_FACTOR, 2)
    return item["qty"] * unit


def total_value(stock):
    return round(sum(line_value(i) for i in stock if i["qty"] > 0), 2)
