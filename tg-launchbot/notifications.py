'''
notifications.py handles effectively everything related to generating, sending, and deleting
notifications.
'''

import os
import sys
import time
import datetime
import sqlite3
import logging
import inspect
import telegram


from apscheduler.schedulers.background import BackgroundScheduler
from telegram import InlineKeyboardButton, InlineKeyboardMarkup


from db import create_chats_db, update_stats_db
from timezone import load_bulk_tz_offset
from utils import (
	short_monospaced_text, map_country_code_to_flag, reconstruct_link_for_markdown,
	reconstruct_message_for_markdown, anonymize_id, suffixed_readable_int,
	timestamp_to_legible_date_string)


def postpone_notification(
	db_path: str, postpone_tuple: tuple, bot: 'telegram.bot.Bot'):
	'''
	Handles the final stages of the flow associated with sending a postpone notification.
	'''
	def send_postpone_notification(chat_id: str, launch_id: str):
		'''
		Handles the actual sending of the notification.
		'''
		try:
			# set the muting button
			keyboard = InlineKeyboardMarkup(
				inline_keyboard = [[InlineKeyboardButton(
					text='🔇 Mute this launch', callback_data=f'mute/{launch_id}/1')]])

			# catch the sent message object so we can store its id
			sent_msg = bot.sendMessage(
				chat_id, message, parse_mode='MarkdownV2', reply_markup=keyboard)

			# sent message is stored in sent_msg; store in db so we can edit messages
			msg_identifier = f'{sent_msg["chat"]["id"]}:{sent_msg["message_id"]}'
			return True, msg_identifier

		except telegram.error.RetryAfter as error:
			''' Rate-limited by Telegram
			https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this '''
			retry_time = error.retry_after
			logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {retry_time} sec.')
			time.sleep(retry_time + 0.25)

			return False, None

		except telegram.error.TimedOut as error:
			logging.exception('🚧 Got a telegram.error.TimedOut: sleeping for 1 second.')
			time.sleep(1)

			return False, None

		except telegram.error.Unauthorized as error:
			logging.info(f'⚠️ Unauthorized to send: {error}')

			# known error: clean the chat from the chats db
			logging.info('🗃 Cleaning chats database...')
			clean_chats_db(db_path, chat_id)

			# succeeded in (not) sending the message
			return True, None

		except telegram.error.ChatMigrated as error:
			logging.info(f'⚠️ Chat {chat_id} migrated to {error.new_chat_id}! Updating chats db...')
			conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
			cursor = conn.cursor()

			try:
				cursor.execute('UPDATE chats SET chat = ? WHERE chat = ?', (error.new_chat_id, chat_id))
			except:
				logging.exception(f'Unable to migrate {chat_id} to {error.new_chat_id}!')

			conn.commit()
			conn.close()

		except telegram.error.TelegramError as error:
			if 'chat not found' in error.message:
				logging.exception(f'⚠️ Chat {anonymize_id(chat_id)} not found.')

			elif 'bot was blocked' in error.message:
				logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat_id)}.')

			elif 'user is deactivated' in error.message:
				logging.exception(f'⚠️ User {anonymize_id(chat_id)} was deactivated.')

			elif 'bot was kicked from the supergroup chat' in error.message:
				logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat_id)}.')

			elif 'bot is not a member of the supergroup chat' in error.message:
				logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat_id)}.')

			elif "Can't parse entities" in error.message:
				logging.exception('🛑 Error parsing message markdown!')
				return False, None

			else:
				logging.exception('⚠️ Unhandled telegram.error.TelegramError in send_notification!')

			# known error: clean the chat from the chats db
			logging.info('🗃 Cleaning chats database...')
			clean_chats_db(db_path, chat_id)

			# succeeded in (not) sending the message
			return True, None

		else:
			# Something else, log
			logging.exception('⚠️ Unhandled telegram.error.TelegramError in send_notification!')
			return True, None

	# pull info from postpone_tuple
	launch_obj = postpone_tuple[0]
	postpone_msg = postpone_tuple[1]

	if len(launch_obj.lsp_name) > len('Virgin Orbit'):
		lsp_db_name = launch_obj.lsp_short
	else:
		lsp_db_name = launch_obj.lsp_name

	# load chats to notify
	notification_list = get_notify_list(
		db_path=db_path, lsp=lsp_db_name,
		launch_id=launch_obj.unique_id, notify_class='postpone')

	# load tz tuple for each chat
	notification_list_tzs = load_bulk_tz_offset(data_dir=db_path, chat_id_set=notification_list)

	sent_notification_ids = set()
	for chat, tz_tuple in notification_list_tzs.items():
		# generate unique time for each chat
		utc_offset = 3600 * tz_tuple[0]
		launch_unix = datetime.datetime.utcfromtimestamp(
			launch_obj.net_unix + utc_offset)

		# generate lift-off time string
		if launch_unix.minute < 10:
			launch_time = f'{launch_unix.hour}:0{launch_unix.minute}'
		else:
			launch_time = f'{launch_unix.hour}:{launch_unix.minute}'

		# set time with UTC-string for chat
		time_string = f'`{launch_time}` `UTC{tz_tuple[1]}`'
		message = postpone_msg.replace('LAUNCHTIMEHERE', time_string)

		# set date for chat
		date_string = timestamp_to_legible_date_string(launch_obj.net_unix + utc_offset)
		message = message.replace('DATEHERE', date_string)

		success, msg_id = send_postpone_notification(
			chat_id=chat, launch_id=launch_obj.unique_id)

		if success and msg_id is not None:
			''' send counts as success even if we fail due to the bot being blocked etc.:
			if we succeeded, but got a message id (actually sent something), store it '''
			sent_notification_ids.add(msg_id)
		elif not success:
			logging.info(f'⚠️ Failed to send postpone notification to chat={chat}!')

			fail_count = 0
			while not success or fail_count < 5:
				fail_count += 1
				success, msg_id = send_postpone_notification(
					chat_id=chat, launch_id=launch_obj.unique_id)

			# if we got success and a msg_id, store the identifiers
			if success and msg_id is not None:
				logging.info(f'✅ Success after {fail_count} tries!')
				sent_notification_ids.add(msg_id)

	return notification_list, sent_notification_ids


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

	# enabled states: parse comma-separated entries into lists
	if chat_row['enabled_notifications'] is not None:
		enabled_notifs = chat_row['enabled_notifications'].split(',')
	else:
		enabled_notifs = []

	# disabled states: parse comma-separated entries into lists
	if chat_row['disabled_notifications'] is not None:
		disabled_notifs = chat_row['disabled_notifications'].split(',')
	else:
		disabled_notifs = []

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


def store_notification_identifiers(db_path: str, launch_id: str, identifiers: str):
	'''
	Stores the notification identifiers for a sent notification. Already parsed into
	a string, so all we have to do is insert it.
	'''
	# Establish connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	# update db with this
	update_tuple = (identifiers, launch_id)

	try:
		cursor.execute('UPDATE launches SET sent_notification_ids = ? WHERE unique_id = ?', update_tuple)
	except:
		logging.exception('Error updating notification identifiers!')

	conn.commit()
	conn.close()


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
		if query_return[0]['enabled_notifications'] is not None:
			old_enabled_states = query_return[0]['enabled_notifications'].split(',')
		else:
			old_enabled_states = []

		if query_return[0]['disabled_notifications'] is not None:
			old_disabled_states = query_return[0]['disabled_notifications'].split(',')
		else:
			old_disabled_states = []

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


def toggle_launch_mute(db_path: str, chat: str, launch_id: str, toggle: int):
	'''
	Toggles launch mute for a chat.
	'''
	# get mute status
	conn = sqlite3.connect(os.path.join(db_path,'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# stringify chat ID
	chat = str(chat)

	# pull the current muted_launches field
	cursor.execute("SELECT muted_by FROM launches WHERE unique_id = ?", (launch_id,))
	query_return = [dict(row) for row in cursor.fetchall()]

	if len(query_return) == 0:
		logging.warning(f'No launches found to mute with launch_id={launch_id}')
		return

	if query_return[0]['muted_by'] is not None:
		muted_by = query_return[0]['muted_by'].split(',')
	else:
		muted_by = []

	if chat in muted_by and toggle == 0:
		# if chat is in muted_by, remove
		muted_by.remove(chat)

	elif chat not in muted_by and toggle == 1:
		# chat isn't in mutedd_by and toggle==1: add to muted_by
		muted_by.append(chat)

	elif chat not in muted_by and toggle == 0 or chat in muted_by and toggle == 1:
		# handle odd cases that should never happen
		if toggle == 0:
			logging.warning(f'Chat={chat} not found in muted_by and called with toggle==0!')
		elif toggle == 1:
			logging.warning(f'Chat={chat} found in muted_by, but called with toggle==1!')

		return

	# construct the new string we'll then insert
	muted_by_str = ','.join(muted_by)

	if len(muted_by_str) == 0:
		muted_by_str = None

	# insert
	cursor.execute('UPDATE launches SET muted_by = ? WHERE unique_id = ?', (muted_by_str, launch_id))

	# commit, close
	conn.commit()
	conn.close()


def load_mute_status(db_path: str, launch_id: str):
	'''
	Loads the mute status for a launch_id, and returns a tuple of all
	chats that have muted said launch.
	'''
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	# pull launch mute status for chat
	cursor.execute("SELECT muted_by FROM launches WHERE unique_id = ?", (launch_id,))
	query_return = cursor.fetchall()
	conn.close()

	if len(query_return) == 0:
		return ()

	# load comma-separated value string -> split by comma
	if query_return[0][0] is not None:
		muted_by = query_return[0][0].split(',')
	else:
		return ()

	# return as a tuple
	return tuple(muted_by)


def clean_chats_db(db_path, chat):
	'''
	Removes all notification settings for a chat from the chats database
	'''
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	cursor.execute("DELETE FROM chats WHERE chat = ?", (chat,))
	conn.commit()
	conn.close()


def remove_previous_notification(
	db_path: str, launch_id: str, notify_set: set, bot: 'telegram.bot.Bot'):
	'''
	This function attempts to remove the previously sent notification for this launch.

	Keyword arguments
		db_path (str)
		launch_id (str)
		notify_set (set): set of chat IDs the following notification will be sent to.
		If a chat is notified, the previous notification should be deleted, if possible.

	'''
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	cursor.execute('SELECT sent_notification_ids FROM launches WHERE unique_id = ?', (launch_id,))
	query_return = cursor.fetchall()

	if len(query_return) == 0:
		logging.info(f'No notifications to remove for launch {launch_id}')
		return

	# pull identifiers, verify they're not Nonetypes
	identifiers = query_return[0][0]
	if identifiers in (None, ''):
		logging.info('✅ No notificatons to remove!')
		return

	try:
		identifiers = identifiers.split(',')
	except:
		logging.exception(f'Unable to split identifiers! identifiers={identifiers}')
		return

	success_count, muted_count = 0, 0
	for id_pair in identifiers:
		# split into chat_id, message_id
		id_pair = id_pair.split(':')

		try:
			# construct the message identifier
			chat_id, msg_id = id_pair[0], id_pair[1]
			message_identifier = (chat_id, msg_id)
		except IndexError:
			# throws an error if nothing to remove (i.e. empty db)
			logging.info(f'Nothing to remove: id_pair = {id_pair}, identifiers={identifiers}')
			return

		# make sure chat_id is in notify_set
		if chat_id in notify_set:
			try:
				success = bot.delete_message(chat_id, msg_id)
				if success:
					success_count += 1
				else:
					logging.info(f'Failed to delete message {message_identifier}! Ret={success}')
			except telegram.error.BadRequest:
				pass
			except telegram.error.RetryAfter as error:
				# sleep for a while
				retry_time = error.retry_after
				logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {retry_time} sec.')
				time.sleep(retry_time + 0.25)

				# try deleting again
				if bot.delete_message(chat_id, msg_id):
					logging.info(f'✅ Successfully deleted message after sleeping!')
				else:
					logging.info(f'⚠️ Failed to remove notification after sleeping!')

			except Exception as error:
				logging.exception(f'⚠️ Unable to delete previous notification. msg_id: {message_identifier}')
				logging.warning(f'Error: {error} | vars: {vars(error)}')
		else:
			muted_count += 1
			logging.info(f'🔍 Not removing previous notification for chat={anonymize_id(chat_id)}')

	logging.info(f'✅ Successfully removed {success_count} previously sent notifications!')
	logging.info(f'🔍 {muted_count} avoided due to mute status or notification disablement.')


def get_notify_list(db_path: str, lsp: str, launch_id: str, notify_class: str) -> set:
	'''
	Pull all chats with matching keyword (LSP ID), matching country code notification,
	or an "all" marker (and no exclusion for this ID/country)
	'''
	# Establish connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	''' Select all where the lsp or 'All' is in the row. If chat has 'All' in the row,
	make sure the lsp name isn't in disabled_notifications. '''
	try:
		cursor.execute("""
			SELECT * FROM chats WHERE 
			enabled_notifications LIKE '%'||?||'%' 
			OR enabled_notifications LIKE '%'||?||'%'""", (lsp, 'All'))
	except sqlite3.OperationalError:
		conn.close()
		return set()

	# pull all
	query_return = cursor.fetchall()

	if len(query_return) == 0:
		logging.warning('⚠️ query_return == 0 in get_notify_list!')
		conn.close()
		return set()

	# pull all chats that have muted this launch (tuple)
	muted_by = load_mute_status(db_path, launch_id)

	# keep track chats to notify
	notification_list = set()

	# if a postpone, check for mutes/disabled launches only
	if notify_class == 'postpone':
		# only notify of postpone if chat has already been notified once
		cursor.execute('''SELECT notify_24h, notify_12h, notify_60min, notify_5min
			FROM launches WHERE unique_id = ?''', (launch_id,))

		launch_notif_states = cursor.fetchall()[0]
		logging.debug('notify_class==postpone: parsing...')
		logging.debug(f'got launch_notif_states: {launch_notif_states}')

		for enum, state in enumerate(launch_notif_states):
			enabled = bool(int(state) == 1)
			logging.debug(f'enum: {enum}, state: {state}, enabled: {enabled}')

			if int(state) == 0:
				if enum == 0:
					logging.debug('⚠️ Avoided min_enabled_state == -1')
					logging.debug('\tint(state) == 0 | min_enabled_state = 0')
					min_enabled_state = 0
				else:
					logging.debug(f'\tint(state) == 0 | min_enabled_state = {enum}')
					min_enabled_state = enum

				break

			if enum == 3 and int(state) != 0:
				logging.info('⚠️ All notif states enabled: avoided ValueError...')
				min_enabled_state = 3

		for chat_row in query_return:
			# if muted, pass
			if chat_row['chat'] in muted_by:
				continue

			# if disabled, pass
			if lsp in chat_row['disabled_notifications']:
				continue

			''' TODO determine which chats have disabled postpone notifs
			i.e. (implement the setting for users) '''

			# not muted, and not explicitly disabled: verify chat wants this type of notif
			# should result in e.g. a ['1','1','1','1'] (note: strings)
			chat_notif_prefs = chat_row['notify_time_pref'].split(',')
			logging.debug(f'{chat_row["chat"]}')
			logging.debug(f'\tchat_notif_prefs: {chat_notif_prefs}')

			# if any notify preference ≤ min_enabled_state == 1, add to notification_list
			chat_notified = False
			for notif_state in range(min_enabled_state, -1, -1):
				''' if index matches, chat has a ≥ notification state enabled and
				 should have been previously notified -> add to notification list '''
				logging.debug(f'\tnotif_state: {notif_state}')
				if chat_notif_prefs[notif_state] == '1':
					logging.debug(f'\t{chat_notif_prefs[notif_state]} == "1" | notif_state={notif_state}')
					notification_list.add(chat_row['chat'])
					chat_notified = True
					break

			if chat_notified:
				logging.debug('✅ Chat will be notified')
			else:
				logging.debug('🔴 Chat wont be notified!')
			logging.debug('\n===========')

		return notification_list

	# map notify_time to a list index, so we can check for notify preference
	notify_index = {
		'notify_24h': 0, 'notify_12h': 1,
		'notify_60min': 2, 'notify_5min': 3}[notify_class]

	# parse all chat rows to figure out who to send the notification to
	for chat_row in query_return:
		if chat_row['chat'] in muted_by:
			# if chat has marked this launch as muted, don't notify
			logging.info(f'🔇 Launch muted by {chat_row["chat"]}: not notifying')
			continue

		if lsp in chat_row['disabled_notifications']:
			# if lsp is in disabled_notification, pretty simple: don't notify
			logging.info(f'{lsp} in disabled_notifications for chat {chat_row["chat"]}')
			continue

		# not muted, and not explicitly disabled: verify chat wants this type of notif
		chat_notif_prefs = chat_row['notify_time_pref'].split(',')

		# if notify preference == 1, add no notification_list
		if chat_notif_prefs[notify_index] == '1':
			notification_list.add(chat_row['chat'])
		else:
			logging.info(f'Chat {chat_row["chat"]} has disabled notify_class={notify_class}')

	conn.close()
	return notification_list


def send_notification(
	chat: str, message: str, launch_id: str, notif_class: str,
	bot: 'telegram.bot.Bot', tz_tuple: tuple, net_unix: int, db_path: str):
	'''
	Functions sends a launch notification to chat.
	'''
	# send early notifications silently
	silent = bool(notif_class not in ('notify_60min', 'notify_5min'))

	# generate unique time for each chat
	utc_offset = 3600 * float(tz_tuple[0])
	launch_unix = datetime.datetime.utcfromtimestamp(net_unix + utc_offset)

	# generate lift-off time string
	if launch_unix.minute < 10:
		launch_time = f'{launch_unix.hour}:0{launch_unix.minute}'
	else:
		launch_time = f'{launch_unix.hour}:{launch_unix.minute}'

	# set time with UTC-string for chat
	time_string = f'`{launch_time}` `UTC{tz_tuple[1]}`'
	message = message.replace('LAUNCHTIMEHERE', time_string)

	try:
		# set the muting button
		keyboard = InlineKeyboardMarkup(
			inline_keyboard = [[InlineKeyboardButton(
				text='🔇 Mute this launch', callback_data=f'mute/{launch_id}/1')]])

		# catch the sent message object so we can store its id
		logging.info(f'Sending to {chat}...')
		sent_msg = bot.sendMessage(chat, message, parse_mode='MarkdownV2',
			reply_markup=keyboard, disable_notification=silent)

		# sent message is stored in sent_msg; store in db so we can edit messages
		msg_identifier = f'{sent_msg["chat"]["id"]}:{sent_msg["message_id"]}'
		return True, msg_identifier

	except telegram.error.RetryAfter as error:
		''' Rate-limited by Telegram
		https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this '''
		retry_time = error.retry_after
		logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {retry_time} sec.')
		time.sleep(retry_time + 0.25)

		return False, None

	except telegram.error.TimedOut as error:
		logging.exception('🚧 Got a telegram.error.TimedOut: sleeping for 1 second.')
		time.sleep(1)

		return False, None

	except telegram.error.Unauthorized as error:
		logging.info(f'⚠️ Unauthorized to send: {error}')

		# known error: clean the chat from the chats db
		logging.info('🗃 Cleaning chats database...')
		clean_chats_db(db_path, chat)

		# succeeded in (not) sending the message
		return True, None

	except telegram.error.ChatMigrated as error:
		logging.info(f'⚠️ Chat {chat} migrated to {error.new_chat_id}! Updating chats db...')
		conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
		cursor = conn.cursor()

		try:
			cursor.execute('UPDATE chats SET chat = ? WHERE chat = ?', (error.new_chat_id, chat))
		except:
			logging.exception(f'Unable to migrate {chat} to {error.new_chat_id}!')

		conn.commit()
		conn.close()

	except telegram.error.BadRequest as error:
		if 'Chat_write_forbidden' in error.message:
			logging.warning('⚠️ Unallowed to send messages to chat! (Chat_write_forbidden)')
			return True, None

		if 'Have no rights to send a message' in error.message:
			logging.warning('⚠️ Unallowed to send messages to chat! (Have no rights to send message)')
			return True, None

		logging.error('⚠️ Unknown BadRequest when sending message (telegram.error.BadRequest)')
		return True, None

	except telegram.error.TelegramError as error:
		if 'chat not found' in error.message:
			logging.exception(f'⚠️ Chat {anonymize_id(chat)} not found.')

		elif 'bot was blocked' in error.message:
			logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat)}.')

		elif 'user is deactivated' in error.message:
			logging.exception(f'⚠️ User {anonymize_id(chat)} was deactivated.')

		elif 'bot was kicked from the supergroup chat' in error.message:
			logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)}.')

		elif 'bot is not a member of the supergroup chat' in error.message:
			logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat)}.')

		elif "Can't parse entities" in error.message:
			logging.exception('🛑 Error parsing message markdown!')
			return False, None

		else:
			logging.exception('⚠️ Unhandled telegram.error.TelegramError in send_notification!')

		# known error: clean the chat from the chats db
		logging.info('🗃 Cleaning chats database...')
		clean_chats_db(db_path, chat)

		# succeeded in (not) sending the message
		return True, None

	else:
		# Something else, log
		logging.exception('⚠️ Unhandled telegram.error.TelegramError in send_notification!')
		return True, None


def create_notification_message(launch: dict, notif_class: str, bot_username: str) -> str:
	'''Summary
	Generates the notification message body from the provided launch
	database row.
	'''
	# launch name
	launch_name = launch['name'].split('|')[1].strip()

	# map long provider names to shorter ones where needed
	provider_name_map = {
		'Rocket Lab Ltd': 'Rocket Lab',
		'Northrop Grumman Innovation Systems': 'Northrop Grumman',
		'Russian Federal Space Agency (ROSCOSMOS)': 'ROSCOSMOS'}

	# shorten long launch service provider names
	if launch['lsp_name'] in provider_name_map.keys():
		lsp_name = provider_name_map[launch['lsp_name']]
	else:
		if len(launch['lsp_name']) > len('Galactic Energy'):
			if launch['lsp_short'] not in (None, ''):
				lsp_name = launch['lsp_short']
			else:
				lsp_name = launch['lsp_name']
		else:
			lsp_name = launch['lsp_name']

	# flag for lsp
	lsp_flag = map_country_code_to_flag(launch['lsp_country_code'])

	# shorten very common pad names
	if 'LC-' not in launch['pad_name']:
		launch['pad_name'] = launch['pad_name'].replace('Space Launch Complex ', 'SLC-')
		launch['pad_name'] = launch['pad_name'].replace('Launch Complex ', 'LC-')

	if 'air launch' in launch['pad_name'].lower():
		launch['pad_name'] = 'Air launch to orbit'

	# generate location
	launch_site = launch['location_name'].split(',')[0].strip()
	location_flag = map_country_code_to_flag(launch['location_country_code'])

	if 'Starship' in launch['rocket_name']:
		location = f'SpaceX South Texas Launch Site, Boca Chica {location_flag}'
	else:
		location = f'{launch["pad_name"]}, {launch_site} {location_flag}'

	# add mission information: type, orbit
	mission_type = launch['mission_type'].capitalize() if launch['mission_type'] is not None else 'Unknown purpose'

	# TODO add orbits for TMI and TLI, once these pop up for the first time
	orbit_map = {
		'Sub Orbital': 'Sub-orbital',
		'VLEO': 'Very low-Earth orbit', 'LEO': 'Low-Earth orbit',
		'SSO': 'Sun-synchronous orbit', 'PO': 'Polar orbit',
		'MEO': 'Medium-Earth orbit', 'GEO': 'Geostationary (direct)',
		'GTO': 'Geostationary (transfer)', 'GSO': 'Geosynchronous orbit',
		'LO': 'Lunar orbit'
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

	if launch['spacecraft_crew_count'] not in (None, 0):
		if 'Dragon' in launch['spacecraft_name']:
			spacecraft_info = True
		else:
			spacecraft_info = None
	else:
		spacecraft_info = None

	# if there's a landing attempt, generate the string for the booster
	if launch['launcher_landing_attempt']:
		core_str = launch['launcher_serial_number']
		core_str = 'Unknown' if core_str is None else core_str

		if launch['launcher_is_flight_proven']:
			reuse_count = launch['launcher_stage_flight_number']
			if lsp_name == 'SpaceX' and core_str[0:2] == 'B1':
				core_str += f'.{int(reuse_count)}'

			reuse_str = f'{core_str} ({suffixed_readable_int(reuse_count)} flight ♻️)'
		else:
			if lsp_name == 'SpaceX' and core_str[0:2] == 'B1':
				core_str += '.1'

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

		recovery_str = True
	else:
		recovery_str = None

	# parse for info text
	if launch['mission_description'] not in ('', None):
		# pull launch info
		if launch['mission_description'] is None:
			info_str = 'No launch information available.'
		else:
			if len(launch['mission_description'].split('.')) > 3:
				# if longer than 3 sentences, use the first 3
				info_str = '.'.join(launch['mission_description'].split('\n')[0].split('.')[0:3])
			else:
				# otherwise, just use the entire thing
				info_str = '\n\t'.join(launch['mission_description'].split('\n'))

		info_text = f'ℹ️ {info_str}'
	else:
		info_text = None

	# add the webcast link, if one exists, if we're close to launch
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

			link_text = '🔴 *Watch the launch* LinkTextGoesHere'
	else:
		link_text = None

	# map notif_class to a more readable string
	t_minus = {
		'notify_24h': '24 hours', 'notify_12h': '12 hours',
		'notify_60min': '60 minutes', 'notify_5min': '5 minutes'}

	# construct the base message
	base_message = f'''
	🚀 *{launch_name}* is launching in *{t_minus[notif_class]}*
	*Launch provider* {short_monospaced_text(lsp_name)} {lsp_flag}
	*Vehicle* {short_monospaced_text(launch["rocket_name"])}
	*Pad* {short_monospaced_text(location)}

	*Mission information* {orbit_info}
	*Type* {short_monospaced_text(mission_type)}
	*Orbit* {short_monospaced_text(orbit_str)}
	'''

	if spacecraft_info is not None:
		base_message += '\n\t'
		base_message += '*Dragon information* 🐉\n\t'
		base_message += f'*Crew* {short_monospaced_text("👨‍🚀" * launch["spacecraft_crew_count"])}\n\t'
		base_message += f'*Capsule* {short_monospaced_text(launch["spacecraft_sn"])}'
		base_message += '\n\t'

	if recovery_str is not None:
		base_message += '\n\t'
		base_message += '*Vehicle information* 🚀\n\t'

		if 'Starship' in launch['rocket_name']:
			base_message += f'*Starship* {short_monospaced_text(reuse_str)}\n\t'
		else:
			base_message += f'*Core* {short_monospaced_text(reuse_str)}\n\t'

		base_message += f'*Landing* {short_monospaced_text(landing_str)}'
		base_message += '\n\t'

	if info_text is not None:
		base_message += '\n\t'
		base_message += info_text
		base_message += '\n\t'

	if link_text is not None:
		base_message += '\n\t'
		base_message += link_text

	footer = f'''
	🕓 *The launch is scheduled* for LAUNCHTIMEHERE
	🔕 *To disable* use /notify@{bot_username}'''
	base_message += footer

	# reconstruct for markdown (text only: link added later)
	base_message = reconstruct_message_for_markdown(base_message)

	# add link with a simple str replace
	if link_text is not None and 'LinkTextGoesHere' in base_message:
		base_message = base_message.replace(
			'LinkTextGoesHere',
			f'[live\!]({reconstruct_link_for_markdown(vid_url)})'
		)

	return inspect.cleandoc(base_message)


def notification_handler(
	db_path: str, notification_dict: dict, bot_username: str, bot: 'telegram.bot.Bot'):
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
			# cursor executed an insert in verify_launch_is_up_to_date: commit here
			conn.commit()
			conn.close()

			logging.warning(f'⚠️ Launch info isn\'t up to date! launch_id={launch_id}')
			logging.warning('⚠️ Commiting database change and returning...')
			return

		# info is up to date!
		logging.info('✅ Launch info is up to date! Proceeding with sending notification...')

		# create the notification message 
		# TODO add astronaut & spacecraft info
		notification_message = create_notification_message(
			launch=launch_dict, notif_class=notify_class, bot_username=bot_username)

		# log message
		logging.info(notification_message)

		# get name LSP is identified as in the db (ex. CASC isn't in the db with its full name)
		if len(launch_dict['lsp_name']) > len('Virgin Orbit'):
			lsp_db_name = launch_dict['lsp_short']
		else:
			lsp_db_name = launch_dict['lsp_name']

		# log the db name for debugging
		logging.info(f'✅ lsp_db_name set to {lsp_db_name}')

		# get list of people to send the notification to
		notification_list = get_notify_list(
			db_path=db_path, lsp=lsp_db_name, launch_id=launch_id, notify_class=notify_class)
		logging.info(f'✅ Got notification list (len={len(notification_list)}) {notification_list}')

		# get time zone information for each chat: this is a lot faster in bulk
		notification_list_tzs = load_bulk_tz_offset(data_dir=db_path, chat_id_set=notification_list)
		logging.info(f'✅ Got notification tz list {notification_list_tzs}')

		# log send mode (silent or with sound)
		without_sound = bool(notify_class not in ('notify_60min', 'notify_5min'))
		logging.info(f'🔈 Sending notification {"silenty" if without_sound else "with sound"}...')

		# iterate over chat_id and chat's UTC-offset -> send notification
		sent_notification_ids = set()
		for chat_id, tz_tuple in notification_list_tzs.items():
			try:
				success, msg_id = send_notification(
					chat=chat_id, message=notification_message, launch_id=launch_id,
					notif_class=notify_class, bot=bot, tz_tuple=tz_tuple,
					net_unix=launch_dict['net_unix'], db_path=db_path)
			except Exception as error:
				logging.exception(f'⚠️ Exception sending notification ({error})')
				continue

			if success and msg_id is not None:
				''' send counts as success even if we fail due to the bot being blocked etc.:
				if we succeeded, but got a message id (actually sent something), store it '''
				sent_notification_ids.add(msg_id)
			elif not success:
				logging.info(f'⚠️ Failed to send notification to chat={chat_id}!')

				fail_count = 0
				while not success or fail_count < 5:
					fail_count += 1
					success, msg_id = send_notification(
						chat=chat_id, message=notification_message, launch_id=launch_id,
						notif_class=notify_class, bot=bot, tz_tuple=tz_tuple, net_unix=launch_dict['net_unix'],
						db_path=db_path)

				# if we got success and a msg_id, store the identifiers
				if success and msg_id is not None:
					logging.info(f'✅ Success after {fail_count} tries!')
					sent_notification_ids.add(msg_id)

			# TODO add reached_people back (slow; sensible?)

		# remove previous notification
		remove_previous_notification(
			db_path=db_path, launch_id=launch_id, notify_set=notification_list, bot=bot)
		logging.info('✉️ Previous notifications removed!')

		# notifications sent: store identifiers
		msg_id_str = ','.join(sent_notification_ids)
		store_notification_identifiers(db_path=db_path, launch_id=launch_id, identifiers=msg_id_str)
		logging.info('📃 Notification identifiers stored!')

		# update stats
		update_stats_db(stats_update={'notifications':len(notification_list)}, db_path=db_path)
		logging.info('📊 Stats updated!')

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
	cursor = conn.cursor()

	# fields to be selected
	select_fields = 'net_unix, unique_id, status_state'
	select_fields += ', notify_24h, notify_12h, notify_60min, notify_5min'

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
		# don't notify of unverified launches (status=TBD)
		launch_status = launch_row[2]
		if launch_status == 'TBD':
			continue

		for enum, notif_bool in enumerate(launch_row[3::]):
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
	logging.debug('🚮 Clearing previously queued notifications...')
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
			logging.debug(f't={send_time}, dict={notification_dict}, scheduled_notifs={scheduled_notifications}')
			logging.info(f'📨 Scheduled {len(notification_dict)} notifications for {notification_dt}')
			scheduled_notifications += 1

	# if we've missed any notifications, clear them
	if len(missed_notifications) != 0:
		clear_missed_notifications(db_path, missed_notifications)

	logging.info(f'Notification scheduling done! Queued {scheduled_notifications} notifications.')

	# close db connection at exit
	conn.close()
