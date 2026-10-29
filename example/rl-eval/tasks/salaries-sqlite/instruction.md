`/workspace/company.db` is a SQLite database with the tables `departments(id, name)`, `employees(id, name, department_id, hired)` and `salaries(employee_id, year, amount)`. An employee has one `salaries` row for each year since being hired, from 2023 onward.

For each department, compute the average `amount` of all `salaries` rows of its employees for 2025 and for 2026. Find the department whose average salary increased the most from 2025 to 2026, and the size of that increase in currency units.

Create `/workspace/outputs/` if it does not exist and write `/workspace/outputs/answer.json` containing a JSON object with the keys: `department` (the department name) and `increase` (the 2026 average minus the 2025 average, as a JSON number rounded to 2 decimals).
