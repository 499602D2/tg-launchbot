# Used to clean old logs

rem_lines = 0
with open('log.log', 'r') as file:
	with open('new_log.log', 'w') as new_file:
		for line in file:
			if 'Received text, not a command:' not in line:
				try:
					if line[2] == '/' and line[5] == '/':
						new_file.write(line)
				except:
					rem_lines += 1
			else:
				rem_lines += 1

print(f'Done! Removed {rem_lines} lines of logs.')
