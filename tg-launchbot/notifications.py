import os
import time
import datetime
import sqlite3
import logging
import inspect

import ujson as json

from db import create_chats_db

from utils import (
	short_monospaced_text, map_country_code_to_flag, reconstruct_link_for_markdown,
	reconstruct_message_for_markdown, time_delta_to_legible_eta, anonymize_id)

from apscheduler.schedulers.background import BackgroundScheduler
from telegram import ReplyKeyboardMarkup, ReplyKeyboardRemove
from telegram import InlineKeyboardButton, InlineKeyboardMarkup
from telegram.ext import Updater, CommandHandler, MessageHandler, Filters, ConversationHandler
from telegram.ext import CallbackQueryHandler

# handle sending of postpone notifications; done in a separate function so we can retry more easily and handle exceptions
def send_postpone_notification(chat, notification, launch_id, keywords):
	try:
		# load mute status, generate keys
		mute_status = load_mute_status(chat, launch_id, keywords)
		mute_press = 0 if mute_status == 1 else 1
		mute_key = {0:f'🔇 Mute this launch',1:'🔊 Unmute this launch'}[mute_status]

		# /mute/$provider/$launch_id/(0/1) | 1=muted (true), 0=not muted
		keyboard = InlineKeyboardMarkup(
			inline_keyboard = [[
					InlineKeyboardButton(text=mute_key, callback_data=f'mute/{keywords}/{launch_id}/{mute_press}')
			]]
		)

		sent_msg = bot.sendMessage(
			chat, notification, parse_mode='MarkdownV2', reply_markup=keyboard, disable_notification=False)

		# sent message is stored in sent_msg; store in db so we can edit messages
		msg_identifier = f"{sent_msg['chat']['id']}:{sent_msg['message_id']}"
		msg_identifiers.append(f'{msg_identifier}')
		return True

	except telegram.exception.BotWasBlockedError:
		if debug_log:
			logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat)} – cleaning notify database...')

		clean_notify_database(chat)
		return True

	except telegram.exception.TelegramError as error:
		# Bad Request: chat not found
		if error.error_code == 400 and 'not found' in error.description:
			if debug_log:
				logging.exception(f'⚠️ Chat {anonymize_id(chat)} not found – cleaning notify database... Error: {error}')

			clean_notify_database(chat)
			return True

		elif error.error_code == 403:
			if 'user is deactivated' in error.description:
				if debug_log:
					logging.exception(f'⚠️ User {anonymize_id(chat)} was deactivated – cleaning notify database... Error: {error}')

				clean_notify_database(chat)
				return True

			elif 'bot was kicked from the supergroup chat' in error.description:
				if debug_log:
					logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database... Error: {error}')

				clean_notify_database(chat)
				return True

			elif 'Forbidden: bot is not a member of the supergroup chat' in error.description:
				if debug_log:
					logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database... Error: {error}')

				clean_notify_database(chat)
				return True

			else:
				if debug_log:
					logging.exception(f'⚠️ Unhandled 403 telegram.exception.TelegramError in send_postpone_notification: {error}')

		# Rate-limited by Telegram (https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this)
		elif error.error_code == 429:
			if debug_log:
				logging.exception(f'🚧 Rate-limited (429) - sleeping for 5 seconds and continuing. Error: {error}')

			time.sleep(5)
			return False

		# Some error code we don't know how to handle
		else:
			if debug_log:
				logging.exception(f'⚠️ Unhandled telegram.exception.TelegramError in send_notification: {error}')

			return False

	except Exception as caught_exception:
		return caught_exception


def get_user_notifications_status(
	db_dir: str, chat: str, provider_set: set, provider_name_map: dict) -> dict:
	'''
	The function takes a list of provider strings as input, and returns a dict containing
	the notification status for all providers.
	'''
	# Establish connection
	conn = sqlite3.connect(os.path.join(db_dir, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# verify table exists
	cursor.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'chats'))
	if len(cursor.fetchall()) == 0:
		logging.warning("⚠️ Chats table doesn't exists: creating...")
		create_chats_db(db_path=db_dir, cursor=cursor)

		conn.commit()
		logging.info('✅ Chats table created!')

	# select the field for our chat, convert to a dict, close conn
	cursor.execute("SELECT * FROM chats WHERE chat = ?", (chat,))
	query_return = [dict(row) for row in cursor.fetchall()]
	conn.close()

	# dict for storing the status of notifications, init with "All".
	notification_statuses = {'All': 0}

	# iterate over all providers supported by LaunchBot
	for provider in provider_set:
		''' check if this provider name is mapped to another name
		in provider_name_map -> use that one instead '''
		if provider in provider_name_map.keys():
			provider = provider_name_map[provider]

		# set default notification_status to 0
		notification_statuses[provider] = 0

	# if chat doesn't exist or return is 0, return the zeroed dict
	if len(query_return) == 0:
		return notification_statuses

	# keep track of the all_flag, init to false
	all_flag = False

	# there should only be one row
	chat_row = query_return[0]

	# enabled, disabled states: parse comma-separated entries into lists
	enabled_notifs = chat_row['enabled_notifications'].split(',')
	disabled_notifs= chat_row['disabled_notifications'].split(',')

	# iterate over enabled lsp notifications
	for enabled_lsp in enabled_notifs:
		if enabled_lsp != '':
			notification_statuses[enabled_lsp] = 1
			if enabled_lsp == 'All':
				all_flag = True

	# iterate over disabled lsp notifications
	for disabled_lsp in disabled_notifs:
		if disabled_lsp != '':
			notification_statuses[disabled_lsp] = 0
			if disabled_lsp == 'All':
				all_flag = False

	if 'All' not in notification_statuses:
		notification_statuses['All'] = all_flag

	return notification_statuses


# toggle a notification for chat of type (toggle_type, keyword) with the value keyword
def toggle_notification(
	data_dir: str, chat: str, toggle_type: str, keyword: str,
	toggle_to_state: int, provider_by_cc: dict, provider_name_map: dict):
	'''
	Toggle a notification to the toggle_to_state state (if keyword is all or a cc),
	otherwise determine the new toggle state ourselves.

	data_dir (int): data root dir
	chat (str): chat
	toggle_type (str): the type of notification class to toggle
	'''
	# Establish connection
	conn = sqlite3.connect(os.path.join(data_dir, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# if toggle type is a country code, map the ccode to a list of providers
	if toggle_type == 'country_code':
		provider_list = set(provider_by_cc[keyword])
		provider_list_mod = set()
		for key in provider_list:
			if key in provider_name_map.keys():
				provider_list_mod.add(provider_name_map[key])
			else:
				provider_list_mod.add(key)

		provider_list = provider_list_mod

	elif toggle_type == 'lsp':
		if keyword in provider_name_map.keys():
			keyword = provider_name_map[keyword]

		provider_list = {keyword}

	elif toggle_type == 'all':
		provider_list = {'All'}
		provider_list_mod = {'All'}
		for val in provider_by_cc.values():
			for provider in val:
				if provider in provider_name_map.keys():
					provider_list_mod.add(provider_name_map[provider])
				else:
					provider_list_mod.add(provider)

		provider_list = provider_list_mod

	''' Do string operations so we can update the notification states.
	Basically, we have a new toggle_state that indicated whether the new
	state is enabled or disabled. Before we can proceed, pull the current
	notification states. '''

	cursor.execute('SELECT * FROM chats WHERE chat = ?', (chat,))
	query_return = [dict(row) for row in cursor.fetchall()]
	data_exists = bool(len(query_return) != 0)

	# pull existing strs, split
	if data_exists:
		old_enabled_states = query_return[0]['enabled_notifications'].split(',')
		old_disabled_states = query_return[0]['disabled_notifications'].split(',')

	# merge enabled and disabled states into one dict of kw:bool
	old_states = {}
	if data_exists:
		for enabled in old_enabled_states:
			old_states[enabled] = 1

		for disabled in old_disabled_states:
			old_states[disabled] = 0

	# keep old_states intact, edit new_states (needed?)
	new_states = old_states

	# iterate over the keywords (lsp names, country code, or simply "All") we'll be toggling
	if toggle_type == 'lsp':
		''' If a launch service provider, there's only one keyword we're toggling: should be simple.
		Do note, however, that in the case of a LSP we need to figure out the new state ourselves. '''
		if keyword in old_states:
			# toggle to 1 if previous state is 0: else, toggle to 0
			new_states[keyword] = 1 if old_states[keyword] == 0 else 0
		else:
			new_states[keyword] = 1

		# new_status for return statement
		new_status = new_states[keyword]

	elif toggle_type in ('all', 'country_code'):
		# if 'all' or 'country_code', iterate over provider_list (the ready list of keywords)
		for provider in provider_list:
			new_states[provider] = toggle_to_state

	# we should now have our new notification states: construct strings based on toggle state
	new_enabled_notifications = set()
	new_disabled_notifications = set()
	for notification, state in new_states.items():
		if state == 1:
			new_enabled_notifications.add(notification)
		else:
			new_disabled_notifications.add(notification)

	# construct strings for insert
	new_enabled_str = ','.join(new_enabled_notifications)
	new_disabled_str = ','.join(new_disabled_notifications)

	if len(new_enabled_str) > 0:
		if new_enabled_str[0] == ',':
			new_enabled_str = new_enabled_str[1::]

	if len(new_disabled_str) > 0:
		if new_disabled_str[0] == ',':
			new_disabled_str = new_disabled_str[1::]

	try:
		if data_exists:
			cursor.execute('''UPDATE chats SET enabled_notifications = ?, disabled_notifications = ?
				WHERE chat = ?''', (new_enabled_str, new_disabled_str, chat))
		else:
			cursor.execute('''INSERT INTO chats (chat, subscribed_since, time_zone, time_zone_str,
				command_permissions, postpone_notify, notify_time_pref, enabled_notifications, 
				disabled_notifications) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)''',
				(chat, int(time.time()), None, None, None, None, '1,1,1,1', new_enabled_str, new_disabled_str))
	except sqlite3.IntegrityError:
		# already found: simply update the db row
		cursor.execute('''UPDATE chats SET enabled_notifications = ?, disabled_notifications = ?
				WHERE chat = ?''', (new_enabled_str, new_disabled_str, chat))

	conn.commit()
	conn.close()

	if toggle_type == 'lsp':
		return new_status

	return toggle_to_state


def update_notif_preference(db_path: str, chat: str, notification_type: str) -> int:
	'''
	db_path (str): main data dir path
	chat (str): chat to update preferences for
	notification_type (str): one of ('24h', '12h', '1h', '5m')
	'''
	# get current status: convert to a list so it's editable
	old_preferences = list(get_notif_preference(db_path, chat))

	# map notification_type to a corresponding index in old_preferences
	update_index = {'24h': 0, '12h': 1, '1h': 2, '5m': 3}[notification_type]
	new_state = 1 if old_preferences[update_index] == 0 else 0

	old_preferences[update_index] = new_state
	new_preferences = ','.join(str(val) for val in old_preferences)

	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	''' chats table:
	chat TEXT 				subscribed_since INT 			time_zone TEXT
	time_zone_str TEXT 		command_permissions TEXT 		postpone_notify BOOLEAN
	notify_time_pref TEXT 	enabled_notifications TEXT 		disabled_notifications TEXT
	'''
	try:
		cursor.execute('''INSERT INTO chats
			(chat, subscribed_since, time_zone, time_zone_str, command_permissions, postpone_notify,
			notify_time_pref, enabled_notifications, disabled_notifications) VALUES (?,?,?,?,?,?,?,?,?)''',
			(chat, int(time.time()), None, None, None, None, new_preferences, None, None))
	except sqlite3.IntegrityError:
		cursor.execute("UPDATE chats SET notify_time_pref = ? WHERE chat = ?", (new_preferences, chat))

	conn.commit()
	conn.close()

	toggle_state_text = 'enabled (🔔)' if new_state == 1 else 'disabled (🔕)'
	logging.info(f'📩 {anonymize_id(chat)} {toggle_state_text} {notification_type} notification')

	return new_state


def get_notif_preference(db_path: str, chat: str) -> tuple:
	'''
	Returns the notification preferences (24h, 12h, 1h, 5m) as a tuple of boolean values
	'''
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	cursor.execute("SELECT notify_time_pref FROM chats WHERE chat = ?",(chat,))
	query_return = cursor.fetchall()
	conn.close()

	if len(query_return) == 0:
		return (1, 1, 1, 1)

	notif_preferences = query_return[0][0].split(',')

	return (
		int(notif_preferences[0]), int(notif_preferences[1]),
		int(notif_preferences[2]), int(notif_preferences[3]))


def toggle_launch_mute(chat, launch_provider, launch_id, toggle):
	data_dir = 'data'
	if not os.path.isfile(os.path.join(data_dir,'launchbot-data.db')):
		create_notify_database()

	try:
		int(launch_provider)
		logging.info(f'⚠️ Integer launch_provider value provided to toggle_launch_mute! \
			launch_provider={launch_provider}, launch_id={launch_id}, toggle={toggle}')
		launch_provider = name_from_provider_id(launch_provider)
		logging.info(f'⚙️ Related integer value to provider name: {launch_provider}')
	except:
		pass

	# get mute status
	conn = sqlite3.connect(os.path.join(data_dir,'launchbot-data.db'))
	c = conn.cursor()

	# pull the current muted_launches field
	c.execute("SELECT muted_launches FROM notify WHERE chat = ? AND keyword = ?", (chat, launch_provider))
	query_return = c.fetchall()

	# mute
	if toggle == '1':
		if len(query_return) == 0:
			new_mute_string = str(launch_id)
		else:
			if query_return[0][0] is None:
				new_mute_string = str(launch_id)

			elif query_return[0][0] != '':
				if launch_id in query_return[0][0].split(','):
					new_mute_string = query_return[0][0]
				else:
					new_mute_string = f'{query_return[0][0]},{launch_id}'
			else:
				new_mute_string = f'{launch_id}'

			new_mute_string = new_mute_string.replace(f'None,', '')

	# unmute
	elif toggle == '0':
		new_mute_string = ''
		if len(query_return) == 0:
			pass
		else:
			mute_string = query_return[0][0]
			if mute_string is None:
				new_mute_string = str(launch_id)
			elif f'{launch_id},' in mute_string:
				new_mute_string = mute_string.replace(f'{launch_id},', '')
			elif f',{launch_id}' in mute_string:
				new_mute_string = mute_string.replace(f',{launch_id}', '')
			else:
				new_mute_string = mute_string.replace(f'{launch_id}', '')

			new_mute_string = new_mute_string.replace(f'None,', '')

	if len(query_return) == 0:
		c.execute("INSERT INTO notify (chat, keyword, muted_launches, enabled) VALUES (?, ?, ?, ?)", (chat, launch_provider, new_mute_string, 1))
	else:
		c.execute("UPDATE notify SET muted_launches = ? WHERE chat = ? AND keyword = ?", (new_mute_string, chat, launch_provider))

	conn.commit()
	conn.close()


# load mute status for chat and launch
def load_mute_status(chat: str, launch_id: str, lsp_name: str):
	data_dir = 'data'
	conn = sqlite3.connect(os.path.join(data_dir,'launchbot-data.db'))
	c = conn.cursor()

	# pull launch mute status for chat
	c.execute("SELECT muted_launches FROM notify WHERE chat = ? AND keyword = ?", (chat, lsp_name))
	query_return = c.fetchall()

	if len(query_return) == 0:
		mute_status = 0
	else:
		if query_return[0][0] is None:
			mute_status = 0
		elif str(launch_id) in query_return[0][0].split(','):
			mute_status = 1
		else:
			mute_status = 0

	return mute_status


# removes all notification settings for a chat from the notify database
def clean_notify_database(chat):
	conn = sqlite3.connect(os.path.join('data', 'launchbot-data.db'))
	c = conn.cursor()

	c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
	conn.commit()
	conn.close()


def remove_previous_notification(launch_id, keyword):
	''' Before storing the new identifiers, remove the old notification if possible. '''
	data_dir = 'data'
	if not os.path.isfile(os.path.join(data_dir, 'launchbot-data.db')):
		return

	conn = sqlite3.connect(os.path.join(data_dir, 'launchbot-data.db'))
	c = conn.cursor()

	c.execute("SELECT msg_identifiers FROM sent_notification_identifiers WHERE id = ?", (launch_id,))
	query_return = c.fetchall()

	if len(query_return) == 0:
		if debug_log:
			logging.info(f'No notifications to remove for launch {launch_id}')
		return

	if len(query_return) > 1:
		if debug_log:
			logging.info(f'⚠️ Error getting launch_id! Got {len(query_return)} launches. Ret: {query_return}')
		return

	identifiers, success_count, muted_count = query_return[0][0].split(','), 0, 0
	for id_pair in identifiers:
		id_pair = id_pair.split(':')
		
		try:
			chat_id, message_id = id_pair[0], id_pair[1]
		except: # throws an error if nothing to remove (i.e. empty db)
			return

		message_identifier = (chat_id, message_id)

		# try removing the message, if launch has not been muted
		if load_mute_status(chat_id, launch_id, keyword) == 0:
			try:
				ret = bot.deleteMessage(message_identifier)
				if ret is not False:
					success_count += 1
			except Exception as error:
				if debug_log and error.error_code != 400:
					logging.exception(f'⚠️ Unable to delete previous notification. Unique ID: {message_identifier}.'
								 f'Got error: {error}')
		else:
			muted_count += 1
			if debug_log:
				logging.info(f'🔍 Not removing previous notification due to mute status for chat={anonymize_id(chat_id)}')

	if debug_log:
		logging.info(f'✅ Successfully removed {success_count} previously sent notifications! {muted_count} avoided due to mute status.')


# gets a request to send a notification about launch X from launch_update_check()
def notification_handler_old(db_path: str, launch_unique_id: str, bot: 'telegram.bot.Bot'):
	# handle notification sending; done in a separate function so we can retry more easily and handle exceptions
	def send_notification(chat, notification, launch_id, keywords, vid_link, notif_class):
		# send early notifications silently
		silent = True if notif_class not in ('1h', '5m') else False

		# parse the message text for MarkdownV2
		notification = reconstruct_message_for_markdown(notification)
		if 'LinkTextGoesHere' in notification:
			link_text = reconstruct_link_for_markdown(vid_link)
			notification = notification.replace('LinkTextGoesHere', f'[live\!]({link_text})')

		try:
			# load mute status, generate keys
			mute_status = load_mute_status(chat, launch_id, keywords)
			mute_press = 0 if mute_status == 1 else 1
			mute_key = {0:f'🔇 Mute this launch',1:'🔊 Unmute this launch'}[mute_status]

			# /mute/$provider/$launch_id/(0/1) | 1=muted (true), 0=not muted
			keyboard = InlineKeyboardMarkup(
				inline_keyboard = [[
						InlineKeyboardButton(
							text=mute_key, callback_data=f'mute/{keywords}/{launch_id}/{mute_press}')]])

			sent_msg = bot.sendMessage(
				chat, notification, parse_mode='MarkdownV2',
				reply_markup=keyboard, disable_notification=silent)

			# sent message is stored in sent_msg; store in db so we can edit messages
			msg_identifier = f'{sent_msg["chat"]["id"]}:{sent_msg["message_id"]}'
			msg_identifiers.append(str(msg_identifier))
			return True
		
		except telegram.exception.BotWasBlockedError:
			if debug_log:
				logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat)} – cleaning notify database...')

			clean_notify_database(chat)
			return True

		except telegram.exception.TelegramError as error:
			# Bad Request: chat not found
			if error.error_code == 400 and 'not found' in error.description:
				if debug_log:
					logging.exception(f'⚠️ Chat {anonymize_id(chat)} not found – cleaning notify database... Error: {error}')

				clean_notify_database(chat)
				return True

			elif error.error_code == 403:
				if 'user is deactivated' in error.description:
					if debug_log:
						logging.exception(f'⚠️ User {anonymize_id(chat)} was deactivated – cleaning notify database... Error: {error}')

					clean_notify_database(chat)
					return True

				elif 'bot was kicked from the supergroup chat' in error.description:
					if debug_log:
						logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database... Error: {error}')

					clean_notify_database(chat)
					return True

				elif 'Forbidden: bot is not a member of the supergroup chat' in error.description:
					if debug_log:
						logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database... Error: {error}')

					clean_notify_database(chat)
					return True

				else:
					if debug_log:
						logging.exception(f'⚠️ Unhandled 403 telegram.exception.TelegramError in send_notification: {error}')

			# Rate limited by Telegram (https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this)
			elif error.error_code == 429:
				if debug_log:
					logging.exception(f'🚧 Rate-limited (429) - sleeping for 5 seconds and continuing. Error: {error}')

				time.sleep(5)
				return False

			# Something else
			else:
				if debug_log:
					logging.exception(f'⚠️ Unhandled telegram.exception.TelegramError in send_notification: {error}')

				return False

	# TODO pull launch info

	launch_id = launch_row[1]
	keywords = int(launch_row[2])

	# check if LSP ID in keywords is in our custom list, so we can get the short name and the flag
	if keywords not in LSP_IDs.keys():
		lsp, lsp_flag = None, ''
	else:
		lsp = LSP_IDs[keywords][0]
		lsp_flag = LSP_IDs[keywords][1]

	# pull launch information from database
	data_dir = 'data'
	conn = sqlite3.connect(os.path.join(data_dir, 'launchbot-data.db'))
	c = conn.cursor()

	# select the launch we're tracking
	c.execute(f'''SELECT * FROM launches WHERE id = {launch_id}''')
	query_return = c.fetchall()

	# parse the input so we can generate the message later
	launch_name = query_return[0][0].strip()
	lsp_short = query_return[0][4]
	vehicle = query_return[0][5]
	pad = query_return[0][6]
	info = query_return[0][7]

	# parse pad to convert common names to shorter ones
	if 'LC-' not in pad:
		pad = pad.replace('Space Launch Complex ', 'SLC-').replace('Launch Complex ', 'LC-')

	if info is not None:
		# if the info text is longer than 60 words, pick the first three sentences.
		if len(info.split(' ')) > 60:
			info = f'{". ".join(info.split(". ")[0:2])}.'

		if 'DM2' in launch_name:
			info = 'A new era of human spaceflight is set to begin as 🇺🇸-astronauts once again launch to orbit on a 🇺🇸-rocket from 🇺🇸-soil, almost a decade after the retirement of the Space Shuttle fleet in 2011.'
			launch_name = 'SpX-DM2'

		info_text = f'ℹ️ {info}'
	else:
		info_text = f'ℹ️ No launch information available'

	if lsp is None:
		lsp = query_return[0][3]
		lsp_short = query_return[0][4]

	# launch time as a unix time stamp
	utc_timestamp = query_return[0][9]

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
					logging.exception(f'\t🛑 Error disabling notification: {e}')

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
	lsp_name = lsp_short if len(lsp) > len('Virgin Orbit') else lsp

	# if it's a SpaceX launch, pull get the info string
	if lsp_name == 'SpaceX':
		if debug_log:
			logging.info(f'Notifying of a SpX launch. Calling spx_info_str_gen with ({launch_name}, 0, {utc_timestamp})')

		spx_info_str, spx_orbit_info = spx_info_str_gen(launch_name, 0, utc_timestamp)
		if spx_info_str is not None:
			spx_str = True
			if debug_log:
				logging.info('Got a SpX str!')
		else:
			spx_str = False
			if debug_log:
				logging.info('Got None from SpX str gen.')


	# do some string magic to reduce the space width of monospaced text in the telegram message
	lsp_str = ' '.join("`{}`".format(word) for word in lsp_name.split(' '))
	vehicle_name = ' '.join("`{}`".format(word) for word in vehicle.split(' '))
	pad_name = ' '.join("`{}`".format(word) for word in pad.split(' '))

	if 'DM2' in launch_name:
		launch_name = 'SpX-DM2'
		if time_format == 'minutes':
			info_text += ' Godspeed Behnken & Hurley.'

	

	# add the footer
	message_footer += f'*🕓 The launch is scheduled* for LAUNCHTIMEHERE\n'
	message_footer += f'*🔕 To disable* use /notify@{BOT_USERNAME}'
	launch_str = message_header + '\n\n' + info_text + '\n\n' + message_footer

	# if NOT a SpaceX launch and we're close to launch, add the video URL
	if lsp_name != 'SpaceX':
		# a different kind of message for 60m and 5m messages, which contain the video url (if one is available)
		if notif_class in ('1h', '5m') and launch_row[19] != '': # if we're close to launch, add the video URL
			vid_str = f'🔴 *Watch the launch* LinkTextGoesHere'
			launch_str = message_header + '\n\n' + info_text + '\n\n' + vid_str + '\n' + message_footer

		# no video provided, probably a Chinese launch
		elif notif_class == '5m' and launch_row[19] == '':
			vid_str = '🔇 *No live video* available.'
			launch_str = message_header + '\n\n' + info_text + '\n\n' + vid_str + '\n' + message_footer

		else:
			launch_str = message_header + '\n\n' + info_text + '\n\n' + message_footer			
		
	# if it's a SpaceX launch
	else:
		if notif_class in ('24h', '12h'):
			if spx_str:
				launch_str = message_header + '\n\n' + spx_info_str + '\n\n' + info_text + '\n\n' + message_footer

		# we're close to the launch, send the video URL
		elif notif_class in ('1h', '5m') and launch_row[19] != '':
			vid_str = f'🔴 *Watch the launch* LinkTextGoesHere'

			if spx_str:
				launch_str = message_header + '\n\n' + spx_info_str + '\n\n' + info_text + '\n\n' + vid_str + '\n' + message_footer
			else:
				launch_str = message_header + '\n\n' + info_text + '\n\n' + vid_str + '\n' + message_footer
		
		# handle whatever fuckiness there might be with the video URLs; i.e. no URL
		else:
			if spx_str:
				launch_str = message_header + '\n\n' + spx_info_str + '\n\n' + info_text + '\n\n' + message_footer
			else:
				launch_str = message_header + '\n\n' + info_text + '\n\n' + message_footer


	# get chats to send the notification to
	notify_list = get_notify_list(lsp, launch_id, notif_class)

	if debug_log:
		launch_unix = datetime.datetime.utcfromtimestamp(utc_timestamp)
		logging.info(f'Sending notifications for launch {launch_id} | NET: {launch_unix}')

	# send early notifications silently
	if debug_log:
		if notif_class not in ('1h', '5m'):
			logging.info('🔈 Sending notification silently...')
		else:
			logging.info('🔊 Sending notification with sound')

	# use proper lsp name
	if len(launch_row[3]) > len('Virgin Orbit'):
		cmd_keyword = lsp_short
	else:
		cmd_keyword = launch_row[3]

	global msg_identifiers
	reached_people, start_time, msg_identifiers = 0, timer(), []
	for chat in notify_list:
		# generate unique time for each chat
		utc_offset = 3600 * load_time_zone_status(chat, readable=False)
		local_timestamp = utc_timestamp + utc_offset
		launch_unix = datetime.datetime.utcfromtimestamp(local_timestamp)

		# generate lift-off time
		if launch_unix.minute < 10:
			launch_time = f'{launch_unix.hour}:0{launch_unix.minute}'
		else:
			launch_time = f'{launch_unix.hour}:{launch_unix.minute}'

		# set time for chat
		readable_utc = load_time_zone_status(chat, readable=True)
		time_string = f'`{launch_time}` `UTC{readable_utc}`'
		chat_launch_str = launch_str.replace('LAUNCHTIMEHERE', time_string)
		ret = send_notification(chat, chat_launch_str, launch_id, cmd_keyword, launch_row[19], notif_class)

		if ret:
			success = True
		else:
			success = False
			if debug_log:
				logging.info(f'🛑 Error sending notification to chat={anonymize_id(chat)}! Exception: {ret}')


		tries = 1
		while not ret:
			time.sleep(2)
			ret = send_notification(chat, chat_launch_str, launch_id, cmd_keyword, launch_row[19], notif_class)
			tries += 1

			if ret:
				success = True
				if debug_log:
					logging.info(f'✅ Notification sent successfully to chat={anonymize_id(chat)}! Took {tries} tries.')

			elif ret != True and tries > 5:
				if debug_log:
					logging.info(f'⚠️ Tried to send notification to {anonymize_id(chat)} {tries} times – passing.')

				ret = True

		if success:
			try:
				reached_people += bot.getChatMembersCount(chat) - 1
			except Exception as error:
				if debug_log:
					logging.exception(f'⚠️ Error getting number of chat members for chat={anonymize_id(chat)}. Error: {error}')

	# log end time
	end_time = timer()

	# update stats for sent notifications
	conn.close()
	update_stats_db(stats_update={'notifications':len(notify_list)}, db_path='data')

	# set notification as sent; if 12 hour sent but 24 hour not sent, disable "higher" ones as well
	conn.close()
	conn = sqlite3.connect(os.path.join(data_dir, 'launchbot-data.db'))
	c = conn.cursor()

	try:
		notification_type = notif_dict[notif_class]
		c.execute(f'UPDATE launches SET {notification_type} = 1 WHERE id = ?', (launch_id,))

		if debug_log:
			try:
				logging.info(f'🚩 {t_minus} {time_format} notification flag set to 1 for launch {launch_id}')
				logging.info(f'ℹ️ Notifications sent: {len(notify_list)} in {((end_time - start_time)):.2f} s, number of people reached: {reached_people}')
			except:
				pass

	except Exception as e:
		if debug_log:
			logging.exception(f'''⚠️ Error disabling notification in notification_handler_old().
			t_minus={t_minus}, launch_id={launch_id}. Notifications sent: {len(notify_list)}.
			Exception: {e}. Disabling all further notifications.''')

		c.execute('UPDATE launches SET notify24h = 1, notify12h = 1, notify60min = 1, notify5min = 1, notifylaunch = 1 WHERE id = ?', (launch_id,))

	conn.commit()
	conn.close()

	# remove previous notification
	remove_previous_notification(launch_id, cmd_keyword)

	# store msg_identifiers
	msg_identifiers = ','.join(msg_identifiers)
	store_notification_identifiers(launch_id, msg_identifiers)


def get_notify_list(lsp, launch_id, notif_class):
	'''
	Pull all chats with matching keyword (LSP ID), matching country code notification,
	or an "all" marker (and no exclusion for this ID/country) '''

	# Establish connection
	data_dir = 'data'
	if not os.path.isfile(os.path.join(data_dir,'launchbot-data.db')):
		create_notify_database()

	conn = sqlite3.connect(os.path.join(data_dir,'launchbot-data.db'))
	c = conn.cursor()

	# pull all where keyword = LSP or "All"
	c.execute('SELECT * FROM notify WHERE keyword == ? OR keyword == ?',(lsp, 'All'))
	query_return = c.fetchall()

	# parse for possible mutes
	parsed_query_return = set()
	muted_chats = set()
	for row in query_return:
		append = True
		if row[2] is not None:
			if row[2] != '':
				split = row[2].split(',')
				for muted_id in split:
					if muted_id == str(launch_id):
						append = False
						muted_chats.add(row[0])

		if append:
			if row[0] not in muted_chats:
				parsed_query_return.add(row)

	query_return = parsed_query_return
	parsed_query_return, muted_this_launch = set(), set()
	for row in query_return:
		if row[0] in muted_chats:
			muted_this_launch.add(row[0])
		else:
			parsed_query_return.add(row)

	query_return = parsed_query_return

	if debug_log and len(muted_this_launch) > 0:
		logging.info(f'🔇 Not notifying {len(muted_this_launch)} chat(s) due to mute status')

	# parse output
	notify_dict, notify_list = {}, set() # chat: id: toggle
	for row in query_return:
		chat = row[0]
		if chat not in notify_dict:
			notify_dict[chat] = {}

		notify_dict[chat][row[1]] = row[3] # lsp: 0/1, or All: 0/1

	# if All is enabled, and lsp is disabled
	for chat, val in notify_dict.items(): # chat, dictionary (dict is in the form of LSP: toggle)
		enabled, disabled = set(), set()
		for l, e in val.items(): # lsp, enabled
			if e == 1:
				enabled.add(l)
			else:
				disabled.add(l)

		if lsp in disabled and 'All' in enabled:
			if debug_log:
				logging.info(f'🔕 Not notifying {anonymize_id(chat)} about {lsp} due to disabled flag. All flag was enabled.')
				try:
					logging.info(f'ℹ️ notify_dict[{anonymize_id(chat)}]: {notify_dict[chat]} | lsp: {lsp} | enabled: {enabled} | disabled: {disabled}')
				except:
					logging.info(f'⚠️ KeyError getting notify_dict[chat]. notify_dict: {notify_dict}')

		elif lsp in enabled or 'All' in enabled:
			notify_list.add(chat)

	# parse for chats which have possibly disabled this notification type in preferences
	if notif_class is not None:
		final_list, ignored_due_to_pref = set(), set()
		index = {'24h': 0, '12h': 1, '1h': 2, '5m': 3}[notif_class]
		for chat in notify_list:
			if list(get_notif_preference(chat))[index] == 1:
				final_list.add(chat)
			else:
				ignored_due_to_pref.add(chat)

		if debug_log:
			logging.info(f'🔕 Not notifying {len(ignored_due_to_pref)} chat(s) due to notification preferences.')
	else:
		final_list = notify_list

	return final_list



def send_notification(
	chat: str, message: str, launch_id: str, lsp_name: str, notif_class: str,
	bot: 'telegram.bot.Bot'):
	# send early notifications silently
	silent = True if notif_class not in ('1h', '5m') else False

	try:
		# load mute status, generate keys
		mute_status = load_mute_status(chat, launch_id, lsp_name)
		mute_press = 0 if mute_status == 1 else 1
		mute_key = {0:f'🔇 Mute this launch',1:'🔊 Unmute this launch'}[mute_status]

		# /mute/$provider/$launch_id/(0/1) | 1=muted (true), 0=not muted
		keyboard = InlineKeyboardMarkup(
			inline_keyboard = [[
					InlineKeyboardButton(
						text=mute_key, callback_data=f'mute/{keywords}/{launch_id}/{mute_press}')]])

		sent_msg = bot.sendMessage(
			chat, notification, parse_mode='MarkdownV2',
			reply_markup=keyboard, disable_notification=silent)

		# sent message is stored in sent_msg; store in db so we can edit messages
		msg_identifier = f'{sent_msg["chat"]["id"]}:{sent_msg["message_id"]}'
		msg_identifiers.append(str(msg_identifier))
		return True
	
	except telegram.exception.BotWasBlockedError:
		if debug_log:
			logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat)} – cleaning notify database...')

		clean_notify_database(chat)
		return True

	except telegram.exception.TelegramError as error:
		# Bad Request: chat not found
		if error.error_code == 400 and 'not found' in error.description:
			if debug_log:
				logging.exception(f'⚠️ Chat {anonymize_id(chat)} not found – cleaning notify database... Error: {error}')

			clean_notify_database(chat)
			return True

		elif error.error_code == 403:
			if 'user is deactivated' in error.description:
				if debug_log:
					logging.exception(f'⚠️ User {anonymize_id(chat)} was deactivated – cleaning notify database... Error: {error}')

				clean_notify_database(chat)
				return True

			elif 'bot was kicked from the supergroup chat' in error.description:
				if debug_log:
					logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database... Error: {error}')

				clean_notify_database(chat)
				return True

			elif 'Forbidden: bot is not a member of the supergroup chat' in error.description:
				if debug_log:
					logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database... Error: {error}')

				clean_notify_database(chat)
				return True

			else:
				if debug_log:
					logging.exception(f'⚠️ Unhandled 403 telegram.exception.TelegramError in send_notification: {error}')

		# Rate limited by Telegram (https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this)
		elif error.error_code == 429:
			if debug_log:
				logging.exception(f'🚧 Rate-limited (429) - sleeping for 5 seconds and continuing. Error: {error}')

			time.sleep(5)
			return False

		# Something else
		else:
			if debug_log:
				logging.exception(f'⚠️ Unhandled telegram.exception.TelegramError in send_notification: {error}')

			return False


def create_notification_message(launch: dict, notif_class: str, bot_username: str) -> str:
	'''Summary
	Generates the notification message body from the provided launch
	database row.
	'''

	# shorten long launch service provider names
	launch_name = launch['name'].split('|')[1].strip()
	lsp_name = launch['lsp_short'] if len(launch['lsp_name']) > len('Virgin Orbit') else launch['lsp_name']
	lsp_flag = map_country_code_to_flag(launch['lsp_country_code'])

	# shorten very common pad names
	if 'LC-' not in launch['pad_name']:
		launch['pad_name'] = launch['pad_name'].replace('Space Launch Complex ', 'SLC-').replace('Launch Complex ', 'LC-')

	if 'air launch' in launch['pad_name'].lower():
		launch['pad_name'] = 'Air launch to orbit'

	# generate location
	launch_site = launch['location_name'].split(',')[0].strip()
	location_flag = map_country_code_to_flag(launch['location_country_code'])
	location = f'{launch["pad_name"]}, {launch_site} {location_flag}'

	# add mission information: type, orbit
	mission_type = launch['mission_type'].capitalize() if launch['mission_type'] is not None else 'Unknown purpose'

	# TODO add orbits for TMI and TLI, once these pop up for the first time
	orbit_map = {
		'Sub Orbital': 'Sub-orbital', 'VLEO': 'Very low-Earth orbit', 'LEO': 'Low-Earth orbit',
		'SSO': 'Sun-synchronous orbit', 'MEO': 'Medium-Earth orbit', 'GEO': 'Geostationary (direct)',
		'GTO': 'Geostationary (transfer)', 'GSO': 'Geosynchronous orbit', 'LO': 'Lunar orbit'
	}

	try:
		orbit_info = '🌒' if 'LO' in launch['mission_orbit_abbrev'] else '🌍'
		if launch['mission_orbit_abbrev'] in orbit_map.keys():
			orbit_str = orbit_map[launch['mission_orbit_abbrev']]
		else:
			orbit_str = launch['mission_orbit'] if launch['mission_orbit_abbrev'] is not None else 'Unknown'
			if 'Starlink' in launch_name:
				orbit_str = 'Very-low Earth orbit'
	except TypeError:
		orbit_info = '🌍'
		orbit_str = 'Unknown orbit'

	# launch probability to weather emoji TODO add to final message
	probability_map = {80: '☀️', 60: '🌤', 40: '🌥', 20: '☁️', 00: '⛈'}
	if launch['probability'] not in (-1, None):
		for prob_range_start, prob_str in probability_map.items():
			if launch['probability'] >= prob_range_start:
				probability = f"{prob_str} *{int(launch['probability'])} %* probability of launch"
	else:
		probability = None

	# if there's a landing attempt, generate the string for the booster
	if launch['launcher_landing_attempt']:
		core_str = launch['launcher_serial_number']
		core_str = 'Unknown' if core_str is None else core_str

		if launch['launcher_is_flight_proven']:
			reuse_count = launch['launcher_stage_flight_number']
			if reuse_count < 10:
				reuse_count = {
					1: 'first', 2: 'second', 3: 'third', 4: 'fourth', 5: 'fifth',
					6: 'sixth', 7: 'seventh', 8: 'eighth', 9: 'ninth', 10: 'tenth'}[reuse_count]
				reuse_str = f'{core_str} ({reuse_count} flight ♻️)'
			else:
				try:
					if reuse_count in (11, 12, 13):
						suffix = 'th'
					else:
						suffix = {1: 'st', 2: 'nd', 3: 'rd'}[int(str(reuse_count)[-1])]
				except:
					suffix = 'th'

				reuse_str = f'{core_str} ({reuse_count}{suffix} flight ♻️)'
		else:
			reuse_str = f'{core_str} (first flight ✨)'

		landing_loc_map = {
			'OCISLY': 'Atlantic Ocean', 'JRTI': 'Atlantic Ocean', 'ASLOG': 'Pacific Ocean',
			'LZ-1': 'CCAFS RTLS', 'LZ-2': 'CCAFS RTLS', 'LZ-4': 'VAFB RTLS'}

		if launch['launcher_landing_location'] in landing_loc_map.keys():
			landing_type = landing_loc_map[launch['launcher_landing_location']]
			landing_str = f"{launch['launcher_landing_location']} ({landing_type})"
		else:
			landing_type = launch['landing_type']
			landing_str = f"{launch['launcher_landing_location']} ({landing_type})"

		recovery_str = f'''
		*Booster information* 🚀
		*Core* {short_monospaced_text(reuse_str)}
		*Landing* {short_monospaced_text(landing_str)}\n
		'''
	else:
		recovery_str = None

	# TODO add "live_str" with link to webcast if 1 hour or 5 min
	if notif_class in ('notify_60min', 'notify_5min'):
		vid_url = None
		try:
			urls = launch['webcast_url_list'].split(',')
		except AttributeError:
			urls = set()

		if len(urls) == 0:
			link_text = '🔇 *No live video* available.'
		else:
			for url in urls:
				if 'youtube' in url:
					vid_url = url
					break

			if vid_url is None:
				vid_url = urls[0]

			link_text = f'🔴 *Watch the launch* LinkTextGoesHere'
			link_text = link_text.replace(
				'LinkTextGoesHere', f'[live\!]({reconstruct_link_for_markdown(vid_url)})')
	else:
		link_text = None

	# map notif_class to a more readable string
	t_minus = {
		'notify_24h': '24 hours', 'notify_12h': '12 hours',
		'notify_60min': '60 minutes', 'notify_5min': '5 minutes'}

	# construct the base message
	message = f'''
	🚀 *{launch_name}* is launching in *{t_minus[notif_class]}*
	*Launch provider* {short_monospaced_text(lsp_name)} {lsp_flag}
	*Vehicle* {short_monospaced_text(launch["rocket_name"])}
	*Pad* {short_monospaced_text(location)}

	*Mission information* {orbit_info}
	*Type* {short_monospaced_text(mission_type)}
	*Orbit* {short_monospaced_text(orbit_str)}

	{recovery_str if recovery_str is not None else ""}
	{link_text if link_text is not None else ""}
	*🕓 The launch is scheduled* for LAUNCHTIMEHERE
	*🔕 To disable* use /notify@{bot_username}
	'''

	return inspect.cleandoc(message)


def notification_handler(db_path: str, notification_dict: dict, bot_username: str,
	bot: 'telegram.bot.Bot'):
	''' Summary
	Handles the flow associated with sending a notification.

	notification_dict is of type dict(uid1:notify_class, uid2:notify_class...)
	'''
	def verify_launch_is_up_to_date(launch_uid: str, cursor: sqlite3.Cursor):
		''' Summary
		Function verifies that the last time the launch info was update is equal
		to the last time the API was updated. If these two don't match,
		the launch may have moved so much forward that we don't "see" it anymore.
		'''
		# verify update times match: if not, remove launch and return false
		cursor.execute('SELECT last_updated FROM launches WHERE unique_id = ?', (launch_uid,))
		query_return = cursor.fetchall()

		if len(query_return) == 0:
			logging.warning(f'verify_launch_is_up_to_date couldn\'t find launch with id={launch_uid}')
			return False

		# integer unix time stamp of when the launch was last updated
		launch_last_update = query_return[0][0]

		# pull last time the DB was updated from statistics database
		cursor.execute('SELECT last_api_update FROM stats')

		try:
			last_api_update = cursor.fetchall()[0][0]
		except KeyError:
			logging.exception('Error pulling last_api_update from stats database!')
			return False

		# if equal, we're good
		if launch_last_update == last_api_update:
			return True

		# if not, uh oh...
		logging.warning(
			f'''
			🛑 [verify_launch_is_up_to_date] launch_last_update != last_api_update!
			🛑 launch_uid={launch_uid}
			🛑 launch_last_update={launch_last_update}
			🛑 last_api_update={last_api_update}
			''')

		# remove launch from db
		cursor.execute('DELETE FROM launches WHERE unique_id = ?', (launch_uid,))
		logging.warning(f'⚠️ launch_id={launch_uid} successfully removed from database!')

		return False

	logging.info('🎉 notification_handler ran successfully!')
	logging.info(f'📨 notification_dict: {notification_dict}')

	# db connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	for launch_id, notify_class in notification_dict.items():
		# select launches with matching IDs, execute query
		cursor.execute("SELECT * FROM launches WHERE unique_id = ?", (launch_id,))

		# convert rows into dictionaries for super easy parsing
		launch_dict = [dict(row) for row in cursor.fetchall()][0]

		# pull relevant stuff from launch_dict
		launch_id = launch_dict['unique_id']

		# toggle notification to 1 in launch db
		cursor.execute(f"UPDATE launches SET {notify_class} = 1 WHERE unique_id = ?", (launch_id,))

		# log, commit changes
		logging.info(f'🚩 Toggled notification flags to 1 for {notify_class}')
		conn.commit()

		''' Right before sending, verify launch was actually updated in the last API update:
		if it wasn't, the launch may have slipped so much forward that it's not included within
		the 50 launches we request. In this case, delete the launch row from the database. '''
		up_to_date = verify_launch_is_up_to_date(launch_uid=launch_id, cursor=cursor)

		# if launch isn't up to date, uh oh
		if not up_to_date:
			logging.warning(f'⚠️ Launch info isn\'t up to date! launch_id={launch_id}')
			logging.warning(f'⚠️ Commiting database change and returning...')

			# cursor executed an insert in verify_launch_is_up_to_date: commit here
			conn.commit()
			conn.close()
			return

		# info is up to date!
		logging.info('✅ Launch info is up to date! Proceeding with sending notification...')

		# create the notification message TODO add astronaut/spacecraft info
		notification_message = create_notification_message(
			launch=launch_dict, notif_class=notify_class, bot_username=bot_username)

		# log message
		logging.info(notification_message)

		# parse message for markdown
		notification_message = reconstruct_message_for_markdown(notification_message)

		# send notification
		with open(os.path.join(db_path, 'bot-config.json'), 'r') as bot_config:
			config = json.load(bot_config)

		chat_id = config['owner']

		if bot is not None:
			try:
				bot.sendMessage(chat_id, notification_message, parse_mode='MarkdownV2')
			except:
				logging.exception('⚠️ Error sending notification!')

			logging.info('✉️ Sent notification!')
		else:
			logging.info('✉️ Pseudo-sent notification!')

	# close db connection at exit
	conn.close()


def clear_missed_notifications(db_path: str, launch_id_dict_list: list):
	'''
	[Enter module description]

	Args:
	    db_path (str): Description
	    launch_id_dict_list (list): Description
	'''
	# open db connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	# count missed
	miss_count = 0

	# check which notifications we've missed
	for launch_id_dict in launch_id_dict_list:
		# pull uid: missed_notification from launch_id_dict
		for uid, missed_notification in launch_id_dict.items():
			# construct insert statement for the missed notifications: all will be set to True
			cursor.execute(f'''UPDATE launches SET {missed_notification} = 1 WHERE unique_id = ?''', (uid,))
			miss_count += 1

			# log
			logging.warning(f'⚠️ Missed {missed_notification} for uid={uid}')

	logging.info(f'✅ Cleared {miss_count} missed notifications!')

	conn.commit()
	conn.close()


def notification_send_scheduler(db_path: str, next_api_update_time: int,
	scheduler: BackgroundScheduler, bot_username: str, bot: 'telegram.bot.Bot'):
	'''Summary
	Notification checks are performed right after an API update, so they're always
	up to date when the scheduling is performed. There should be only one of each
	notification in the schedule at all times. Thus, the notification jobs should
	be tagged accordingly.
	'''

	# debug print
	logging.debug('📩 Running notification_send_scheduler...')

	# load notification statuses for launches
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# fields to be selected
	select_fields = 'net_unix, unique_id, notify_24h, notify_12h, notify_60min, notify_5min'

	# set a 5 minute notify window, so we don't miss notifications
	notify_window = int(time.time()) - 60*5

	try:
		cursor.execute(f'SELECT {select_fields} FROM launches WHERE net_unix >= ?', (notify_window,))
		query_return = cursor.fetchall()
	except sqlite3.OperationalError:
		query_return = set()

	if len(query_return) == 0:
		logging.info('⚠️ No launches found for scheduling notifications!')
		return

	# sort in-place by NET
	query_return.sort(key=lambda tup:tup[0])

	# create a dict of notif_send_time: launch(es) tags
	notif_send_times, time_map = {}, {0: 24*3600+30, 1: 12*3600+30, 2: 3600+30, 3: 5*60+30}
	for launch_row in query_return:
		for enum, notif_bool in enumerate(launch_row[2::]):
			if not notif_bool:
				# time for check: launch time - notification time (before launch time)
				send_time = launch_row[0] - time_map[enum]

				# launch id
				uid = launch_row[1]

				# map enum to a notify_class
				notify_class_map = {
					0: 'notify_24h', 1: 'notify_12h', 2: 'notify_60min', 3: 'notify_5min'}

				'''
				send_time -> launches to notify for.
				This isn't necessarily required, but there might be some unique case
				where two notifications are needed to be sent at the exact same time. Previously
				this wasn't relevant, as pending notifications were checked for continuously,
				but now this could result in a notification not being sent.

				Updated: notif_send_times[send_time] now contains a dictionary of uid:notif_class.
				'''
				if send_time not in notif_send_times:
					notif_send_times[send_time] = {uid: notify_class_map[enum]}
				else:
					if uid not in notif_send_times:
						notif_send_times[send_time][uid] = notify_class_map[enum]
					else:
						logging.warning(f'''⚠️ More than one notify_class!
							Existing: {notif_send_times[send_time][uid]}
							Replacing with: {notify_class_map[enum]}''')
						notif_send_times[send_time][uid] = notify_class_map[enum]

	# clear previously stored notifications
	logging.debug(f'🚮 Clearing previously queued notifications...')
	cleared_count = 0
	for job in scheduler.get_jobs():
		if 'notification' in job.id:
			scheduler.remove_job(job.id)
			cleared_count += 1

	# cleared!
	logging.debug(f'✅ Cleared {cleared_count} queued notifications!')

	''' Add notifications to schedule queue until we hit the next scheduled API update.
	This allows us to queue the minimum amount of notifications '''
	scheduled_notifications, missed_notifications = 0, []
	for send_time, notification_dict in notif_send_times.items():
		# if send time is later than next API update, ignore
		if send_time > next_api_update_time:
			pass
		elif send_time < time.time() - 60*5:
			# if send time is more than 5 minutes in the past, declare it missed
			missed_notifications.append(notification_dict)
		else:
			# verify we're not already past send_time
			if send_time < time.time():
				send_time_offset = int(time.time() - send_time)
				logging.warning(f'⚠️ Missed send_time by {send_time_offset} sec! Sending in 3 seconds.')
				send_time = time.time() + 3

			# convert to a datetime object, add 2 sec for margin
			notification_dt = datetime.datetime.fromtimestamp(send_time + 2)

			# schedule next API update, and we're done: next update will be scheduled after the API update
			scheduler.add_job(
				notification_handler, 'date', id=f'notification-{int(send_time)}',
				run_date=notification_dt, args=[db_path, notification_dict, bot_username, bot])

			# done, log
			logging.debug(f'''send_time={send_time}, notification_dict={notification_dict},
				scheduled_notifications={scheduled_notifications}''')
			logging.info(f'📨 Scheduled {len(notification_dict)} notifications for {notification_dt}')
			scheduled_notifications += 1

	# if we've missed any notifications, clear them
	if len(missed_notifications) != 0:
		clear_missed_notifications(db_path, missed_notifications)

	logging.info(f'Notification scheduling done! Queued {scheduled_notifications} notifications.')

	# close db connection at exit
	conn.close()
