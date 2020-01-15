import os, sys, time, ssl, datetime, logging, math, requests, inspect
import telepot, sqlite3, cursor, difflib, schedule
import ujson as json

from telepot.loop import MessageLoop
from telepot.namedtuple import InlineKeyboardMarkup, InlineKeyboardButton
from uptime import uptime

'''
Roadmap
0.2 (December):
	- implement /next using DB calls
	- implement support for SpaceX core information

0.3 (January):
	- improve notification handling with the hold flag -> moving NETs and info text regarding them
	- change launch database index from tminus to net
	- add probability of launch
	- handle notification send checks with schedule, instead of polling every 20-30 seconds (i.e. update schedule every time db is updated)

---------------------------------
To-do
- /feedback with inline keyboard
- add button to mute a single launch
- add launch location; could be in db as pad/location
- add support for moving NETs; if new NET doesn't match old NET, purge previous notifications etc.
- add support for search in /next
- add support for country codes (dict map)
- fix the issue with all-flag causing some notifications to be unable to be disabled (i.e. those not in the supported list)
- add probability of launch to info message (i.e. add a new column to the launch database)
- notify users of a launch holding? 
- reset notification flag if NET moves forward, add a piece of info text saying that the launch was previously held
'''

# main loop-function for messages with flavor=chat
def handle(msg):
	try:
		content_type, chat_type, chat = telepot.glance(msg, flavor="chat")
	except KeyError:
		if debug_log:
			logging.info(f'KeyError in handle(): {msg}')
		return

	# for admin/private chat checks; also might throw an error when kicked out of a group, so handle that here as well
	try:
		sender = bot.getChatMember(chat, msg['from']['id'])
		chat_type = bot.getChat(chat)['type']
	
	except telepot.exception.BotWasKickedError:
		'''
		Bot kicked; remove corresponding chat IDs from notification database
		
		This exception is effectively only triggered if we're handling a message
		_after_ the bot has been kicked, e.g. after a bot restart.
		'''
		conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
		c = conn.cursor()
		
		c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
		conn.commit()
		conn.close()

		if debug_log:
			logging.info(f'⚠️ Bot removed from chat {chat} – notifications database cleaned [1]')
		return

	# group upgraded to a supergroup; migrate data
	if 'migrate_to_chat_id' in msg:
		old_ID = chat
		new_ID = msg['migrate_to_chat_id']

		if debug_log:
			logging.info(f'⚠️ Group {old_ID} migrated to {new_ID} - starting database migration...')

		# Establish connection
		conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
		c = conn.cursor()

		# replace old IDs with new IDs
		c.execute("UPDATE notify SET chat = ? WHERE chat = ?", (new_ID, old_ID))
		conn.commit()
		conn.close()

		if debug_log:
			logging.info('✅ Chat data migration complete!')

	# bot removed from chat
	if 'left_chat_member' in msg and msg['left_chat_member']['id'] == bot_ID:
		# bot kicked; remove corresponding chat IDs from notification database
		conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
		c = conn.cursor()
		
		c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
		conn.commit()
		conn.close()

		if debug_log:
			logging.info(f'⚠️ Bot removed from chat {chat} – notifications database cleaned [2]')
		return

	# detect if bot added to a new chat
	if 'new_chat_members' in msg or 'group_chat_created' in msg:
		if 'new_chat_member' in msg:
			try:
				if bot_ID in msg['new_chat_member']['id']:
					pass
				else:
					return
			
			except TypeError:
				if msg['new_chat_member']['id'] == bot_ID:
					pass
				else:
					return
		elif 'group_chat_created' in msg:
			if msg['group_chat_created']:
				pass
			else:
				return

		reply_msg = f'''🚀 *Hi there!* I'm *LaunchBot*, a launch information and notifications bot!

		*To get started*, you can enable all notifications: `/notify all`
		Or, just check the next flight: `/next`
	
		*Commands*
		`/notify` toggle notifications for various launch service providers.
		`/next` shows the next launch.
		`/shecdule` displays a simple schedule of upcoming flights.
		`/statistics` displays various statistics about the bot.

		*Example usage*
		`/notify all`: toggle notifications for all launches
		`/notify SpaceX`: toggle notifications for all SpaceX launches

		*LaunchBot* version {version}.
		'''
		
		bot.sendMessage(chat, inspect.cleandoc(reply_msg), parse_mode='Markdown')

		# ask if the user wants to enable all notifications
		msg_text = f'🔔 Would you like to enable all notifications? (You can disable them at any time)'
		keyboard = InlineKeyboardMarkup(inline_keyboard=[
			[InlineKeyboardButton(text='✅ Yes please!', callback_data=f'{chat}/notify/All')],
			[InlineKeyboardButton(text='🛑 No, thank you.', callback_data=f'{chat}/notify/None')]])

		bot.sendMessage(chat, msg_text, reply_markup=keyboard)

		if debug_log:
			logging.info(f'🌟 Bot added to a new chat! chat_id={chat}. Sent an inline_keyboard.')

		return
	
	try:
		command_split = msg['text'].strip().split(" ")
	except Exception as e:
		if debug_log:
			logging.info(f'🛑 Error generating command split, returning: {e}')
		return

	# regular text, pass
	if content_type == 'text' and command_split[0][0] != '/':
		if debug_log:
			logging.info(f'❔ Received text, not a command: chat={chat}, text: "{msg["text"]}". Returning.')
		return
	
	# sees a valid command
	elif content_type == 'text':
		if command_split[0].lower() in valid_commands or command_split[0] in valid_commands_alt:
			# command we saw
			command = command_split[0].lower()

			# check timers
			if not timerHandle(command, chat):
				if debug_log:
					logging.info(f'✋ Spam prevented from chat {chat}. Command: {command}, returning.')
				return

			# check if sender is an admin/creator, and/or if we're in a public chat
			if chat_type != 'private' and sender['status'] != 'creator' and sender['status'] != 'administrator':
				if debug_log:
					logging.info(f'✋ {command} called by a non-admin in {chat}, returning.')
				return
			else:
				bot.sendChatAction(chat, action='typing')

			# store statistics here, so our stats database can't be spammed either
			updateStats({'commands':1})

			if debug_log:
				if msg['from']['id'] != 421341996 and command != '/start':
					try:
						logging.info(f'🕹 {command} called by {chat}. Args: {command_split[1:]}')
					except:
						logging.info(f'🕹 {command} called by {chat}. Args: []')

			# /start or /help (1, 2)
			if command in [valid_commands[0], valid_commands_alt[0], valid_commands[1], valid_commands_alt[1]]:
				# construct info message
				reply_msg = f'''🚀 *LaunchBot version {version}*

				Hi there, I'm *LaunchBot*! To get started, you can enable some notifications, or check the next flight with /next!

				*List of commands*
				`/notify` toggle notifications for various launch service providers
				`/next` shows the next launch
				`/schedule` displays a simple flight schedule
				`/statistics` displays various statistics about the bot

				*Example usage*
				`/notify all`: toggle notifications for all flights
				`/notify SpaceX`: toggle notifications for all SpaceX launches.
				'/next': show the next launch
				`/schedule`: display the flight schedule

				*Changelog for version 0.2.3*
				- implemented /next & /schedule
				- added Falcon 9 & Heavy information
				- fixed various issues with setting notifications
				- fix issues with notification formatting
				- various fixes and improvements
				'''
				
				bot.sendMessage(chat, inspect.cleandoc(reply_msg), parse_mode='Markdown')

				# /start, send also the inline keyboard
				if command in [valid_commands[0], valid_commands_alt[0]]:
					msg_text = f'🔔 Would you like to enable all notifications? (You can disable them at any time)'
					keyboard = InlineKeyboardMarkup(inline_keyboard=[
						[InlineKeyboardButton(text='✅ Yes please!', callback_data=f'{chat}/notify/All')],
						[InlineKeyboardButton(text='🛑 No, thank you.', callback_data=f'{chat}/notify/None')]])

					bot.sendMessage(chat, msg_text, reply_markup=keyboard)

					if debug_log:
						logging.info(f'🌟 Bot added to a new chat! chat_id={chat}. Sent user an inline_keyboard.')

			# /next (3)
			elif command in [valid_commands[2], valid_commands_alt[2]]:
				nextFlight(msg)

			# /notify (4)
			elif command in [valid_commands[3], valid_commands_alt[3]]:
				notify(msg)

			# /statistics (5)
			elif command in [valid_commands[4], valid_commands_alt[4]]:
				statistics(msg)

			# /schedule (6)
			elif command in [valid_commands[5], valid_commands_alt[5]]:
				flightSchedule(msg)

			return

		else:
			if debug_log:
				logging.info(f'❔ Unknown command received in chat {chat}: {command}. Returning.')
			return


def callbackHandler(msg):
	try:
		query_id, from_id, query_data = telepot.glance(msg, flavor='callback_query')
		if debug_log:
			logging.info(f'🔊 Callback query: query_id:{query_id}, from_id:{from_id}, data:{query_data}')
	
	except Exception as caught_exception:
		if debug_log:
			logging.info(f'⚠️ Exception in callbackHandler: {caught_exception}')

		return

	# verify input, assume (chat/command/data) | (https://core.telegram.org/bots/api#callbackquery)
	input_data = query_data.split('/')
	chat = input_data[0]

	# check that the query is from an admin or an owner
	sender = bot.getChatMember(chat, from_id)
	chat_type = bot.getChat(chat)['type']
	if chat_type != 'private' and sender['status'] != 'creator' and sender['status'] != 'administrator':
		bot.answerCallbackQuery(query_id, text="🚨 This button is only callable by administrators!")
		if debug_log:
			logging.info(f'✋ Callback query called by a non-admin in {chat}, returning.')
		
		return

	# callbacks only supported for notify at the moment; verify it's a notify command
	if input_data[1] != 'notify':
		if debug_log:
			logging.info(f'⚠️ Incorrect input data in callbackHandler! input_data={input_data}')

		return

	updateStats({'commands':1})

	if input_data[2] in ['None', 'All']:
		if input_data[2] == 'None':
			all_flag = 0
			bot.answerCallbackQuery(query_id, text='🔇 Got it, you can enable notifications manually at any time with /notify!')	
	
		elif input_data[2] == 'All':
			all_flag = 1
			bot.answerCallbackQuery(query_id, text="✅ Got it! You'll be notified of all flights now!")

		# check if notification database exists
		launch_dir = 'data/launch'
		if not os.path.isfile(os.path.join(launch_dir,'notifications.db')):
			createNotifyDatabase()
		
		# connect to database
		notify_conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
		notify_cursor = notify_conn.cursor()
		
		try:
			notify_cursor.execute("INSERT INTO notify (chat, keyword, lastnotified, enabled) VALUES (?, ?, 0, ?)", (chat, 'All', all_flag))
		except:
			notify_cursor.execute("UPDATE notify SET enabled = ? WHERE chat = ? AND keyword = ?", (all_flag, chat, 'All'))

		notify_conn.commit()
		notify_conn.close()

		if debug_log:
			logging.info(f'⌨️ {chat} toggled all notifications to state={all_flag} with inline keyboard')
	
	return


# restrict command send frequency to avoid spam
def timerHandle(command, chat):
	# remove the '/' command prefix
	command = command.strip('/')
	chat = str(chat)

	if '@' in command:
		command = command.split('@')[0]

	# get current time
	now_called = datetime.datetime.today()

	# check if settings.json exists; if not, generate it
	if not os.path.isfile('data/command-timers.json'):
		with open('data/command-timers.json', 'w') as json_data:
			setting_map = {} # empty .json file
			
			# generate fields
			setting_map['commandTimers'] = {}
			for command in valid_commands:
				command = command.replace('/','')
				setting_map['commandTimers'][command] = '0.35'
			
			json.dump(setting_map, json_data, indent=4)

	# load settings
	with open('data/command-timers.json', 'r') as json_data:
		setting_map = json.load(json_data)

	# load timer for command
	try:
		timer = float(setting_map['commandTimers'][command])
	
	except KeyError:
		with open('data/command-timers.json', 'w') as json_data:
			setting_map['commandTimers'][command] = '0.35'
			json.dump(setting_map, json_data, indent=4)

		timer = float(setting_map['commandTimers'][command])

	if timer <= -1:
		return False


	# checking if the command has been called previously
	# load time the command was previously called
	if not os.path.isfile('data/last-called.json'):
		with open('data/last-called.json', 'w') as json_data:
			lastMap = {}
			if chat not in lastMap:
				if debug_log:
					logging.info(f'🌟 New chat detected! chat_id={chat}')
				lastMap[chat] = {}

			# never called, set to 0
			if command not in lastMap[chat]:
				lastMap[chat][command] = '0'

			json.dump(lastMap, json_data, indent=4)

	with open('data/last-called.json') as json_data:
		lastMap = json.load(json_data)

	try:
		last_called = lastMap[chat][command]
	except KeyError:
		if chat not in lastMap:
			lastMap[chat] = {}
		
		if command not in lastMap[chat]:
			lastMap[chat][command] = '0'	
		
		last_called = lastMap[chat][command]

	if last_called == '0': # never called; store now
		lastMap[chat][command] = str(now_called) # stringify datetime object, store
		with open('data/last-called.json', 'w') as json_data:
			json.dump(lastMap, json_data, indent=4)
	
	else:
		last_called = datetime.datetime.strptime(last_called, "%Y-%m-%d %H:%M:%S.%f") # unstring datetime object
		time_since = abs(now_called - last_called)

		if time_since.seconds > timer:
			lastMap[chat][command] = str(now_called) # stringify datetime object, store
			with open('data/last-called.json', 'w') as json_data:
				json.dump(lastMap, json_data, indent=4)
		else:
			return False

	return True


# display a very simple schedule for upcoming flights (all)
def flightSchedule(msg):
	content_type, chat_type, chat = telepot.glance(msg, flavor='chat')
	launch_dir = 'data/launch'

	# open db connection
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
	c = conn.cursor()

	# perform the select; if cmd == all, just pull the next launch
	today_unix = time.mktime(datetime.datetime.today().timetuple())
	c.execute('SELECT * FROM launches WHERE NET >= ?',(today_unix,))
	
	# sort ascending by NET, pick smallest 5
	query_return = c.fetchall()
	conn.close()

	# sort in place by NET
	query_return.sort(key=lambda tup: tup[9])

	# map months numbers to strings
	month_map = {
	1: 'January', 2: 'February', 3: 'March', 4: 'April',
	5: 'May', 6: 'June', 7: 'July', 8: 'August',
	9: 'Septemper', 10: 'October', 11: 'November', 12: 'December'
	}

	# pick 5 smallest, map into dict with dates
	sched_dict = {}
	for row, i in zip(query_return, range(len(query_return))):
		if i > 5:
			break

		launch_unix = datetime.datetime.utcfromtimestamp(row[9])
		provider = row[3] if len(row[3]) <= len('Arianespace') else row[4]
		vehicle = row[5].split('/')[0]

		# shorten monospaced text length
		provider = ' '.join("`{}`".format(word) for word in provider.split(' '))
		vehicle = ' '.join("`{}`".format(word) for word in vehicle.split(' '))

		flt_str = f'{provider} {vehicle}'
		utc_str = f'{launch_unix.year}-{launch_unix.month}-{launch_unix.day}'

		if utc_str not in sched_dict:
			sched_dict[utc_str] = [flt_str]
		else:
			sched_dict[utc_str].append(flt_str)

	schedule_msg, i = f'📅 *Flight schedule*\n', 0
	for key, val in sched_dict.items():
		if i != 0:
			schedule_msg += '\n\n'

		# create the date string; key in the form of year-month-day
		ymd_split = key.split('-')
		try:
			suffix = {1: 'st', 2: 'nd', 3: 'rd'}[str(ymd_split[2])[-1]]
		except:
			suffix = 'th'

		schedule_msg += f'*{month_map[int(ymd_split[1])]} {ymd_split[2]}{suffix}*\n'
		for mission, j in zip(val, range(len(val))):
			if j != 0:
				schedule_msg += '\n'

			schedule_msg += mission

		i += 1

	bot.sendMessage(chat, schedule_msg, parse_mode='Markdown')
	return


# handles /next by polling the launch database
def nextFlight(msg):
	content_type, chat_type, chat = telepot.glance(msg, flavor='chat')
	launch_dir = 'data/launch'

	command_split = msg['text'].strip().split(" ")
	cmd = ' '.join(command_split[1:])

	if cmd == ' ' or cmd == '':
		cmd = None

	elif cmd == 'all':
		pass

	elif len(difflib.get_close_matches('all', ['all'])) == 1:
		cmd = 'all'

	else:
		bot.sendMessage(chat, '⚠️ Not a valid query type – currently supported queries are `/next` and `/next all`.', parse_mode='Markdown')
		return

	# if command was "all", no need to perform a special select
	# if no command, we'll need to figure out what LSPs the user has set notifs for
	notify_conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	notify_cursor = notify_conn.cursor()
	notify_cursor.execute('''SELECT * FROM notify WHERE chat = ?''', (chat,))
	
	query_return = notify_cursor.fetchall()
	notify_conn.close()

	# flag for all notifications enabled
	all_flag = False

	# chat has no enabled notifications; pull from all
	if len(query_return) == 0:
		cmd, user_notif_enabled = 'all', False
		enabled, disabled = [], []

	else:
		notif_providers, user_notif_enabled = [], None
		enabled, disabled = [], []
		for row in query_return:
			# chat ID - keyword - UNIX timestamp - enabled true/false
			if row[1].lower() == 'all' and row[3] == 1:
				all_flag, user_notif_enabled = True, True
				enabled.append(row[1])

			else:
				if row[3] == 1:
					enabled.append(row[1])
				else:
					disabled.append(row[1])

		if len(enabled) == 0:
			user_notif_enabled = False

		notif_providers = enabled

	# if chat has no notifications enabled, use cmd=all
	if len(enabled) == 0:
		cmd = 'all'

	# open db connection
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
	c = conn.cursor()

	# datetimes
	today_unix = time.mktime(datetime.datetime.today().timetuple())

	# perform the select; if cmd == all, just pull the next launch
	if cmd == 'all':
		c.execute('SELECT * FROM launches WHERE NET >= ?',(today_unix,))

	# if no next command, assume the user wants to know the next launch they're interested in
	elif cmd == None:
		if all_flag:
			if len(disabled) > 0:
				query_str = f"SELECT * FROM launches WHERE NET >= {today_unix} AND lsp_name NOT IN ({','.join(['?']*len(disabled))})"
				c.execute(query_str, disabled)
			else:
				c.execute('SELECT * FROM launches WHERE NET >= ?',(today_unix,))
		else:
			query_str = f"SELECT * FROM launches WHERE NET >= {today_unix} AND lsp_name IN ({','.join(['?']*len(notif_providers))})"
			c.execute(query_str, notif_providers)

	# sort ascending by NET, pick smallest
	query_return = c.fetchall()
	if len(query_return) > 0:
		query_return.sort(key=lambda tup: tup[9])
		query_return = query_return[0]
	else:
		provider_str = '\n\t\t- '.join(notif_providers)
		msg_text = f'''⚠️ No upcoming launches with a NET-date specified found.
		Searched for the following launch service providers:

		- {provider_str}

		ℹ️ To search for all upcoming launches: `/next all`
		'''
		bot.sendMessage(chat, inspect.cleandoc(msg_text), parse_mode='Markdown')
		return

	# close connection
	conn.close()

	# pull relevant info from query return
	mission_name = query_return[0].strip()
	lsp_id = query_return[2]
	lsp_name = query_return[3]
	vehicle_name = query_return[5]
	pad_name = query_return[6]
	info = query_return[7]

	if lsp_name == 'SpaceX':
		spx_info_str = spxInfoStrGen(mission_name, 0)
		if spx_info_str != None:
			spx_str = True
		else:
			spx_str = False
	else:
		spx_str = False

	if cmd == 'all' and lsp_name in disabled:
		user_notif_enabled = False

	# check if user has notifications enabled
	if user_notif_enabled == None:
		if lsp_name in enabled:
			user_notif_enabled = True
		elif len(difflib.get_close_matches(lsp_name, enabled)) == 1:
			user_notif_enabled = True
		elif lsp_name in disabled:
			user_notif_enabled = False
		else:
			if debug_log:
				logging.info(f'⚠️ failed to set user_notif_enabled: lsp: {lsp_name}, diff: {difflib.get_close_matches(lsp_name, notif_providers)}\
					, notif_providers: {notif_providers}')
			user_notif_enabled = False

	launch_unix = datetime.datetime.utcfromtimestamp(query_return[9])
	if launch_unix.second == 0:
		if launch_unix.minute < 10:
			min_time = f'0{launch_unix.minute}'
		else:
			min_time = launch_unix.minute

		launch_time = f'{launch_unix.hour}:{min_time}'
	else:
		if launch_unix.second < 10:
			sec_time = f'0{launch_unix.second}'
		else:
			sec_time = launch_unix.second

		if launch_unix.minute < 10:
			min_time = f'0{launch_unix.minute}'
		else:
			min_time = launch_unix.minute

		launch_time = f'{launch_unix.hour}:{min_time}.{sec_time}'

	net_stamp = datetime.datetime.fromtimestamp(query_return[9])
	eta = abs(datetime.datetime.today() - net_stamp)

	if eta.days >= 365: # over 1 year
		t_y = math.floor(eta.days/365)
		t_m = math.floor(eta.months)
		
		if t_y == 1:
			eta_str = f'{t_y} year, {t_m} months'
		else:
			eta_str = f'{t_y} years, {t_m} months'

	elif eta.days < 365 and eta.days >= 31: # over 1 month
		t_m = eta.months
		t_d = eta.days

		if t_m == 1:
			eta_str = f'{t_m} month, {t_d} days'
		else:
			eta_str = f'{t_m} months, {t_d} days'

	elif eta.days >= 1 and eta.days < 31: # over a day
		t_d = eta.days
		t_h = math.floor(eta.seconds/3600)
		t_m = math.floor((eta.seconds-t_h*3600)/60)

		t_d_str = f'{t_d} day' if t_d == 1 else f'{t_d} days'
		min_suff = 'minute' if t_m == 1 else 'minutes'

		if t_h == 1:
			eta_str = f'{t_d_str}, {t_h} hour, {t_m} {min_suff}'
		else:
			eta_str = f'{t_d_str}, {t_h} hours, {t_m} {min_suff}'

	elif (eta.seconds/3600) < 24 and (eta.seconds/3600) >= 1: # under a day, more than an hour
		t_h = math.floor(eta.seconds/3600)
		t_m = math.floor((eta.seconds-t_h*3600)/60)

		min_suff = 'minute' if t_m == 1 else 'minutes'

		if t_h == 1:
			eta_str = f'{t_h} hour, {t_m} {min_suff}'
		else:
			eta_str = f'{t_h} hours, {t_m} {min_suff}'
	
	elif (eta.seconds/3600) < 1:
		t_m = math.floor(eta.seconds/60)
		t_s = math.floor(eta.seconds-t_m*60)

		s_suff = 'second' if t_s == 1 else 'seconds'

		if t_m == 1:
			eta_str = f'{t_m} minute, {t_s} {s_suff}'
		elif t_m == 0:
			if t_s <= 10:
				eta_str = f'T- {t_s}, terminal countdown'
			else:
				eta_str = f'T- {t_s} {s_suff}'
		else:
			eta_str = f'{t_m} minutes, {t_s} {s_suff}'

	LSP_IDs = {
	121: 	['SpaceX', '🇺🇸'],
	147: 	['Rocket Lab', '🇺🇸'],
	99: 	['Northrop Grumman', '🇺🇸'],
	115: 	['Arianespace', '🇪🇺'],
	124: 	['ULA', '🇺🇸'],
	98: 	['Mitsubishi Heavy Industries', '🇯🇵'],
	88: 	['CASC', '🇨🇳'],
	190: 	['Antrix Corporation', '🇮🇳'],
	122: 	['Sea Launch', '🇷🇺'],
	118: 	['ILS', '🇺🇸🇷🇺'],
	193: 	['Eurockot', '🇪🇺🇷🇺'],
	119:	['ISC Kosmotras', '🇷🇺🇺🇦🇰🇿'],
	123:	['Starsem SA', '🇪🇺🇷🇺'],
	194:	['ExPace', '🇨🇳']
	}

	if int(lsp_id) in LSP_IDs:
		lsp_name = LSP_IDs[int(lsp_id)][0]
		lsp_flag = LSP_IDs[int(lsp_id)][1]
	else:
		lsp_flag = None

	# inform the user whether they'll be notified or not
	if user_notif_enabled:
		notify_str = '🔔 You will be notified of this launch!'
	else:
		notify_str = f'🔕 You will *not* be notified of this launch.\nℹ️ *To enable* notifications: `/notify {lsp_name}`'

	if info is not None:
		info_msg = f'ℹ️ {info}'
	else:
		info_msg = None

	# do some string magic to reduce the space width of monospaced text in the telegram message
	lsp_name = ' '.join("`{}`".format(word) for word in lsp_name.split(' '))
	mission_name = ' '.join("`{}`".format(word) for word in mission_name.split(' '))
	vehicle_name = ' '.join("`{}`".format(word) for word in vehicle_name.split(' '))
	pad_name = ' '.join("`{}`".format(word) for word in pad_name.split(' '))
	eta_str = ' '.join("`{}`".format(word) for word in eta_str.split(' '))

	# create a readable time string instead of the old YYYY-MM-DD format
	month_map = {
	1: 'January', 2: 'February', 3: 'March', 4: 'April',
	5: 'May', 6: 'June', 7: 'July', 8: 'August',
	9: 'Septemper', 10: 'October', 11: 'November', 12: 'December'
	}

	try:
		suffix = {1: 'st', 2: 'nd', 3: 'rd'}[str(launch_unix.day)[-1]]
	except:
		suffix = 'th'

	date_str = f'{month_map[launch_unix.month]} {launch_unix.day}{suffix}'
	date_str = ' '.join("`{}`".format(word) for word in date_str.split(' '))

	# construct the message
	if lsp_flag != None:
		header = f'🚀 *Next launch* is by {lsp_name} {lsp_flag}\n*Mission* {mission_name}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'
	else:
		header = f'🚀 *Next launch* is by {lsp_name}\n*Mission* {mission_name}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'

	time_str = f'📅 {date_str}`,` `{launch_time} UTC`\n⏱ {eta_str}'
	
	# not a spx launch, or no info available
	if not spx_str:
		if info_msg is not None:
			msg_text = f'{header}\n\n{time_str}\n\n{info_msg}\n\n{notify_str}'
		else:
			msg_text = f'{header}\n\n{time_str}\n\n{notify_str}'
	
	# spx info string provided
	else:
		if info_msg is not None:
			msg_text = f'{header}\n\n{time_str}\n\n{spx_info_str}\n\n{info_msg}\n\n{notify_str}'
		
		else:
			msg_text = f'{header}\n\n{time_str}\n\n{spx_info_str}\n\n{notify_str}'

	bot.sendMessage(chat, msg_text, parse_mode='Markdown')
	return


# handles the /notify command, i.e. toggles notifications
def notify(msg):
	content_type, chat_type, chat = telepot.glance(msg, flavor='chat')
	launch_dir = 'data/launch'

	bot.sendChatAction(chat, action='typing')

	# check if notification database exists
	if not os.path.isfile(os.path.join(launch_dir,'notifications.db')):
		createNotifyDatabase()

	# connect to database
	notify_conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	notify_cursor = notify_conn.cursor()

	# split input
	command_split = msg['text'].strip().split(" ")
	notify_text = ' '.join(command_split[1:]).lower()
	raw_str = ' '.join(command_split[1:])

	# list supported notification settings (missing All)
	supported_notifs = [
	'All',
	'SpaceX',
	'Rocket Lab',
	'Northrop Grumman',
	'ULA',
	'Mitsubishi Heavy Industries',
	'Sea Launch',
	'Arianespace',
	'Eurockot',
	'International Launch Services',
	'ISC Kosmotras',
	'Starsem',
	'Antrix Corporation',
	'CASC',
	'ExPace'
	]

	'''
	'USA',
	'EU',
	'Russia',
	'India',
	'China',
	'''

	supported_notifs_lower = [x.lower() for x in supported_notifs]
	valid_notifs_str = '''⚠️ *Invalid notification!* Below is a list of supported notifications.

		Notifications are sent 24 hours, 12 hours, 60 minutes, and 5 minutes before a flight.

		To list the notifications you currently have enabled, simply send `/notify`.

		To enable *all notifications:* `/notify all` *(EXPERIMENTAL)*

		*New-space launch providers*
		- SpaceX 🇺🇸
		- Rocket Lab 🇺🇸🇳🇿
		- Blue Origin 🇺🇸 (not yet supported)

		*Old-space launch providers*
		- ULA 🇺🇸
		- Northrop Grumman 🇺🇸
		- Mitsubishi Heavy Industries 🇯🇵
		- Sea Launch 🇷🇺

		*Governmental launch service providers*
		- Arianespace 🇪🇺
		- Eurockot 🇪🇺🇷🇺
		- International Launch Services 🇺🇸🇷🇺
		- ISC Kosmotras 🇷🇺🇺🇦🇰🇿
		- Starsem 🇪🇺🇷🇺
		- Antrix Corporation 🇮🇳
		- CASC 🇨🇳
		- ExPace 🇨🇳

		*Space agencies* _(not yet supported)_
		- ESA 🇪🇺
		- NASA 🇺🇸
		- ROSCOSMOS 🇷🇺
		- JAXA 🇯🇵
		- ISRO 🇮🇳
		- CNSA 🇨🇳

		*By launch service provider's country* _(not yet supported)_
		- EU 🇪🇺
		- USA 🇺🇸
		- Japan 🇯🇵
		- India 🇮🇳
		- China 🇨🇳
		- Russia 🇷🇺
		- New Zealand 🇳🇿

		ℹ️ *An example* toggle command would be `/notify Rocket Lab`.
		'''

	if notify_text == 'help' or len(difflib.get_close_matches(notify_text, ['help'])) >= 1:
		valid_notifs_str = valid_notifs_str.replace(
			'⚠️ *Invalid notification!* Below is a list of supported notifications:',
			'ℹ️ *Below is a list of supported notifications:*')
		bot.sendMessage(chat, inspect.cleandoc(valid_notifs_str), parse_mode='Markdown')
		return

	# if no input, simply list all enabled notifications; if all-flag is set, list the disabled notifications
	if len(notify_text) == 0:
		# pull all entries from the notification database that contain the chat ID and are enabled
		notify_cursor.execute("SELECT * FROM notify WHERE chat = ?", (chat,))

		# construct a list of enabled notification
		info_message = ''
		query_return = notify_cursor.fetchall()

		enabled_notifs, disabled_notifs = [], []
		for row in query_return:
			if row[3] == 1:
				enabled_notifs.append(row[1])
			else:
				disabled_notifs.append(row[1])

		# set the all_flag and whether we'll list disabled notifications only
		all_flag = True if 'All' in enabled_notifs else False
		if not all_flag and 'All' in disabled_notifs:
			disabled_notifs.remove('All')

		if not all_flag and len(enabled_notifs) == 0:
			valid_notifs_str = valid_notifs_str.replace(
				'⚠️ *Invalid notification!* Below is a list of supported notifications.',
				'ℹ️ *No notifications are enabled for this chat.* Below is a list of supported notifications:')
			bot.sendMessage(chat, inspect.cleandoc(valid_notifs_str), parse_mode='Markdown')
			return

		# no all_flag enabled, list enabled notifications
		elif len(enabled_notifs) > 0 and not all_flag:
			info_message = '🔔 The following notifications are enabled for this chat\n\n'
			
			for notif, i in zip(enabled_notifs, range(len(enabled_notifs))):
				info_message = info_message + '\t- ' + notif

				if i != len(enabled_notifs) - 1:
					info_message = info_message + '\n'

			info_message = info_message + '\n\n🔕 Disable a notification by sending `/notify` again.'
			info_message = info_message + '\n*For a list of supported notifications:* `/notify help`.'

			bot.sendMessage(chat, inspect.cleandoc(info_message), parse_mode='Markdown')
			notify_conn.close()
			return

		# all_flag enabled, list disabled notifications only
		elif all_flag:
			if len(disabled_notifs) > 0:
				info_message = '🔔 You have notifications enabled for all flights, with some exceptions.\n'
				info_message += '🔕 The following notifications are *disabled* for this chat:\n\n'
				
				for notif, i in zip(disabled_notifs, range(len(disabled_notifs))):
					info_message = info_message + '\t- ' + notif

					if i != len(disabled_notifs) - 1:
						info_message = info_message + '\n'

				info_message = info_message + '\n\n*Enable a notification* by sending `/notify` for that keyword again.'
				info_message = info_message + '\n*For a list of supported notifications:* `/notify help`.'

			else:
				info_message = '''🔔 You have notifications enabled for all flights, with no exceptions.
				
				🔕 To not receive a notification, send `/notify` for that launch provider.
				To disable notifications for *all flights*: `/notify all`
				
				For a list of supported notifications: `/notify help`.
				'''

			bot.sendMessage(chat, inspect.cleandoc(info_message), parse_mode='Markdown')
			notify_conn.close()
			return

	# get close matches to our input
	close_matches = difflib.get_close_matches(raw_str, supported_notifs)

	# check if we have a direct match; if we do have, match the lower-cased input to the correctly cased name
	if notify_text in supported_notifs_lower:
		raw_str = supported_notifs[supported_notifs_lower.index(notify_text)]

	# no close matches; invalid input
	elif len(close_matches) == 0:
		bot.sendMessage(chat, inspect.cleandoc(valid_notifs_str), parse_mode='Markdown')
		return

	else: # found a close match, continue
		if len(close_matches) == 1:
			if debug_log:
				logging.info(f'ℹ️ User mistype corrected: {raw_str} -> {close_matches[0]}')
			notify_text, raw_str = close_matches[0], close_matches[0]
			pass
		
		else: # multiple close matches, ask user what they meant
			meant_str = ' or '.join(close_matches)
			if debug_log:
				logging.info(f'ℹ️ User mistype corrected: "{meant_str}"')
			bot.sendMessage(chat, f'⚠️ Did you mean {meant_str}?')
			return


	# pull all entries from the notification database that contain the chat ID and notify_text
	notify_cursor.execute("SELECT * FROM notify WHERE chat = ? AND keyword = ? OR chat = ? AND keyword = ?", (chat, raw_str, chat, 'All'))

	# n rows, in the form of chat ID - keyword - UNIX-timestamp - enabled
	query_return = notify_cursor.fetchall()

	# check if the chat has the all-flag set to enabled
	all_flag, parsed_query_return = False, []
	for row, i in zip(query_return, range(len(query_return))):
		if 'All' in row[1]:
			if row[3] == 1:
				all_flag = True
			else:
				all_flag = False
			
			if len(query_return) == 1 and raw_str == 'All':
				parsed_query_return.append([row[0], row[1], row[2], row[3]])
		else:
			parsed_query_return.append([row[0], row[1], row[2], row[3]])

	query_return = tuple(parsed_query_return)

	# check if the notification string can already be found; if it is, just do as we normally would
	if len(query_return) > 0:
		# if enabled, disable, and vice versa
		new_status = 0 if query_return[0][3] == 1 else 1
		notify_status = 'enabled' if new_status == 1 else 'disabled'
		notify_icon = {0:'🔕', 1:'🔔'}[new_status]

		if raw_str != 'All':
			extra_info_str = {
			0: f'''All notifications for flights of {raw_str} have been disabled.
			You will not receive any further notifications.

			To re-enable notifications, send `/notify {raw_str}` again.
			''',
			1: f'''You will now be notified for all flights of {raw_str}.
			
			The first notification is sent 24 hours before a flight.
			To disable notifications, send `/notify {raw_str}` again.
			'''
			}[new_status]

			info_string = f'''
			{notify_icon} Notifications *{notify_status}* for *{raw_str}*

			{extra_info_str}
			'''

		else:
			extra_info_str = {
			0: f'''You have disabled the all-notifications flag.
			You will continue receiving the notifications you have manually set.

			To list the notifications you will still receive: `/notify`
			''',
			
			1: f'''You will now be notified for all flights, excluding the ones you have disabled manually.
			The first notification is sent 24 hours before a flight.

			To list the notifications you have disabled: `/notify`
			'''
			}[new_status]

			info_string = f'''
			{notify_icon} Notifications *{notify_status}* for *all flights*

			{extra_info_str}
			'''

		if debug_log:
			logging.info(f'🔀 chat {chat} set keyword={raw_str} to {new_status}')

		notify_cursor.execute("UPDATE notify SET enabled = ? WHERE chat = ? AND keyword = ?", (new_status, chat, raw_str))
		bot.sendMessage(chat, inspect.cleandoc(info_string), parse_mode='Markdown')

	# notification can't be found; insert as new
	else:
		if raw_str == 'All':
			notify_cursor.execute("INSERT INTO notify (chat, keyword, lastnotified, enabled) VALUES (?, ?, 0, 1)", (chat, raw_str))

			info_string = f'''🔔 Notifications *enabled* for all flights!
			
			Individual notifications can be disabled with /notify.
			The first notification is sent 24 hours before a flight.
			'''

			if debug_log:
				logging.info(f'🔀 chat {chat} enabled keyword={raw_str} for the first time. all_flag=False')

		elif not all_flag:
			notify_cursor.execute("INSERT INTO notify (chat, keyword, lastnotified, enabled) VALUES (?, ?, 0, 1)", (chat, raw_str))
			info_string = f'''🔔 Notifications *enabled* for *{raw_str}*.

			You will now be notified for all flights of {raw_str}. 
			The first notification is sent 24 hours before a flight.
			'''

			if debug_log:
				logging.info(f'🔀 chat {chat} enabled keyword={raw_str} for the first time. all_flag=False')

		else:
			notify_cursor.execute("INSERT INTO notify (chat, keyword, lastnotified, enabled) VALUES (?, ?, 0, 0)", (chat, raw_str))
			info_string = f'''🔕 Notifications *disabled* for *{raw_str}*.

			To re-enable notifications for *{raw_str}*, simply send this command again.
			To list your enabled notifications, send `/notify`.
			'''

			if debug_log:
				logging.info(f'🔀 chat {chat} disabled keyword={raw_str}. all_flag=True, defaulted to disable.')

		bot.sendMessage(chat, inspect.cleandoc(info_string), parse_mode='Markdown')

	notify_conn.commit()
	notify_conn.close()
	return


# handles API update requests and decides on which notification to send
def launchUpdateCheck():
	# compare data to data found in local launch database
	# send a notification if launch time is approaching

	# every time this is ran: (every 15-30 seconds)
	# check data (T-) in launch.db
	# if we're past a threshold, check for updates again, then notify if we're still past the threshold:
		# if T- < 24 hours, notify (if not notified)
		# if T- < 12 hours, notify (if not notified)
		# if T- < 1 hour, notify (if not notified)
		# if T- < 5 minutes, notify (if not notified)

	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir, 'launches.db')):
		createLaunchDatabase()
		getLaunchUpdates(None)

	# Establish connection to the launch database
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
	c = conn.cursor()

	# Select all launches from the database that have a T- of less than 24 hours and 15 seconds
	Tminus_threshold_24h = 24*3600 + 15
	Tminus_threshold_12h = 12*3600 + 15
	Tminus_threshold_1h = 1*3600 + 15
	Tminus_threshold_5m = 5*60 + 15

	# current unix time, also construct the unix time ranges
	now_timestamp = time.mktime(datetime.datetime.today().timetuple())
	unix_24h_threshold = now_timestamp + Tminus_threshold_24h
	unix_12h_threshold = now_timestamp + Tminus_threshold_12h
	unix_60m_threshold = now_timestamp + Tminus_threshold_1h
	unix_5m_threshold = now_timestamp + Tminus_threshold_5m

	c.execute(f'''SELECT * FROM launches 
		WHERE 
		NET <= {unix_24h_threshold} AND NET >= {now_timestamp} AND notify24h = 0 OR
		NET <= {unix_12h_threshold} AND NET >= {now_timestamp} AND notify12h = 0 OR 
		NET <= {unix_60m_threshold} AND NET >= {now_timestamp} AND notify60min = 0 OR
		NET <= {unix_5m_threshold} AND NET >= {now_timestamp} AND notify5min = 0''')

	query_return = c.fetchall()
	if len(query_return) == 0:
		updateStats({'db_calls': 1})
		return

	# we presumably have at least one launch now that has an unsent notification
	# update the database, then check again
	if debug_log:
		logging.info(f'⏰ Found {len(query_return)} pending notifications... Updating database to verify.')
	
	getLaunchUpdates(None)
	c.execute(f'''SELECT * FROM launches 
		WHERE 
		NET <= {unix_24h_threshold} AND NET >= {now_timestamp} AND notify24h = 0 OR
		NET <= {unix_12h_threshold} AND NET >= {now_timestamp} AND notify12h = 0 OR 
		NET <= {unix_60m_threshold} AND NET >= {now_timestamp} AND notify60min = 0 OR
		NET <= {unix_5m_threshold} AND NET >= {now_timestamp} AND notify5min = 0''')
	
	updateStats({'db_calls': 2})
	query_return = c.fetchall()
	if len(query_return) == 0:
		return

	for row in query_return:
		# decide which notification to send
		curr_Tminus = query_return[0][10]
		NET = query_return[0][9]
		status_24h, status_12h, status_1h, status_5m = query_return[0][11], query_return[0][12], query_return[0][13], query_return[0][14]

		notif_class = []
		if NET <= unix_24h_threshold and status_24h == 0:
			notif_class.append('24h')
		if NET <= unix_12h_threshold and status_12h == 0:
			notif_class.append('12h')
		if NET <= unix_60m_threshold and status_1h == 0:
			notif_class.append('1h')
		if NET <= unix_5m_threshold and status_5m == 0:
			# if the launch already happened, don't notify
			if now_timestamp - NET > 0:
				if now_timestamp - NET > 600:
					notif_class = []
					if debug_log:
						logging.info(f'🛑 Launch happened {now_timestamp - NET} seconds ago; aborted notification sending. id: {row[1]}')

					return
				else:
					notif_class.append('5m')
			else:
				notif_class.append('5m')
		
		if len(notif_class) == 0:
			if debug_log:
				logging.info(f'⚠️ Error setting notif_class in notificationHandler(): curr_Tminus:{curr_Tminus}, launch:{query_return[0][1]}.\
				 24h: {status_24h}, 12h: {status_12h}, 1h: {status_1h}, 5m: {status_5m}')
			
			return

		else:
			if debug_log:
				logging.info(f'✅ Set {len(notif_class)} notif_classes. Timestamp: {now_timestamp}, flt NET: {NET}')

		notificationHandler(row, notif_class)

	return


# handles r/spacex api requests
def spxAPIHandler():
	'''
	This function performs an API call to the r/SpaceX API and updates the database with
	the returned information. 

	constructParams():
		Dynamically constructs the parameters for the request URL so we don't have to do it manually.
	
	multiParse():
		Parses the returned json-file by iterating over the launches found in the json, and updating
		the database with the information.
	'''

	def constructParams(PARAMS):
		param_url, i = '', 0
		if PARAMS is not None:
			for key, val in PARAMS.items():
				if i == 0:
					param_url += f'?{key}={val}'
				else:
					param_url += f'&{key}={val}'
				i += 1

		return param_url

	def multiParse(json, launch_count):
		# check if db exists
		launch_dir = 'data/launch'
		if not os.path.isfile(os.path.join(launch_dir, 'spx-launches.db')):
			createSPXDatabase()

		# open connection
		conn = sqlite3.connect(os.path.join(launch_dir, 'spx-launches.db'))
		c = conn.cursor()

		# launch, id, keywords, countrycode, NET, T-, notify24hour, notify12hour, notify60min, notify5min, success, launched, hold
		for i in range(0, launch_count):
			# json of flight i
			launch = launch_json[i]

			# extract relevant information
			launch_num = launch['flight_number']
			launch_name = launch['mission_name'].lower()
			
			try:
				net = launch['launch_date_unix']
			except:
				net = 0
			
			try:
				orbit = launch['rocket']['second_stage']['payloads'][0]['orbit']
			except:
				orbit = '?'
			
			try:
				fairing_reused = launch['rocket']['fairings']['reused']
			except:
				if launch['rocket']['fairings'] == None:
					if 'dragon' in launch['rocket']['second_stage']['payloads'][0]['payload_type'].lower():
						dragon_type = launch['rocket']['second_stage']['payloads'][0]['payload_type']
						try:
							dragon_serial = launch['rocket']['second_stage']['payloads'][0]['cap_serial']
						except:
							dragon_serial = '?'
						dragon_reused = launch['rocket']['second_stage']['payloads'][0]['reused']
						dragon_crew = launch['crew']
						fairing_reused = f'{dragon_type}/{dragon_serial}/{dragon_reused}/{dragon_crew}'
				else:
					fairing_reused = None

			try:
				fairing_rec_attempt = launch['rocket']['fairings']['recovery_attempt']
			except:
				fairing_rec_attempt = None

			try:
				fairing_ship = launch['rocket']['fairings']['ship']
			except:
				fairing_ship = None

			try:
				cores = launch['rocket']['first_stage']['cores']
				vehicle_type = 'FH' if len(cores) > 1 else 'F9'
			except:
				cores = None
				vehicle_type = None

			# iterate through found booster information (FH has three boosters, that's why)
			# also handle the extremely prevalent NULL cases in the returned .json
			if cores != None:
				reuses, serials, landing_intents = '', '', ''
				for core, i in zip(cores, range(len(cores))):
					# serials
					if core['core_serial'] != None:
						serials = serials + str(core['core_serial'])
					else:
						serials = serials + 'Unknown'

					# reuses
					if core['reused'] != None:
						if core['reused'] == True:
							if core['flight'] != None:
								reuses = reuses + str(core['flight'] - 1)
							else:
								reuses = reuses + '?'
						else:
							reuses = reuses + '0'
					else:
						reuses = reuses + 'Unknown'

					# landing intents
					if core['landing_intent'] != None:
						if core['landing_intent'] == True:
							landing_intents = landing_intents + f"{core['landing_type']}/{core['landing_vehicle']}"
						else:
							landing_intents = landing_intents + 'expend'
					else:
						landing_intents = landing_intents + 'Unknown'

					if i < len(cores) - 1:
						reuses = reuses + ','
						serials = serials + ','
						landing_intents = landing_intents + ','

			else:
				reuses, serials, landing_intents = None, None, None

			# if launch name in database, update values; if not, insert
			try:
				c.execute('''INSERT INTO launches \
					(flight_num, launch_name, NET, orbit, vehicle, core_serials, core_reuses, landing_intents,
					fairing_reused, fairing_rec_attempt, fairing_ship)\
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)''',
					(launch_num, launch_name, net, orbit, vehicle_type,
						serials, reuses, landing_intents, fairing_reused, fairing_rec_attempt, fairing_ship)
					)
			except:
				c.execute('''UPDATE launches \
					SET flight_num = ?, NET = ?, orbit = ?, vehicle = ?, core_serials = ?, core_reuses = ?,
					landing_intents = ?, fairing_reused = ?, fairing_rec_attempt = ?, fairing_ship = ? \
					WHERE launch_name = ?''',
						(launch_num, net, orbit, vehicle_type, serials, reuses, landing_intents, fairing_reused,
						fairing_rec_attempt, fairing_ship, launch_name)
					)

		conn.commit()
		conn.close()
		return
	

	'''
	To pull all launches for debugging purposes:
		API_REQUEST = f'launches'
		PARAMS = {'limit': 100}
	'''

	# datetime, so we can only get launches starting today
	now = datetime.datetime.now()

	# what we're throwing at the API
	API_REQUEST = f'launches/upcoming'
	PARAMS = {'limit': 10, 'start': f'{now.year}-{now.month}-{now.day}'}
	API_URL = 'https://api.spacexdata.com'
	API_VERSION = 'v3'

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{constructParams(PARAMS)}'
	
	try: # perform the API call
		API_RESPONSE = requests.get(API_CALL)
	except Exception as e:
		if debug_log:
			logging.info(f'🛑 Error in r/SpaceX API request: {e}')
			logging.info(f'⚠️ Trying again after 3 seconds...')

		time.sleep(3)
		spxAPIHandler()

		if debug_log:
			logging.info(f'✅ Success!')
		
		return

	# parse all launches one-by-one in the returned json-file
	launch_json = API_RESPONSE.json()
	multiParse(launch_json, len(launch_json))

	# update stats
	updateStats({'API_requests':1, 'db_updates':1, 'data':len(API_RESPONSE.content)})

	return


# constructs an information string for a SpaceX launch with relevant booster & mission information
def spxInfoStrGen(launch_name, run_count):
	# open the database connection and check if the launch exists in the database
	# if not, update
	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir, 'spx-launches.db')):
		createSPXDatabase()
		spxAPIHandler()

	# open connection
	conn = sqlite3.connect(os.path.join(launch_dir, 'spx-launches.db'))
	c = conn.cursor()

	# unix time for NET
	today_unix = time.mktime(datetime.datetime.today().timetuple())

	# perform a raw select; if not found, pull all and do some diffing
	# launch names are stored in lower case
	c.execute('''SELECT * FROM launches WHERE launch_name = ?''', (launch_name.lower(),))
	query_return = c.fetchall()
	if len(query_return) == 0:
		# try pulling all launches, diff them, sort by NET
		c.execute('''SELECT * FROM launches WHERE NET >= ?''', (today_unix,))
		query_return = c.fetchall()

		launch_names = {} # launch name -> NET dictionary
		for row in query_return:
			if row[1] not in launch_names:
				launch_names[row[1]] = row[2]

		# perform the diffing; strip keys of parantheses for more accurate results
		stripped_keys = []
		for key in launch_names.keys():
			stripped_keys.append(key.replace('(','').replace(')',''))

		# diff
		close_matches = difflib.get_close_matches(launch_name, stripped_keys)

		# no matches, use the stripped keys
		launch_name_stripped = launch_name.replace('(','').replace(')','').lower()
		if len(close_matches) == 0:
			close_matches = difflib.get_close_matches(launch_name_stripped, stripped_keys)
			if len(close_matches) == 1:
				if debug_log:
					logging.info(f'Close match found for {launch_name_stripped}: {close_matches}')
				
				diff_match = close_matches[0]

			elif len(close_matches) == 0:
				if debug_log:
					logging.info(f'🛑 Error finding {launch_name_stripped} from keys!\nStripped_keys:')
					for key in stripped_keys:
						logging.info(key)
				
				return

			elif len(close_matches) > 1:
				if debug_log:
					logging.info(f'⚠️ More than one close match when attempting to find {launch_name_stripped}; \
					matches: {close_matches}. Returning.')
				
				return


		elif len(close_matches) == 1:
			if debug_log:
				logging.info(f'✅ Got a single close match! {launch_name_stripped} -> {close_matches}')
			
			diff_match = close_matches[0]
		
		elif len(close_matches) > 1:
			smallest_net, net_index = close_matches[0][2], 0
			for row, i in zip(close_matches, range(len(close_matches))):
				if row[2] < smallest_net:
					smallest_net, net_index = row[2], i

			if debug_log:
				logging.info(logging.info(f'⚠️ Got more than 1 close_match initially; parse by NET. {launch_name_stripped} -> {close_matches}'))

			diff_match = close_matches[net_index]

		else:
			if run_count == 0:
				if debug_log:
					logging.info(f'🛑 Error in spxInfoStrGen: unable to find launches \
						with a NET >= {today_unix}. Updating and trying again...')

				spxAPIHandler()
				spxInfoStrGen(launch_name, 1)
			else:
				if debug_log:
					logging.info(f'🛑 Error in spxInfoStrGen: unable to find launches \
						with a NET >= {today_unix}. Tried once before, not trying again.')

			return

	elif len(query_return) == 1:
		if debug_log:
			logging.info(f'✅ Got a single return from the query – no need to diff! {launch_name} -> {query_return}')
		
		db_match = query_return[0]
		diff_match = None

	else:
		if debug_log:
			logging.info(f'⚠️ Error in spxInfoStrGen(): got more than one launch. \
				query: {launch_name}, return: {query_return}')

		return

	# if we got a diff_match, pull the launch manually from the spx database
	if diff_match != None:
		c.execute('''SELECT * FROM launches WHERE launch_name = ?''', (diff_match,))
		query_return = c.fetchall()
		if len(query_return) == 1:
			if debug_log:
				logging.info(f'✅ Found diff_match from database!')
			db_match = query_return[0]
		else:
			if debug_log:
				logging.info(f'🛑 Found {len(query_return)} matches from db... Exiting')
			return

	# same found in multiparse
	# use to extract info from db
	# row stored in db_match
	# flight_num 0, launch_name 1, NET 2, orbit 3, vehicle 4, core_serials 5
	# core_reuses 6, landing_intents 7, fairing_reused 8, fairing_rec_attempt 9, fairing_ship 10

	# booster information
	if db_match[4] == 'FH': # a Falcon Heavy launch
		reuses = db_match[6].split(',')
		try:
			int(reuses[0])
			if int(reuses[0]) > 0:
				center_reuses = f"♻️x{int(reuses[0])}"
			else:
				center_reuses = f'✨ new'
		except:
			center_reuses = f'♻️ ?'

		try:
			int(reuses[1])
			if int(reuses[1]) > 0:
				booster1_reuses = f"♻️x{int(reuses[1])}"
			else:
				booster1_reuses = f'✨ new'
		except:
			booster1_reuses = f'♻️ ?'

		try:
			int(reuses[2])
			if int(reuses[2]) > 0:
				booster2_reuses = f"♻️x{int(reuses[2])}"
			else:
				booster2_reuses = f'✨ new'
		except:
			booster2_reuses = f'♻️ ?'

		# pull serials from db, construct serial strings
		serials = db_match[5].split(',')
		core_serial = f"{serials[0]} {center_reuses}"
		booster_serials = f"{serials[1]} {booster1_reuses} + {serials[2]} {booster2_reuses}"

		landing_intents = db_match[7].split(',')
		if landing_intents[0] != 'expend':
			center_recovery = f"{landing_intents[0]}"
		else:
			center_recovery = f"No recovery — godspeed, {serials[0]}"

		if landing_intents[1] != 'expend':
			booster1_recovery= f"{landing_intents[1]}"
		else:
			booster1_recovery = f"No recovery — godspeed, {serials[1]}"

		if landing_intents[2] != 'expend':
			booster2_recovery = f"{landing_intents[2]}"
		else:
			booster2_recovery = f"No recovery — godspeed, {serials[2]}"

	
	else: # single-stick
		core_serial = db_match[5]

		# recovery
		landing_intents = db_match[7]
		if landing_intents != 'expend':
			if 'None' in landing_intents:
				recovery_str = 'Unknown'
			else:
				recovery_str = f"{landing_intents}"
		else:
			recovery_str = f'No recovery — godspeed, {core_serial}'

	# construct the Falcon-specific information message
	if db_match[4] == 'FH':
		header = f'*Falcon Heavy configuration*\n*Center core* `{core_serial}`\n*Boosters* `{booster_serials}`'
		if landing_intents[1] == 'expend' and landing_intents[2] == 'expend':
			rec_str = f'*Recovery operations*\n*Center core* `{center_recovery}`'
			boost_str = f'*Boosters* No recovery – godspeed, {serials[1]} & {serials[2]}'
			spx_info = f'{header}\n\n{rec_str}\n{boost_str}'
		
		else:
			rec_str = f'*Recovery operations*\n*Center core* `{center_recovery}`'
			boost_str = f'*Boosters* `{booster1_recovery}` & `{booster2_recovery}`'
			spx_info = f'{header}\n\n{rec_str}\n{boost_str}'

		if core_serial == 'Unknown':
			spx_info = f'ℹ️ No FH configuration information available yet'

	# not a FH? Then it's _probably_ a F9
	elif db_match[4] == 'F9':
		reuses = db_match[6]
		try:
			int(reuses)
			if int(reuses) > 0:
				reuses = f"♻️x{int(reuses)}"
			else:
				reuses = f'✨ new'
		except:
			reuses = f'♻️ ?'

		spx_info = f'*Booster information*\n*Core* `{core_serial}` `{reuses}`\n*Recovery* `{recovery_str}`'

		if core_serial == 'Unknown':
			spx_info = f'ℹ️ No booster information available yet'

	else:
		if debug_log:
			logging.info(f'🛑 Error in spxInfoStrGen: vehicle not found ({db_match[4]})')
		
		return None

	# check if there is fairing recovery & orbit information available
	if db_match[8] != '0' and db_match[8] != '1':
		if 'Dragon' in db_match[8]: # check if it's a Dragon flight
			dragon_info = db_match[8].split('/')
			dragon_serial = 'Unknown' if dragon_info[1] == 'None' else dragon_info[1]
			dragon_reused = '♻️ *reused*' if dragon_info[2] == 'True' else '✨ new'
			dragon_crew = dragon_info[3]
			
			crew_str = ''
			if 'Crew' in dragon_info[0] and dragon_crew != 'None':
				if int(dragon_crew) != 0:
					for i in range(int(dragon_crew)):
						crew_str += '👨‍🚀'
				else:
					crew_str = 'Unmanned'
			elif 'Crew' in dragon_info[0] and dragon_crew == 'None':
				crew_str = 'Unmanned/Unknown'			
			elif 'Crew' not in dragon_info[0]:
				crew_str = 'Cargo mission'

			fairing_info = f'*Dragon information*\n*Type* {dragon_info[0]}\n*SN* {dragon_serial} {dragon_reused}\n*Crew* {crew_str}'
			spx_info = spx_info + '\n\n' + fairing_info

	else:
		try:
			if int(db_match[8]) == 1 or int(db_match[8]) == 0:
				if db_match[9] != None:
					try: 
						if int(db_match[9]) == 1:
							if db_match[10] != None:
								rec_str = db_match[10]
							else:
								rec_str = 'Unknown'
						else:
							rec_str = 'No recovery'
					except:
						rec_str = 'Unknown'
				else:
					rec_str = 'Unknown'

				status_str = '♻️ Reused' if db_match[8] == 1 else '✨ New'
				fairing_info = f"*Fairing information*\n*Status* `{status_str}`\n*Recovery* `{rec_str}`"
				spx_info = spx_info + '\n\n' + fairing_info

		except Exception as e:
			if debug_log:
				logging.info(f'{e}')
			pass

	return spx_info


# handles API requests from launchUpdateCheck()
def getLaunchUpdates(launch_ID):
	def constructParams(PARAMS):
		param_url, i = '', 0
		if PARAMS is not None:
			for key, val in PARAMS.items():
				if i == 0:
					param_url += f'?{key}={val}'
				else:
					param_url += f'&{key}={val}'
				i += 1

		return param_url

	def multiParse(json, launch_count):
		# check if db exists
		launch_dir = 'data/launch'
		if not os.path.isfile(os.path.join(launch_dir, 'launches.db')):
			createLaunchDatabase()

		# open connection
		conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
		c = conn.cursor()

		# launch, id, keywords, countrycode, NET, T-, notify24hour, notify12hour, notify60min, notify5min, success, launched, hold
		for i in range(0, launch_count):
			# json of flight i
			launch_json = API_RESPONSE.json()['launches'][i]

			# extract stuff
			launch_name = launch_json['name'].split('|')[1]
			launch_id = launch_json['id']
			status = launch_json['status']

			# extract: lsp_name, vehicle, pad, info
			lsp_name = launch_json['lsp']['name']
			lsp_short = launch_json['lsp']['abbrev']
			vehicle = launch_json['rocket']['name']
			location_name = launch_json['location']['pads'][0]['name']
			
			# find a video url, preferably a youtube link
			try:
				if 'vidURLs' in launch_json:
					urls = launch_json['vidURLs']
					vid_url = None
					
					for url in urls:
						if 'youtube' in url:
							vid_url = url
							break
					
					if vid_url is None:
						vid_url = urls[0]
			except:
				vid_url = ''
			
			if 'Unknown Pad' not in location_name:
				pad = location_name.split(', ')[0]
			else:
				pad = launch_json['location']['name']

			try:
				if launch_json['missions'][0]['description'] != '':
					mission_text = launch_json['missions'][0]['description'].split('\n')[0]
				else:
					mission_text = None
			except:
				mission_text = None

			# Integer (1 Green, 2 Red, 3 Success, 4 Failed) 5 = ?, 6 = in flight?
			success = {1:0, 2:0, 3:1, 4:-1, 5:0, 6:0}[status]
			lsp = launch_json['lsp']['id']
			countrycode = launch_json['lsp']['countryCode']

			if success in [1, -1]:
				launched, holding = 1, -1

			elif success in [2]:
				launched, holding = 0, 1

			elif success in [0]:
				launched, holding = 0, 0


			if launch_json['netstamp'] != 0:
				# construct datetime from netstamp
				net_unix = launch_json['netstamp']
				net_stamp = datetime.datetime.fromtimestamp(net_unix)
				today_unix = time.mktime(datetime.datetime.today().timetuple())

				if today_unix <= net_unix:
					Tminus = abs(datetime.datetime.today() - net_stamp).seconds
				else:
					Tminus = 0

			else:
				net_unix, Tminus = -1, -1

			# update if launch ID found, insert if id not found
			# launch, id, keywords, lsp_name, vehicle, pad, info, countrycode, NET, Tminus
			# notify24h, notify12h, notify60min, notify5min, notifylaunch, success, launched, hold
			
			# lsp_name, vehicle, pad, mission_text
			try:
				c.execute('''INSERT INTO launches
					(launch, id, keywords, lsp_name, lsp_short, vehicle, pad, info, countrycode, NET, Tminus,
					notify24h, notify12h, notify60min, notify5min, notifylaunch, success, launched, hold, vid)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, ?, ?, ?, ?)''',
					(launch_name, launch_id, lsp, lsp_name, lsp_short, vehicle, pad, mission_text, countrycode, net_unix, Tminus, success, launched, holding, vid_url))
			
			except:
				c.execute('''UPDATE launches
					SET NET = ?, Tminus = ?, success = ?, launched = ?, hold = ?, info = ?, pad = ?, vid = ?
					WHERE id = ?''', (net_unix, Tminus, success, launched, holding, mission_text, pad, vid_url, launch_id))

		conn.commit()
		conn.close()
		return
	
	# datetime, so we can only get launches starting today
	now = datetime.datetime.now()
	today_call = f'{now.year}-{now.month}-{now.day}'

	# what we're throwing at the API
	API_REQUEST = f'launch'
	PARAMS = {'mode': 'verbose', 'limit': 40, 'startdate': today_call}
	API_URL = 'https://launchlibrary.net'
	API_VERSION = '1.4'

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{constructParams(PARAMS)}' #&{fields}
	
	# perform the API call
	headers = {'user-agent': 'telegram-launchbot/0.2'}

	try:
		API_RESPONSE = requests.get(API_CALL, headers=headers)
	except Exception as e:
		if debug_log:
			logging.info(f'🛑 Error in API request: {e}')
			logging.info(f'⚠️ Trying again after 3 seconds...')

		time.sleep(3)
		getLaunchUpdates(None)

		if debug_log:
			logging.info(f'✅ Success!')
		
		return

	# pull json, dump for later inspection
	launch_json = API_RESPONSE.json()

	# if we got nothing in return from the API
	if len(launch_json['launches']) == 0:
		if debug_log:
			if API_RESPONSE.status_code == 404:
				logging.info('⚠️ No launches found!')
			else:
				logging.info(f'⚠️ Failed request with status code {API_RESPONSE.status_code}')
		
		return

	# we got something, parse all of it
	elif len(launch_json['launches']) >= 1:
		multiParse(launch_json, len(launch_json['launches']))

	updateStats({'API_requests':1, 'db_updates':1, 'data':len(API_RESPONSE.content)})
	return


# gets a request to send a notification about launch X from launchUpdateCheck()
def notificationHandler(launch_row, notif_class):
	# handle notification sending, so we can retry more easily and handle exceptions
	def sendNotification(chat, notification):
		try:
			bot.sendMessage(chat, notification, parse_mode='Markdown')
			return True
		except telepot.exception.BotWasBlockedError:
				if debug_log:
					logging.info(f'⚠️ Bot was blocked by {chat} – cleaning notifiy database...')

				conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
				c = conn.cursor()
				
				c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
				conn.commit()
				conn.close()

				return True

		except Exception as caught_exception:
			return caught_exception


	# lsp ID -> name dictionary
	LSP_IDs = {
	121: 	['SpaceX', '🇺🇸'],
	147: 	['Rocket Lab', '🇺🇸'],
	99: 	['Northrop Grumman', '🇺🇸'],
	115: 	['Arianespace', '🇪🇺'],
	124: 	['ULA', '🇺🇸'],
	98: 	['Mitsubishi Heavy Industries', '🇯🇵'],
	88: 	['CASC', '🇨🇳'],
	190: 	['Antrix Corporation', '🇮🇳'],
	122: 	['Sea Launch', '🇷🇺'],
	118: 	['ILS', '🇺🇸🇷🇺'],
	193: 	['Eurockot', '🇪🇺🇷🇺'],
	119:	['ISC Kosmotras', '🇷🇺🇺🇦🇰🇿'],
	123:	['Starsem SA', '🇪🇺🇷🇺'],
	194:	['ExPace', '🇨🇳']
	}

	# map country codes found in launch database to human-friendly strings
	ccode_map = {
	'CHN': 'China',
	'USA': 'USA',
	'FRA': 'EU',
	'RUS': 'Russia',
	'JPN': 'Japan',
	'IND': 'India'
	}

	'''
	# agency name -> ID
	agency_IDs = {
	'NASA': 44,
	'ESA': 27,
	'JAXA': 37, 
	'ROSCOSMOS': 63,
	'ISRO': 31,
	'CNSA': 17
	}
	'''

	launch_id = launch_row[1]
	keywords = int(launch_row[2])

	# check if LSP ID in keywords is in our custom list
	if keywords not in LSP_IDs.keys():
		lsp, lsp_flag = None, ''
	else:
		lsp = LSP_IDs[keywords][0]
		lsp_flag = LSP_IDs[keywords][1]

	# pull launch information from database
	launch_dir = 'data/launch'
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
	c = conn.cursor()

	# select the launch we're tracking
	c.execute(f'''SELECT * FROM launches WHERE id = {launch_id}''')
	query_return = c.fetchall()

	launch_name = query_return[0][0]
	vehicle = query_return[0][5]
	pad = query_return[0][6]
	info_text = query_return[0][7]

	if info_text != None:
		info_text = f'ℹ️ {info_text}'
	else:
		info_text = f'ℹ️ No launch information available'
	
	if lsp is None:
		lsp = query_return[0][3]
		lsp_short = query_return[0][4]

	launch_unix = datetime.datetime.utcfromtimestamp(query_return[0][9])
	if launch_unix.second == 0:
		if launch_unix.minute < 10:
			launch_time = f'{launch_unix.hour}:0{launch_unix.minute}'
		else:
			launch_time = f'{launch_unix.hour}:{launch_unix.minute}'
	else:
		if launch_unix.second < 10:
			sec_time = f'0{launch_unix.second}'
		else:
			sec_time = launch_unix.second

		if launch_unix.minute < 10:
			min_time = f'0{launch_unix.minute}'
		else:
			min_time = launch_unix.minute

		launch_time = f'{launch_unix.hour}:{min_time}.{sec_time}'

	# map notif_class to sqlite column names
	notif_dict = {
	'24h': 'notify24h',
	'12h': 'notify12h',
	'1h': 'notify60min',
	'5m': 'notify5min'
	}

	# if we have more than one entry in notif_class, toggle the ones that should've been sent already
	if len(notif_class) > 1:
		if debug_log:
			logging.info('⚠️ More than one notification in notif_class; attempting to handle properly...')

		# set notif_class to the list's last entry, so we avoid sending double notifications (i.e. 24h and 12h at the same time)
		notif_class_list = notif_class # dumb variable names result in dumb code eh
		notif_class = notif_class_list.pop(-1)

		# handle the remaining entries; db connection should be open
		for notif_time in notif_class_list:
			try:
				notification_type = notif_dict[notif_time] # map the notification time to database column name
				c.execute(f'UPDATE launches SET {notification_type} = 1 WHERE id = ?', (launch_id,))
				
				if debug_log:
					logging.info(f'\t✅ notification disabled without sending for notif_time={notif_time}, launch_id={launch_id}')
			
			except Exception as e:
				if debug_log:
					logging.info(f'\t🛑 Error disabling notification: {e}')

		conn.commit()

	else:
		notif_class = notif_class[-1]

	# used to construct the message, e.g. "launching in 1 hour" or "launching in 5 minutes" etc.
	if 'h' in notif_class:
		t_minus = int(notif_class.replace('h',''))
		time_format = 'hour' if notif_class == '1h' else 'hours'
	else:
		t_minus = int(notif_class.replace('m',''))
		time_format = 'minutes'

	# shorten long launch service provider names
	lsp_name = lsp_short if len(lsp) > len('Arianespace') else lsp

	# if it's a SpaceX launch, pull get the info string
	if lsp_name == 'SpaceX':
		spx_info_str = spxInfoStrGen(launch_name, 0)
		if spx_info_str != None:
			spx_str = True
		else:
			spx_str = False

	# do some string magic to reduce the space width of monospaced text in the telegram message
	lsp_str = ' '.join("`{}`".format(word) for word in lsp_name.split(' '))
	vehicle_name = ' '.join("`{}`".format(word) for word in vehicle.split(' '))
	pad_name = ' '.join("`{}`".format(word) for word in pad.split(' '))

	# construct the "base" message
	message_header = f'🚀 *{launch_name}* is launching in *{t_minus} {time_format}*\n'
	message_header += f'*Launch provider* {lsp_str} {lsp_flag}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'
	message_footer = f'*🕓 The launch is scheduled* for `{launch_time} UTC`\n'
	message_footer += f'*🔕 To disable:* `/notify {lsp_name}`'
	launch_str = message_header + '\n\n' + info_text + '\n\n' + message_footer

	# if NOT a SpaceX launch and we're close to launch, add the video URL
	if lsp_name != 'SpaceX':
		# a different kind of message for 60m and 5m messages, which contain the video url (if one is available)
		if notif_class in ['1h', '5m'] and launch_row[-1] != '': # if we're close to launch, add the video URL
			vid_str = f'🔴 *Watch the launch live!*\n{launch_row[-1]}'
			launch_str = message_header + '\n\n' + vid_str + '\n\n' + info_text + '\n\n' + message_footer

		# no video provided, probably a Chinese launch
		elif notif_class in ['5m'] and launch_row[-1] == '':
			vid_str = '🔇 *No live video* available for this launch.'
			launch_str = message_header + '\n\n' + vid_str + '\n\n' + info_text + '\n\n' + message_footer

		else:
			launch_str = message_header + '\n\n' + info_text + '\n\n' + message_footer			
		
	# if it's a SpaceX launch
	else:
		if notif_class in ['24h', '12h']:
			if spx_str:
				launch_str = message_header + '\n\n' + spx_info_str + '\n\n' + info_text + '\n\n' + message_footer

		# we're close to the launch, send the video URL
		elif notif_class in ['1h', '5m'] and launch_row[-1] != '':
			vid_str = f'🔴 *Watch the launch live!*\n{launch_row[-1]}'

			if spx_str:
				launch_str = message_header + '\n\n' + spx_info_str + '\n\n' + vid_str + '\n\n' + info_text + '\n\n' + message_footer
			else:
				launch_str = message_header + '\n\n' + vid_str + '\n\n' + info_text + '\n\n' + message_footer
		
		# handle whatever fuckiness there might be with the video URLs; i.e. no URL
		else:
			if spx_str:
				launch_str = message_header + '\n\n' + spx_info_str + '\n\n' + info_text + '\n\n' + message_footer
			else:
				launch_str = message_header + '\n\n' + info_text + '\n\n' + message_footer


	# get chats to send the notification to
	# pull all with matching keyword (LSP ID), matching country code notification, or an "all" marker (and no exclusion for this ID/country)
	# Establish connection
	conn.close()
	conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	c = conn.cursor()

	# pull all where keyword = LSP or "All"
	c.execute('SELECT * FROM notify WHERE keyword == ? OR keyword == ?',(lsp, 'All'))
	query_return = c.fetchall()

	# parse output
	notify_dict, notify_list = {}, [] # chat: id: toggle
	for row in query_return:
		chat = row[0]
		if chat not in notify_dict:
			notify_dict[chat] = {}
		
		notify_dict[chat][row[1]] = row[3] # lsp: 0/1, or All: 0/1

	# if All is enabled, and lsp is disabled
	for chat, val in notify_dict.items(): # chat, dictionary (dict is in the form of LSP: toggle)
		enabled, disabled = [], []
		for l, e in val.items(): # lsp, enabled
			if e == 1:
				enabled.append(l)
			else:
				disabled.append(l)

		if lsp in disabled and 'All' in enabled:
			if debug_log:
				logging.info(f'🔕 Not notifying {chat} about {lsp} due to disabled flag. All flag was enabled.')
				try:
					logging.info(f'⚠️ notify_dict[chat]: {notify_dict[chat]} | lsp: {lsp} | enabled: {enabled} | disabled: {disabled}')
				except:
					logging.info(f'⚠️KeyError getting notify_dict[chat]. notify_dict: {notify_dict}')
		
		elif lsp in enabled or 'All' in enabled:
			notify_list.append(chat)

	if debug_log:
		logging.info(f'Sending notifications for launch {launch_id} | NET: {launch_unix} | notify_list: {notify_list}')

	for chat in notify_list:
		ret = sendNotification(chat, launch_str)

		if ret != True and debug_log:
			logging.info(f'🛑 Error sending notification to chat={chat}! Exception: {ret}')

		tries = 1
		while ret != True:
			time.sleep(2)
			ret = sendNotification(chat, launch_str)
			tries += 1
			
			if ret == True:
				if debug_log:
					logging.info(f'✅ Notification sent successfully to chat={chat}! Took {tries} tries.')

			elif ret != True and tries > 5:
				if debug_log:
					logging.info(f'⚠️ Tried to send notification to {chat} {tries} times – passing.')
					
				ret = True

	# update stats for sent notifications
	conn.close()
	updateStats({'notifications':len(notify_list), 'db_calls': 3})

	# set notification as sent; if 12 hour sent but 24 hour not sent, disable "higher" ones as well
	conn.close()
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
	c = conn.cursor()

	# notif_dict declared above
	try:
		notification_type = notif_dict[notif_class]
		c.execute(f'UPDATE launches SET {notification_type} = 1 WHERE id = ?', (launch_id,))
		if debug_log:
			logging.info(f'⏰ {t_minus} {time_format} notification flag set to 1 for launch {launch_id}. Notifications sent: {len(notify_list)}')
	except Exception as e:
		if debug_log:
			logging.info(f'''⚠️ Error disabling notification in notificationHandler().
			t_minus={t_minus}, launch_id={launch_id}. Notifications sent: {len(notify_list)}.
			Exception: {e}. Disabling all further notifications.''')

		c.execute(f'UPDATE launches SET notify24h = 1, notify12h = 1, notify60min = 1, notify5min = 1, notifylaunch = 1 WHERE id = ?', (launch_id,))

	conn.commit()
	conn.close()
	return


# updates our stats with the given input
def updateStats(input):
	# check if the db exists
	if not os.path.isfile(os.path.join('data', 'statistics.db')):
		createStatsDatabase()

	# Establish connection
	stats_conn = sqlite3.connect(os.path.join('data', 'statistics.db'))
	stats_cursor = stats_conn.cursor()

	try: # check if table exists
		stats_cursor.execute('''CREATE TABLE stats (notifications INTEGER, API_requests INTEGER, 
			db_updates INTEGER, commands INTEGER, data INTEGER, db_calls INTEGER, PRIMARY KEY (notifications, API_requests))''')
		stats_cursor.execute("INSERT INTO stats (notifications, API_requests, db_updates, commands, data, db_calls) VALUES (0, 0, 0, 0, 0, 0)")
	except sqlite3.OperationalError:
		pass

	for stat, val in input.items():
		stats_cursor.execute(f"UPDATE stats SET {stat} = {stat} + {val}")
	
	stats_conn.commit()
	stats_conn.close()
	return


# prints our stats
def statistics(msg):
	# chat to send the message to
	content_type, chat_type, chat = telepot.glance(msg, flavor='chat')

	# read stats db
	stats_conn = sqlite3.connect(os.path.join('data','statistics.db'))
	stats_cursor = stats_conn.cursor()

	# notifications INTEGER, API_requests INTEGER, db_updates INTEGER, commands INTEGER
	try: # pull stats from db
		stats_cursor.execute("SELECT * FROM stats")

		# parse returned global data
		query_return = stats_cursor.fetchall()
		if len(query_return) != 0:
			notifs = query_return[0][0]
			api_reqs = query_return[0][1]
			db_updates = query_return[0][2]
			commands = query_return[0][3]
			data = query_return[0][4]

		else:
			commands = notifs = api_reqs = db_updates = data = 0

	except sqlite3.OperationalError:
		commands = notifs = api_reqs = db_updates = data = 0

	# get system uptime
	up = uptime()
	updays = int(up/(3600*24))
	uphours = int((up-updays*3600*24)/(3600))
	upmins = int((up - updays*3600*24 - uphours*60*60)/(60))
	
	if upmins < 10:
		upmins = str(0) + str(upmins)
	else:
		upmins = str(upmins)

	# get system load average
	load_avgs = os.getloadavg() # [x, y, z]
	load_avg_str = 'Load {:.2f} {:.2f} {:.2f}'.format(load_avgs[0], load_avgs[1], load_avgs[2])
	
	if updays > 0:
		up_str = "Uptime {:d} days, {:d} h {:s} min".format(updays,uphours,upmins)
	else:
		up_str = "Uptime {:d} hours {:s} min".format(uphours,upmins)

	# format data to MB or GB
	if data / 10**9 >= 1:
		data, data_size_class = data/10**9, 'GB'
	else:
		data, data_size_class = data/10**6, 'MB'

	# get database sizes
	# get chainStore.db file size
	try:
		db_sizes = os.path.getsize(os.path.join('data','launch','launches.db'))
		db_sizes += os.path.getsize(os.path.join('data','launch','spx-launches.db'))
		db_sizes += os.path.getsize(os.path.join('data','launch','notifications.db'))
		db_sizes += os.path.getsize(os.path.join('data','statistics.db'))
	except:
		db_sizes = 0.00

	if db_sizes / 10**9 >= 1:
		db_sizes, db_size_class = db_sizes/10**9, 'GB'
	else:
		db_sizes, db_size_class = db_sizes/10**6, 'MB'

	# pull amount of unique recipients from the notifications database
	conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
	c = conn.cursor()

	c.execute('SELECT * FROM notify WHERE enabled = 1')
	query_return = c.fetchall()

	recipients = []
	for row in query_return:
		if row[0] not in recipients:
			recipients.append(row[0])


	reply_str = f'''
	🚀 *LaunchBot version {version}*
	Notifications delivered: {notifs}
	Notification recipients: {len(recipients)}
	Commands parsed: {commands}

	🛰 *Network statistics*
	Data transferred: {data:.2f} {data_size_class}
	API requests made: {api_reqs}

	💾 *Database statistics*
	Database updates: {db_updates}
	Storage used: {db_sizes:.2f} {db_size_class}

	🎛 *Server information*
	{up_str}
	{load_avg_str}
	'''

	bot.sendMessage(chat, inspect.cleandoc(reply_str), parse_mode='Markdown')
	updateStats({'db_calls':2})
	return


# creates the spx database
def createSPXDatabase():
	launch_dir = 'data/launch'
	if not os.path.isdir(launch_dir):
		if not os.path.isdir('data'):
			os.mkdir('data')

		os.mkdir('data/launch')

	# Establish connection
	conn = sqlite3.connect(os.path.join(launch_dir, 'spx-launches.db'))
	c = conn.cursor()

	try:
		c.execute(
			'''CREATE TABLE launches
			(flight_num INTEGER, launch_name TEXT, NET INTEGER, orbit TEXT,
			vehicle TEXT, core_serials TEXT, core_reuses TEXT, landing_intents TEXT,
			fairing_reused TEXT, fairing_rec_attempt INT, fairing_ship TEXT,
			PRIMARY KEY (launch_name))''')
		
		c.execute("CREATE INDEX keywordtminus ON launches (launch_name, NET)")
	
	except sqlite3.OperationalError as e:
		if debug_log:
			logging.info(f'🛑 Error in createSPXDatabase: {e}')

	conn.commit()
	conn.close()
	return


# creates a new notifications database, if one doesn't exist
def createNotifyDatabase():
	launch_dir = 'data/launch'
	if not os.path.isdir(launch_dir):
		if not os.path.isdir('data'):
			os.mkdir('data')

		os.mkdir('data/launch')

	# Establish connection
	conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	c = conn.cursor()

	try:
		# chat ID - keyword - UNIX timestamp - enabled true/false
		c.execute("CREATE TABLE notify (chat TEXT, keyword TEXT, lastnotified INTEGER, enabled INTEGER, PRIMARY KEY (chat, keyword))")
		c.execute("CREATE INDEX enabledchats ON notify (chat, enabled)")
	except sqlite3.OperationalError:
		pass

	conn.commit()
	conn.close()
	return


# creates a launch database
def createLaunchDatabase():
	launch_dir = 'data/launch'
	if not os.path.isdir(launch_dir):
		if not os.path.isdir('data'):
			os.mkdir('data')

		os.mkdir('data/launch')

	# Establish connection
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
	c = conn.cursor()

	try:
		# launch, id, keywords, lsp_name, vehicle, pad, info, countrycode, NET, Tminus
		# notify24h, notify12h, notify60min, notify5min, notifylaunch, success, launched, hold
		c.execute(
			'''CREATE TABLE launches
			(launch TEXT, id INTEGER, keywords TEXT, lsp_name TEXT, lsp_short TEXT, vehicle TEXT, pad TEXT, info TEXT,
			countrycode TEXT, NET INTEGER, Tminus INTEGER, notify24h BOOLEAN, notify12h BOOLEAN,
			notify60min BOOLEAN, notify5min BOOLEAN, notifylaunch BOOLEAN,
			success BOOLEAN, launched BOOLEAN, hold BOOLEAN, vid TEXT,
			PRIMARY KEY (id))''')
		
		c.execute("CREATE INDEX keywordtminus ON launches (id, NET)")
	
	except sqlite3.OperationalError as e:
		if debug_log:
			logging.info(f'Error in createLaunchDatabase: {e}')
		pass

	conn.commit()
	conn.close()
	return


# creates a statistics database
def createStatsDatabase():
	data_dir = 'data'
	if not os.path.isdir('data'):
		os.mkdir('data')

	# Establish connection
	conn = sqlite3.connect(os.path.join(data_dir, 'statistics.db'))
	c = conn.cursor()

	try:
		# chat ID - keyword - UNIX timestamp - enabled true/false
		c.execute('''CREATE TABLE stats (notifications INTEGER, API_requests INTEGER, 
			db_updates INTEGER, commands INTEGER, data INTEGER, db_calls INTEGER, PRIMARY KEY (notifications, API_requests))''')
		c.execute("INSERT INTO stats (notifications, API_requests, db_updates, commands, data, db_calls) VALUES (0, 0, 0, 0, 0, 0)")
	except sqlite3.OperationalError:
		pass

	conn.commit()
	conn.close()
	return


# if running for the first time
def firstRun():
	print("Looks like you're running launchbot for the first time")
	print("Let's start off by creating some folders.")
	time.sleep(2)
	
	# create /data and /chats
	if not os.path.isdir('data'):
		os.mkdir('data')
		print("Folders created!\n")

	time.sleep(1)

	print('To function, launchbot needs a bot API key;')
	print('to get one, send a message to @botfather on Telegram.')

	# create a settings file for the bot; we'll store the API keys here
	if not os.path.isfile('data' + '/bot-settings.json'):
		if not os.path.isdir('data'):
			os.mkdir('data')

		updateToken(['botToken'])
		time.sleep(2)
		print('\n')


# update bot token
def updateToken(update_tokens):
	# create /data and /chats
	if not os.path.isdir('data'):
		firstRun()

	if not os.path.isfile('data' + '/bot-settings.json'):
		with open('data/bot-settings.json', 'w') as json_data:
			setting_map = {} # empty .json file
	else:
		with open('data' + '/bot-settings.json', 'r') as json_data:
				setting_map = json.load(json_data) # use old .json

	if 'botToken' in update_tokens:
		token_input = str(input('Enter the bot token for launchBot: '))
		while ':' not in token_input:
			print('Please try again – bot-tokens look like "123456789:ABHMeJViB0RHL..."')
			token_input = str(input('Enter the bot token for launchbot: '))

		setting_map['botToken'] = token_input

	with open('data' + '/bot-settings.json', 'w') as json_data:
		json.dump(setting_map, json_data, indent=4)

	time.sleep(2)
	print('Token update successful!\n')


# main
def main():
	# some global vars for use in other functions
	global TOKEN, bot, version, bot_ID, bot_username
	global debug_log, debug_mode

	# current version
	version = '0.2.3 beta'

	# default
	start = False
	debug_log = False
	debug_mode = False

	# list of args the program accepts
	start_args = ['start', '-start']
	debug_args = ['log', '-log', 'debug', '-debug']
	bot_token_args = ['newbottoken', '-newbottoken']

	if len(sys.argv) == 1:
		print('Give at least one of the following arguments:')
		print('\tlaunchbot.py [-start, -newBotToken, -log]\n')
		print('E.g.: python3 launchbot.py -start')
		print('\t-start starts the bot')
		print('\t-newBotToken changes the bot API token')
		print('\t-log stores some logs\n')
		sys.exit('Program stopping...')

	else:
		update_tokens = []
		for arg in sys.argv:
			arg = arg.lower()

			if arg in start_args:
				start = True

			# update tokens if instructed to
			if arg in bot_token_args:
				update_tokens.append('botToken')
			if arg in debug_args:
				if arg == 'log' or arg == '-log':
					debug_log = True
					if not os.path.isdir('data'):
						firstRun()
					
					log = 'data/log.log'

					# disable logging for urllib and requests because jesus fuck they make a lot of spam
					logging.getLogger('requests').setLevel(logging.CRITICAL)
					logging.getLogger('urllib3').setLevel(logging.CRITICAL)
					logging.getLogger('schedule').setLevel(logging.CRITICAL)
					logging.getLogger('chardet.charsetprober').setLevel(logging.CRITICAL)
					logging.getLogger('telepot.exception.TelegramError').setLevel(logging.CRITICAL)

					# start log
					logging.basicConfig(filename=log,level=logging.DEBUG,format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')
					#logging.info('🤖 Bot started')

				if arg == 'debug' or arg == '-debug':
					debug_mode = True


		if len(update_tokens) != 0:
			updateToken(update_tokens)

		if start is False:
			sys.exit('No start command given – exiting. To start the bot, include -start in startup options.')

	# if data folder isn't found, we haven't run before (or someone pressed the wrong button)
	if not os.path.isdir('data'):
		firstRun()

	try:
		bot_settings_path = os.path.join('data','bot-settings.json')
		with open(bot_settings_path, 'r') as json_data:
			setting_map = json.load(json_data)

	except FileNotFoundError:
		firstRun()

		with open(bot_settings_path, 'r') as json_data:
			setting_map = json.load(json_data)

	# token for the Telegram API; get from args or as a text file
	if len(setting_map['botToken']) == 0 or ':' not in setting_map['botToken']:
		firstRun()
	else:
		TOKEN = setting_map['botToken']

	# create the bot
	bot = telepot.Bot(TOKEN)

	# handle ssl exceptions
	ssl._create_default_https_context = ssl._create_unverified_context

	# get the bot's username and id
	bot_specs = bot.getMe()
	bot_username = bot_specs['username']
	bot_ID = bot_specs['id']

	# valid commands we monitor for
	global valid_commands, valid_commands_alt
	
	valid_commands = [
	'/start', '/help', 
	'/next', '/notify',
	'/statistics', '/schedule'
	]

	# generate the "alternate" commands we listen for, as in ones suffixed with the bot's username 
	valid_commands_alt = []
	for command in valid_commands:
		valid_commands_alt.append(command + '@' + bot_username)

	MessageLoop(bot, {'chat': handle, 'callback_query': callbackHandler}).run_as_thread()
	time.sleep(1)

	if not debug_mode:
		print('| LaunchBot.py v{:s}'.format(version))
		print("| Don't close this window or set the computer to sleep. Quit: ctrl + c.")
		time.sleep(0.5)

		status_msg = f'  Connected to Telegram! ✅'
		sys.stdout.write('%s\r' % status_msg)

	#if debug_log:
	#	logging.info('✅ Bot connected')

	# schedule regular database updates and NET checks
	schedule.every(10).minutes.do(getLaunchUpdates, launch_ID=None)
	schedule.every(10).minutes.do(spxAPIHandler)
	schedule.every(30).seconds.do(launchUpdateCheck)
	
	# run both scheduled jobs now, so we don't have to sit in the dark for a while
	getLaunchUpdates(None)
	launchUpdateCheck()
	spxAPIHandler()

	# fancy prints so the user can tell that we're actually doing something
	if not debug_mode:
		cursor.hide()
		print_map = {0: '|', 1: '/', 2: '—', 3: '\\', 4: '|', 5: '/', 6: '—', 7: '\\'}
		while True:
			schedule.run_pending()
			for i in range(0,8):
				print_char = print_map[i]
				sys.stdout.write('%s\r' % print_char)
				sys.stdout.flush()
				time.sleep(1)

	else:
		while True:
			schedule.run_pending()
			time.sleep(1)


main()








