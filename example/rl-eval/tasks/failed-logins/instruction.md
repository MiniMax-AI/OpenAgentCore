`/workspace/auth.log` is an SSH server log in syslog format. Count the lines that report `Failed password` attempts, including those for invalid users, per source IP address (the address after `from`). Other lines, such as failed public-key attempts, invalid-user notices and closed connections, do not count.

Find the source IP address with the most failed password attempts.

Create `/workspace/outputs/` if it does not exist and write `/workspace/outputs/answer.json` containing a JSON object with the keys: `ip` (the address as a string) and `failures` (its number of failed password attempts as a JSON integer).
