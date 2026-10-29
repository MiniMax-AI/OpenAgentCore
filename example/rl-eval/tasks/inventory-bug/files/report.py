from inventory import load_stock, total_value

stock = load_stock("stock.json")
print(f"Items in stock: {sum(1 for i in stock if i['qty'] > 0)}")
print(f"Total stock value: {total_value(stock):.2f}")
