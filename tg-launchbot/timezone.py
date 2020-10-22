import sqlite3
import pytz

def load_locale_string(chat: str):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'launchbot-data.db'))
	c = conn.cursor()

	try:
		c.execute("SELECT timezone_str FROM preferences WHERE chat = ?",(chat,))
	except:
		return None

	query_return = c.fetchall()
	if len(query_return) == 0:
		return None

	if query_return[0][0] is not None:
		return query_return[0][0]

	return None


# remove time zone information for a chat
def remove_time_zone_information(chat: str):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'launchbot-data.db'))
	c = conn.cursor()

	try:
		c.execute("UPDATE preferences SET timezone_str = ?, timezone = ? WHERE chat = ?", (None, None, chat))
		if debug_log:
			logging.info(f'✅ User successfully removed their time zone information!')

	except Exception as e:
		if debug_log:
			logging.exception(f'❓ User tried to remove their time zone information, but ran into exception: {e}')

	conn.commit()
	conn.close()

def update_time_zone_string(chat: str, time_zone: str):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'launchbot-data.db'))
	c = conn.cursor()

	try:
		c.execute(
			"INSERT INTO preferences (chat, notifications, timezone, timezone_str, postpone, commands) VALUES (?, ?, ?, ?, ?, ?)",
			(chat, '1,1,1,1', None, time_zone, 1, None))
	except:
		c.execute("UPDATE preferences SET timezone_str = ?, timezone = ? WHERE chat = ?", (time_zone, None, chat))

	conn.commit()
	conn.close()

	if debug_log:
		logging.info(f'🌎 User successfully set their time zone locale to {time_zone}')


def update_time_zone_value(chat: str, offset: str):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'launchbot-data.db'))
	c = conn.cursor()

	# translate offset to hours
	if 'h' in offset:
		offset = int(offset.replace('h',''))
	elif 'm' in offset:
		offset = float(int(offset.replace('m',''))/60)

	current_value = load_time_zone_status(chat, False)
	current_value = 0 if current_value is None else current_value
	new_time_zone_value = current_value + offset

	if new_time_zone_value > 14:
		new_time_zone_value = -12
	elif new_time_zone_value < -12:
		new_time_zone_value = 14

	try:
		c.execute(
			"INSERT INTO preferences (chat, notifications, timezone, timezone_str, postpone, commands) VALUES (?, ?, ?, ?, ?, ?)", 
			(chat, '1,1,1,1', new_time_zone_value, None, 1, None)
		)
	except:
		c.execute("UPDATE preferences SET timezone = ?, timezone_str = ? WHERE chat = ?", (new_time_zone_value, None, chat))

	conn.commit()
	conn.close()


def load_time_zone_status(chat: str, readable: bool):
	conn = sqlite3.connect(os.path.join('data', 'launchbot-data.db'))
	c = conn.cursor()

	try:
		c.execute("SELECT timezone, timezone_str FROM preferences WHERE chat = ?",(chat,))
	except:
		c.execute("CREATE TABLE preferences (chat TEXT, notifications TEXT, timezone TEXT, timezone_str TEXT, postpone INTEGER, commands TEXT, PRIMARY KEY (chat))")
		conn.commit()
		c.execute("SELECT timezone, timezone_str FROM preferences WHERE chat = ?",(chat,))

	query_return = c.fetchall()
	conn.close()

	if len(query_return) != 0:
		time_zone_string_found = True if query_return[0][1] is not None else False

	if not readable:
		if len(query_return) == 0:
			return 0
		else:
			if not time_zone_string_found:
				if query_return[0][0] is None:
					return 0
				
				return float(query_return[0][0])
			else:
				timezone = pytz.timezone(query_return[0][1])
				user_local_now = datetime.datetime.now(timezone)
				utc_offset = user_local_now.utcoffset().total_seconds()/3600
				return utc_offset
	
	else:
		if len(query_return) == 0:
			return '+0'

		if not time_zone_string_found:
			if query_return[0][0] is None:
				return '+0'

			status = float(query_return[0][0])

			mins = int(60 * (abs(status) % 1))
			hours = math.floor(status)
			prefix = '+' if hours >= 0 else ''

			return f'{prefix}{hours}' if mins == 0 else f'{prefix}{hours}:{mins}'
		else:
			timezone = pytz.timezone(query_return[0][1])
			user_local_now = datetime.datetime.now(timezone)
			user_utc_offset = user_local_now.utcoffset().total_seconds()/3600

			if user_utc_offset % 1 == 0:
				user_utc_offset = int(user_utc_offset)
				utc_offset_str = f'+{user_utc_offset}' if user_utc_offset >= 0 else f'{user_utc_offset}'
			else:
				utc_offset_hours = math.floor(user_utc_offset)
				utc_offset_minutes = int((user_utc_offset % 1) * 60)
				utc_offset_str = f'{utc_offset_hours}:{utc_offset_minutes}'
				utc_offset_str = f'+{utc_offset_str}' if user_utc_offset >= 0 else f'{utc_offset_str}'

			return utc_offset_str
