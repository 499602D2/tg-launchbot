# -*- coding: utf-8 -*-
# /usr/bin/python3
import os
import sys
import time
import ssl
import datetime
import logging
import math
import inspect
import traceback
import random
import sqlite3
import difflib
import signal
from timeit import default_timer as timer

import requests
import telepot
import cursor
import schedule
import pytz
import ujson as json

from utils import (anonymize_id, time_delta_to_legible_eta)

from db import (
	update_stats_db, create_notify_database, store_notification_identifiers)

from timezone import (
	load_locale_string, remove_time_zone_information, update_time_zone_string,
	update_time_zone_value, load_time_zone_status)

from notifications import (
	send_postpone_notification, get_user_notifications_status, toggle_notification,
	update_notif_preference, get_notif_preference, toggle_launch_mute, get_notify_list,
	load_mute_status, remove_previous_notification, notification_handler)

from uptime import uptime
from timezonefinder import TimezoneFinder
from telepot.loop import MessageLoop
from telepot.namedtuple import InlineKeyboardMarkup, InlineKeyboardButton, ForceReply
from telepot.namedtuple import ReplyKeyboardMarkup, KeyboardButton, ReplyKeyboardRemove

'''
*Changelog* for version {VERSION.split('.')[0]}.{VERSION.split('.')[1]} (May 2020)
- Added preferences to /notify@{BOT_USERNAME} ⚙️
- You can now choose when you receive notifications (24h/12h/etc.)
- Updates to the schedule command
- Added probability of launch to /next@{BOT_USERNAME}
- /next@{BOT_USERNAME} now indicates if a launch countdown is on hold
'''

# TODO schedule: add "only show certain launches" button
# TODO changelog: add "show changelog" button to /help

# main loop-function for messages with flavor=chat
def handle(msg):
	try:
		content_type, chat_type, chat = telepot.glance(msg, flavor="chat")
	except KeyError:
		if 'poll' in msg:
			return

		logging.exception(f'KeyError in handle(): {msg}')

		return

	# for admin/private chat checks; also might throw an error when kicked out of a group, so handle that here as well
	try:
		try:
			chat_type = msg['chat']['type']
		except:
			chat_type = bot.getChat(chat)['type']

	except telepot.exception.BotWasKickedError:
		'''
		Bot kicked; remove corresponding chat IDs from notification database

		This exception is effectively only triggered if we're handling a message
		_after_ the bot has been kicked, e.g. after a bot restart.
		'''
		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		c = conn.cursor()

		c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
		conn.commit()
		conn.close()

		logging.info(f'⚠️ Bot removed from chat {anonymize_id(chat)} – notifications database cleaned [1]')
		return

	# group upgraded to a supergroup; migrate data
	if 'migrate_to_chat_id' in msg:
		old_ID = chat
		new_ID = msg['migrate_to_chat_id']

		logging.info(f'⚠️ Group {anonymize_id(old_ID)} migrated to {anonymize_id(new_ID)} - '
					 f'starting database migration...')

		# Establish connection
		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		c = conn.cursor()

		# replace old IDs with new IDs
		c.execute("UPDATE notify SET chat = ? WHERE chat = ?", (new_ID, old_ID))
		conn.commit()
		conn.close()

		logging.info('✅ Chat data migration complete!')

	# bot removed from chat
	elif 'left_chat_member' in msg and msg['left_chat_member']['id'] == BOT_ID:
		# bot kicked; remove corresponding chat IDs from notification database
		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		c = conn.cursor()

		c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
		conn.commit()
		conn.close()

		logging.info(f'⚠️ Bot removed from chat {anonymize_id(chat)} – notifications database cleaned [2]')
		return

	# detect if bot added to a new chat
	elif 'new_chat_members' in msg or 'group_chat_created' in msg:
		if 'new_chat_member' in msg:
			try:
				if BOT_ID in msg['new_chat_member']['id']:
					pass
				else:
					return

			except TypeError:
				if msg['new_chat_member']['id'] == BOT_ID:
					pass
				else:
					return
		elif 'group_chat_created' in msg:
			if msg['group_chat_created']:
				pass
			else:
				return

		reply_msg = f'''🚀 *Hi there!* I'm *LaunchBot*, a launch information and notifications bot!

		*List of commands*
		🔔 /notify adjust notification settings
		🚀 /next shows the next launches
		🗓 /schedule displays a simple flight schedule
		📊 /statistics tells various statistics about the bot
		✍️ /feedback send feedback/suggestion to the developer

		⚠️ *Note for group chats* ⚠️ 
		- Commands are *only* callable by group *admins* and *moderators* to reduce group spam
		- If the bot has admin permissions (permission to delete messages), it will automatically remove commands it doesn't answer to

		*Frequently asked questions* ❓
		_How do I turn off a notification?_
		- Use /notify@{BOT_USERNAME}: find the launch provider you want to turn notifications off for.

		_I want less notifications!_
		- You can choose at what times you receive notifications with /notify@{BOT_USERNAME}. You can edit these at the preferences menu (⚙️).

		_Why does the bot only answer to some people?_
		- You have to be an admin in a group to send commands.

		LaunchBot version *{VERSION}* ✨
		'''

		bot.sendMessage(chat, inspect.cleandoc(reply_msg), parse_mode='Markdown')

		notify(msg)

		logging.info(f'🌟 Bot added to a new chat! chat_id={anonymize_id(chat)}. Sent user the new inline keyboard. [1]')

		return

	try:
		command_split = msg['text'].strip().split(' ')
	except KeyError:
		pass
	except Exception as error:
		logging.exception(f'🛑 Error generating command split, returning. {error}')
		logging.info(f'msg object: {msg}')
		return

	# verify that the user who sent this is not in spammers
	try:
		if msg['from']['id'] in ignored_users:
			logging.info('😎 Message from spamming user ignored successfully')

			return
	except: # all users don't have a user ID, so check for the regular username as well
		if 'author_signature' in msg:
			if msg['author_signature'] in ignored_users:
				logging.info('😎 Message from spamming user (no UID) ignored successfully')

			return

	# regular text — check if it's feedback. If not, return.
	if content_type == 'text' and command_split[0][0] != '/':
		if 'reply_to_message' in msg:
			if msg['reply_to_message']['message_id'] in feedback_message_IDs and 'text' in msg:
				logging.info(f'✍️ Received feedback: {msg["text"]}')

				sender = bot.getChatMember(chat, msg['from']['id'])
				if sender['status'] == 'creator' or sender['status'] == 'administrator' or chat_type == 'private':
					bot.sendMessage(chat, f'😄 Thank you for your feedback!', reply_to_message_id=msg['message_id'])

					try: # remove the original feedback message
						bot.deleteMessage((chat, msg['reply_to_message']['message_id']))
					except Exception as error:
						logging.exception(f'Unable to remove sent feedback message with params chat={chat}, message_id={msg["reply_to_message"]["message_id"]} {error}')

					if OWNER != 0:
						bot.sendMessage(OWNER,
							f'😄 *Received feedback* from `{anonymize_id(msg["from"]["id"])}`:\n{msg["text"]}',
							parse_mode='MarkdownV2')

		return

	# if location in message, verify it's a time zone setup reply
	if 'location' in msg and 'reply_to_message' in msg:
		if chat in time_zone_setup_chats.keys():
			if msg['from']['id'] == time_zone_setup_chats[chat][1] and msg['reply_to_message']['message_id'] == time_zone_setup_chats[chat][0]:
				msg_identifier = (chat, time_zone_setup_chats[chat][0])
				bot.deleteMessage(msg_identifier)

				try:
					bot.deleteMessage((chat, msg['message_id']))
				except:
					pass

				latitude = msg['location']['latitude']
				longitude = msg['location']['longitude']

				timezone_str = TimezoneFinder().timezone_at(lng=longitude, lat=latitude)
				timezone = pytz.timezone(timezone_str)

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

				new_text = f'''✅ Time zone successfully set!

				Your time zone is *UTC{utc_offset_str} ({timezone_str})*

				You can now return to other settings.'''

				keyboard = InlineKeyboardMarkup(inline_keyboard = [[InlineKeyboardButton(text='⏮ Return to menu', callback_data=f'prefs/main_menu')]])
				bot.sendMessage(chat, text=inspect.cleandoc(new_text), reply_markup=keyboard, parse_mode='Markdown')

				# store user's timezone_str
				update_time_zone_string(chat, timezone_str)


		else:
			logging.info(f'🗺 Location received, but chat not in time_zone_setup_chats.keys()')

	# sees a valid command
	if content_type == 'text':
		command_split = [arg.lower() for arg in command_split]
		if command_split[0] in VALID_COMMANDS:
			# command we saw
			command = command_split[0]

			if '@' in command:
				command = command.split('@')[0]

			try:
				sent_by = msg['from']['id']
			except:
				sent_by = 0

			# check timers
			if not timer_handle(command, chat, sent_by):
				logging.info(f'✋ Spam prevented from chat {anonymize_id(chat)} by {anonymize_id(msg["from"]["id"])}. Command: {command}, returning.')
				return

			# check if sender is an admin/creator, and/or if we're in a public chat
			if chat_type != 'private':
				try:
					all_admins = msg['chat']['all_members_are_administrators']
				except:
					all_admins = False

				if not all_admins:
					sender = bot.getChatMember(chat, msg['from']['id'])
					if sender['status'] != 'creator' and sender['status'] != 'administrator':
						# check for bot's admin status and whether we can remove the message
						bot_chat_specs = bot.getChatMember(chat, bot.getMe()['id'])
						if bot_chat_specs['status'] == 'administrator':
							try:
								success = bot.deleteMessage((chat, msg['message_id']))
								if success:
									logging.info(f'✋ {command} called by a non-admin in {anonymize_id(chat)} ({anonymize_id(msg["from"]["id"])}): successfully deleted message! ✅')
								else:
									logging.info(f'✋ {command} called by a non-admin in {anonymize_id(chat)} ({anonymize_id(msg["from"]["id"])}): unable to delete message (success != True. Type:{type(success)}, val:{success}) ⚠️')
							except Exception as error:
								logging.exception(f'⚠️ Could not delete message sent by non-admin: {error}')

						else:
							logging.info(f'✋ {command} called by a non-admin in {anonymize_id(chat)} ({anonymize_id(msg["from"]["id"])}): could not remove.')

						return

			# start timer
			start = timer()

			# /start or /help
			if command in {'/start', '/help'}:
				# construct info message
				reply_msg = f'''🚀 *Hi there!* I'm *LaunchBot*, a launch information and notifications bot!

				*List of commands*
				🔔 /notify adjust notification settings
				🚀 /next shows the next launches
				🗓 /schedule displays a simple flight schedule
				📊 /statistics tells various statistics about the bot
				✍️ /feedback send feedback/suggestion to the developer

				⚠️ *Note for group chats* ⚠️ 
				- Commands are *only* callable by group *admins* and *moderators* to reduce group spam
				- If the bot has admin permissions (permission to delete messages), it will automatically remove commands it doesn't answer to

				*Frequently asked questions* ❓
				_How do I turn off a notification?_
				- Use /notify@{BOT_USERNAME}: find the launch provider you want to turn notifications off for.

				_I want less notifications!_
				- You can choose at what times you receive notifications with /notify@{BOT_USERNAME}. You can edit these at the preferences menu (⚙️).

				_Why does the bot only answer to some people?_
				- You have to be an admin in a group to send commands.

				LaunchBot version *{VERSION}* ✨
				'''

				bot.sendMessage(chat, inspect.cleandoc(reply_msg), parse_mode='Markdown')

				# /start, send also the inline keyboard
				if command == '/start':
					notify(msg)

					logging.info(f'🌟 Bot added to a new chat! chat_id={anonymize_id(chat)}. Sent user the new inline keyboard. [2]')

			# /next
			elif command == '/next':
				next_flight(msg, 0, True, None)

			# /notify
			elif command == '/notify':
				notify(msg)

			# /statistics
			elif command == '/statistics':
				update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)
				statistics(chat, 'cmd')

			# /schedule)
			elif command == '/schedule':
				flight_schedule(msg, True, 'vehicle')

			# /feedback
			elif command == '/feedback':
				feedback(msg)

			t_elapsed = timer() - start
			if msg['from']['id'] != OWNER and command != '/start':
				try:
					logging.info(f'🕹 {command} called by {anonymize_id(chat)} | args: {command_split[1:]} | {(1000*t_elapsed):.0f} ms')
				except:
					logging.info(f'🕹 {command} called by {anonymize_id(chat)} | args: [] | {(1000*t_elapsed):.0f} ms')

			# store statistics here, so our stats database can't be spammed either
			if command != '/statistics':
				update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)

		else:
			return


def callback_handler(msg):
	def update_main_view(chat, msg, provider_by_cc, text_refresh):
		# figure out what the text for the "enable all/disable all" button should be
		providers = set()
		for val in provider_by_cc.values():
			for provider in val:
				if provider in provider_name_map.keys():
					providers.add(provider_name_map[provider])
				else:
					providers.add(provider)

		notification_statuses, disabled_count, all_flag = get_user_notifications_status(chat, providers), 0, False
		if 0 in notification_statuses.values():
			disabled_count = 1

		try:
			if notification_statuses['All'] == 1:
				all_flag = True
		except:
			pass

		rand_planet = random.choice(('🌍', '🌎', '🌏'))

		if all_flag:
			global_text = f'{rand_planet} Press to enable all' if disabled_count != 0 else f'{rand_planet} Press to disable all'
		elif not all_flag:
			global_text = f'{rand_planet} Press to enable all'

		keyboard = InlineKeyboardMarkup(
			inline_keyboard = [
				[InlineKeyboardButton(text=global_text, callback_data=f'notify/toggle/all/all')],

				[InlineKeyboardButton(text='🇪🇺 EU', callback_data=f'notify/list/EU'),
				InlineKeyboardButton(text='🇺🇸 USA', callback_data=f'notify/list/USA')],

				[InlineKeyboardButton(text='🇷🇺 Russia', callback_data=f'notify/list/RUS'),
				InlineKeyboardButton(text='🇨🇳 China', callback_data=f'notify/list/CHN')],

				[InlineKeyboardButton(text='🇮🇳 India', callback_data=f'notify/list/IND'),
				InlineKeyboardButton(text='🇯🇵 Japan', callback_data=f'notify/list/JPN')],

				[InlineKeyboardButton(text='⚙️ Edit your preferences', callback_data=f'prefs/main_menu')],

				[InlineKeyboardButton(text='✅ Save and exit', callback_data=f'notify/done')]
			]
		)

		# tuple containing necessary information to edit the message
		msg_identifier = (msg['message']['chat']['id'],msg['message']['message_id'])

		# now we have the keyboard; update the previous keyboard
		if text_refresh:
			message_text = '''
			🛰 Hi there, nice to see you! Let's set some notifications for you.

			You can search for launch providers, like SpaceX (🇺🇸) or ISRO (🇮🇳), using the flags, or simply enable all!

			You can also edit your notification preferences, like your time zone, from the preferences menu (⚙️).

			🔔 = *enabled* (press to disable)
			🔕 = *disabled* (press to enable)
			'''

			try:
				bot.editMessageText(msg_identifier, text=inspect.cleandoc(message_text), reply_markup=keyboard, parse_mode='Markdown')
			except:
				pass
		else:
			try:
				bot.editMessageReplyMarkup(msg_identifier, reply_markup=keyboard)
			except:
				pass


	def update_list_view(msg, chat, provider_list):
		# get the user's current notification settings for all the providers so we can add the bell emojis
		notification_statuses = get_user_notifications_status(chat, provider_list)

		# get status for the "enable all" toggle for the country code
		providers = []
		for provider in provider_by_cc[country_code]:
			if provider in provider_name_map.keys():
				providers.append(provider_name_map[provider])
			else:
				providers.append(provider)

		notification_statuses, disabled_count = get_user_notifications_status(chat, providers), 0
		for key, val in notification_statuses.items():
			if val == 0 and key != 'All':
				disabled_count += 1
				break

		local_text = 'Press to enable all' if disabled_count != 0 else 'Press to disable all'

		# we now have the list of providers for this country code. Generate the buttons dynamically.
		inline_keyboard = [[
			InlineKeyboardButton(
				text=f'{flag_map[country_code]} {local_text}',
				callback_data=f'notify/toggle/country_code/{country_code}/{country_code}')
		]]

		'''
		dynamically creates a two-row keyboard that's as short as possible but still
		readable with the long provider names.
		'''
		provider_list.sort(key=len)
		current_row = 0 # the all-toggle is the 0th row
		for provider, i in zip(provider_list, range(len(provider_list))):
			if provider in provider_name_map.keys():
				provider_db_name = provider_name_map[provider]
			else:
				provider_db_name = provider

			notification_icon = {0:'🔕', 1:'🔔'}[notification_statuses[provider_db_name]]

			# create a new row
			if i % 2 == 0 or i == 0:
				current_row += 1
				inline_keyboard.append([
					InlineKeyboardButton(
						text=f'{provider} {notification_icon}',
						callback_data=f'notify/toggle/lsp/{provider}/{country_code}')
					])
			else:
				if len(provider) <= len('Virgin Orbit'):
					inline_keyboard[current_row].append(
						InlineKeyboardButton(
							text=f'{provider} {notification_icon}',
							callback_data=f'notify/toggle/lsp/{provider}/{country_code}')
						)
				else:
					current_row += 1
					inline_keyboard.append([
					InlineKeyboardButton(
						text=f'{provider} {notification_icon}',
						callback_data=f'notify/toggle/lsp/{provider}/{country_code}')
					])

		# append a back button, so user can return to the "main menu"
		inline_keyboard.append([InlineKeyboardButton(text='⏮ Return to menu', callback_data='notify/main_menu')])

		keyboard = InlineKeyboardMarkup(
			inline_keyboard=inline_keyboard)

		# tuple containing necessary information to edit the message
		msg_identifier = (msg['message']['chat']['id'],msg['message']['message_id'])

		# now we have the keyboard; update the previous keyboard
		bot.editMessageReplyMarkup(msg_identifier, reply_markup=keyboard)

		if chat != OWNER:
			logging.info(f'🔀 {flag_map[country_code]}-view loaded for {anonymize_id(chat)}')

		return

	try:
		query_id, from_id, query_data = telepot.glance(msg, flavor='callback_query')

	except Exception as caught_exception:
		logging.exception(f'⚠️ Exception in callback_handler: {caught_exception}')

		return

	# start timer
	start = timer()

	# verify input, assume (command/data/...) | (https://core.telegram.org/bots/api#callbackquery)
	input_data = query_data.split('/')
	chat = msg['message']['chat']['id']

	# check that the query is from an admin or an owner
	try:
		chat_type = msg['message']['chat']['type']
	except:
		chat_type = bot.getChat(chat)['type']

	if chat_type != 'private':
		try:
			all_admins = msg['message']['chat']['all_members_are_administrators']
		except:
			all_admins = False

		if not all_admins:
			sender = bot.getChatMember(chat, from_id)
			if sender['status'] != 'creator' and sender['status'] != 'administrator':
				try:
					bot.answerCallbackQuery(query_id, text="⚠️ This button is only callable by admins! ⚠️")
				except Exception as error:
					logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

				logging.info(f'✋ Callback query called by a non-admin in {anonymize_id(chat)}, returning | {(1000*(timer() - start)):.0f} ms')
				return

	# callbacks only supported for notify at the moment; verify it's a notify command
	if input_data[0] not in ('notify', 'mute', 'next_flight', 'schedule', 'prefs', 'stats'):
		logging.info(f'⚠️ Incorrect input data in callback_handler! input_data={input_data} | {(1000*(timer() - start)):.0f} ms')
		return

	# check if notification database exists
	if not os.path.isfile(os.path.join(DATA_DIR,'launchbot-data.db')):
		create_notify_database(DATA_DIR)

	flag_map = {
		'USA': '🇺🇸',
		'EU': '🇪🇺',
		'RUS': '🇷🇺',
		'CHN': '🇨🇳',
		'IND': '🇮🇳',
		'JPN': '🇯🇵',
		'IRN': '🇮🇷'
	}

	# used to update the message
	msg_identifier = (msg['message']['chat']['id'], msg['message']['message_id'])

	if input_data[0] == 'notify':
		# user requests a list of launch providers for a country code
		if input_data[1] == 'list':
			country_code = input_data[2]
			try:
				provider_list = provider_by_cc[country_code]
			except:
				logging.info(f'Error finding country code {country_code} from provider_by_cc!')
				return

			update_list_view(msg, chat, provider_list)

			try:
				bot.answerCallbackQuery(query_id, text=f'{flag_map[country_code]}')
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

		# user requests to return to the main menu; send the main keyboard
		elif input_data[1] == 'main_menu':
			try:
				if input_data[2] == 'refresh_text':
					update_main_view(chat, msg, provider_by_cc, True)
			except:
				update_main_view(chat, msg, provider_by_cc, False)

			try:
				bot.answerCallbackQuery(query_id, text='⏮ Returned to main menu')
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

			if chat != OWNER:
				logging.info(f'⏮ {anonymize_id(chat)} (main-view update) | {(1000*(timer() - start)):.0f} ms')

		# user requested to toggle a notification
		elif input_data[1] == 'toggle':
			def get_all_toggle_new_status(toggle_type, country_code, chat):
				providers = []
				if toggle_type == 'all':
					for val in provider_by_cc.values():
						for provider in val:
							providers.append(provider)

				elif toggle_type == 'country_code':
					for provider in provider_by_cc[country_code]:
						providers.append(provider)

				notification_statuses, disabled_count = get_user_notifications_status(chat, providers), 0
				for key, val in notification_statuses.items():
					if toggle_type == 'country_code' and key != 'All':
						if val == 0:
							disabled_count += 1
							break

					elif toggle_type == 'all' or toggle_type == 'lsp':
						if val == 0:
							disabled_count += 1
							break

				return 1 if disabled_count != 0 else 0

			if input_data[2] not in {'country_code', 'lsp', 'all'}:
				return

			if input_data[2] == 'all':
				all_toggle_new_status = get_all_toggle_new_status('all', None, chat)

			else:
				country_code = input_data[3]
				if input_data[2] == 'country_code':
					all_toggle_new_status = get_all_toggle_new_status('country_code', country_code, chat)
				else:
					all_toggle_new_status = 0

			# chat, type, keyword
			new_status = toggle_notification(chat, input_data[2], input_data[3], all_toggle_new_status)

			if input_data[2] == 'lsp':
				reply_text = {
					1:f'🔔 Enabled notifications for {input_data[3]}',
					0:f'🔕 Disabled notifications for {input_data[3]}'}[new_status]
			elif input_data[2] == 'country_code':
				reply_text = {
					1:f'🔔 Enabled notifications for {flag_map[input_data[3]]}',
					0:f'🔕 Disabled notifications for {flag_map[input_data[3]]}'}[new_status]
			elif input_data[2] == 'all':
				reply_text = {
					1:'🔔 Enabled all notifications 🌍',
					0:'🔕 Disabled all notifications 🌍'}[new_status]

			# give feedback to the button press
			try:
				bot.answerCallbackQuery(query_id, text=reply_text, show_alert=True)
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

			if chat != OWNER:
				logging.info(f'{anonymize_id(chat)} {reply_text} | {(1000*(timer() - start)):.0f} ms')

			# update list view if an lsp button was pressed
			if input_data[2] != 'all':
				country_code = input_data[4]
				try:
					provider_list = provider_by_cc[country_code]
				except:
					logging.info(f'Error finding country code {country_code} from provider_by_cc!')
					return

				# update keyboard list view
				update_list_view(msg, chat, provider_list)

			# update main view if "enable all" button was pressed, as in this case we're in the main view
			else:
				update_main_view(chat, msg, provider_by_cc, False)

		# user is done; remove the keyboard
		elif input_data[1] == 'done':
			# new text + callback text
			reply_text = f'✅ All done!'
			msg_text = f'✅ *All done!* Your preferences were saved.\n\n'
			msg_text += f'ℹ️ If you need to adjust your settings in the future, use /notify@{BOT_USERNAME} to access these same settings.'

			# now we have the keyboard; update the previous keyboard
			bot.editMessageText(msg_identifier, text=msg_text, reply_markup=None, parse_mode='Markdown')

			try:
				bot.answerCallbackQuery(query_id, text=reply_text)
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

			if chat != OWNER:
				logging.info(f'💫 {anonymize_id(chat)} finished setting notifications with the "Done" button! | {(1000*(timer() - start)):.0f} ms')

	elif input_data[0] == 'mute':
		# user wants to mute a launch from notification inline keyboard
		# /mute/$provider/$launch_id/(0/1) | 1=muted (true), 0=not muted

		# reverse the button status on press
		new_toggle_state = 0 if int(input_data[3]) == 1 else 1
		new_text = {0:'🔊 Unmute this launch', 1:'🔇 Mute this launch'}[new_toggle_state]
		new_data = f'mute/{input_data[1]}/{input_data[2]}/{new_toggle_state}'

		# create new keyboard
		inline_keyboard = [[InlineKeyboardButton(text=new_text, callback_data=new_data)]]
		keyboard = InlineKeyboardMarkup(
			inline_keyboard=inline_keyboard)

		# tuple containing necessary information to edit the message
		callback_text = f'🔇 Launch muted!' if input_data[3] == '1' else f'🔊 Launch unmuted!'

		try:
			bot.editMessageReplyMarkup(msg_identifier, reply_markup=keyboard)

			try:
				bot.answerCallbackQuery(query_id, text=callback_text)
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

			if chat != OWNER:
				if new_toggle_state == 0:
					logging.info(f'🔇 {anonymize_id(chat)} muted a launch for {input_data[1]} (launch_id={input_data[2]}) | {(1000*(timer() - start)):.0f} ms')
				else:
					logging.info(f'🔊 {anonymize_id(chat)} unmuted a launch for {input_data[1]} (launch_id={input_data[2]}) | {(1000*(timer() - start)):.0f} ms')

		except Exception as exception:
			logging.exception(f'⚠️ User attempted to mute/unmute a launch, but no reply could be provided (sending message...): {exception}')

			try:
				bot.sendMessage(chat, callback_text, parse_mode='Markdown')
			except Exception as exception:
				logging.exception(f'🛑 Ran into an error sending the mute/unmute message to chat={chat}! {exception}')

		# toggle the mute here, so we can give more responsive feedback
		toggle_launch_mute(chat, input_data[1], input_data[2], input_data[3])

	elif input_data[0] == 'next_flight':
		# next_flight(msg, current_index, command_invoke, cmd):
		# next_flight/{next/prev}/{current_index}/{cmd}
		# next_flight/refresh/0/{cmd}'
		if input_data[1] not in {'next', 'prev', 'refresh'}:
			logging.info(f'⚠️ Error with callback_handler input_data[1] for next_flight. input_data={input_data}')
			return

		current_index, cmd = input_data[2], input_data[3]
		if input_data[1] == 'next':
			new_message_text, keyboard = next_flight(msg, int(current_index)+1, False, cmd)

		elif input_data[1] == 'prev':
			new_message_text, keyboard = next_flight(msg, int(current_index)-1, False, cmd)

		elif input_data[1] == 'refresh':
			try:
				new_message_text, keyboard = next_flight(msg, int(current_index), False, cmd)

			except TypeError:
				new_message_text = '🔄 No launches found! Try enabling notifications for other providers, or searching for all flights.'
				inline_keyboard = []
				inline_keyboard.append([InlineKeyboardButton(text='🔔 Adjust your notification settings', callback_data=f'notify/main_menu/refresh_text')])
				inline_keyboard.append([InlineKeyboardButton(text='🔎 Search for all flights', callback_data=f'next_flight/refresh/0/all')])
				keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

				logging.info(f'🔎 No launches found after next refresh. Sent user the "No launches found" message.')

			except Exception as e:
				logging.exception(f'⚠️ No launches found after refresh! {e}')

		# query reply text
		query_reply_text = {'next': 'Next flight ⏩', 'prev': '⏪ Previous flight', 'refresh': '🔄 Refreshed data!'}[input_data[1]]

		# now we have the keyboard; update the previous keyboard
		try:
			bot.editMessageText(msg_identifier, text=new_message_text, reply_markup=keyboard, parse_mode='MarkdownV2')
		except telepot.exception.TelegramError as exception:
			if exception.error_code == 400 and 'Bad Request: message is not modified' in exception.description:
				pass
			else:
				logging.exception(f'⚠️ TelegramError updating message text: {exception}')

		try:
			bot.answerCallbackQuery(query_id, text=query_reply_text)
		except Exception as error:
			logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

		if chat != OWNER:
			logging.info(f'{anonymize_id(chat)} pressed "{query_reply_text}" button in /next | {(1000*(timer() - start)):.0f} ms')

	elif input_data[0] == 'schedule':
		#schedule/refresh
		if input_data[1] not in ('refresh', 'vehicle', 'mission'):
			return

		# pull new text and the keyboard
		if input_data[1] == 'refresh':
			try:
				new_schedule_msg, keyboard = flight_schedule(msg, False, input_data[2])
			except IndexError: # let's not break """legacy""" compatibility
				new_schedule_msg, keyboard = flight_schedule(msg, False, 'vehicle')
		else:
			new_schedule_msg, keyboard = flight_schedule(msg, False, input_data[1])

		try:
			bot.editMessageText(msg_identifier, text=new_schedule_msg, reply_markup=keyboard, parse_mode='MarkdownV2')

			if input_data[1] == 'refresh':
				query_reply_text = f'🔄 Schedule updated!'
			else:
				query_reply_text = '🚀 Vehicle schedule loaded!' if input_data[1] == 'vehicle' else '🛰 Mission schedule loaded!'

			bot.answerCallbackQuery(query_id, text=query_reply_text)

		except telepot.exception.TelegramError as exception:
			if exception.error_code == 400 and 'Bad Request: message is not modified' in exception.description:
				try:
					query_reply_text = '🔄 Schedule refreshed – no changes detected!'
					bot.answerCallbackQuery(query_id, text=query_reply_text)
				except Exception as error:
					logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')
				pass
			else:
				logging.exception(f'⚠️ TelegramError updating message text: {exception}')

	elif input_data[0] == 'prefs':
		if input_data[1] not in ('timezone', 'notifs', 'cmds', 'done', 'main_menu'):
			return

		if input_data[1] == 'done':
			bot.answerCallbackQuery(query_id, text=f'✅ All done!')
			message_text = f'💫 Your preferences were saved!'
			bot.editMessageText(msg_identifier, text=message_text, reply_markup=None, parse_mode='Markdown')

		elif input_data[1] == 'main_menu':
			rand_planet = random.choice(('🌍', '🌎', '🌏'))
			bot.answerCallbackQuery(query_id, text=f'⏮ Main menu')
			message_text = f'''
			⚙️ *This tool* allows you to edit your chat's preferences.

			*These include...*
			⏰ Launch notification types (24 hour/12 hour etc.)
			{rand_planet} Time zone settings
			🛰 Command permissions

			Your time zone is used when sending notifications to show your local time, instead of the default UTC+0.
			
			*Note:* command permission support is coming later.
			'''

			keyboard = InlineKeyboardMarkup(
							inline_keyboard = [
								[InlineKeyboardButton(text=f'{rand_planet} Time zone settings', callback_data=f'prefs/timezone/menu')],
								[InlineKeyboardButton(text='⏰ Notification settings', callback_data=f'prefs/notifs')],
								[InlineKeyboardButton(text='⏮ Back to main menu', callback_data=f'notify/main_menu/refresh_text')]
							]
						)

			'''
			keyboard = InlineKeyboardMarkup(
						inline_keyboard = [
							[InlineKeyboardButton(text=f'{rand_planet} Timezone settings', callback_data=f'prefs/timezone')],
							[InlineKeyboardButton(text='⏰ Notification settings', callback_data=f'prefs/notifs')],
							[InlineKeyboardButton(text='🛰 Command settings', callback_data=f'prefs/cmds')],
							[InlineKeyboardButton(text='✅ Exit', callback_data=f'prefs/done')]
						]
					)
			'''

			bot.editMessageText(msg_identifier, text=inspect.cleandoc(message_text), reply_markup=keyboard, parse_mode='Markdown')

		elif input_data[1] == 'notifs':
			if len(input_data) == 3:
				if input_data[2] in ('24h', '12h', '1h', '5m'):
					new_state = update_notif_preference(chat, input_data[2])
					query_reply_text = f'{input_data[2]} notifications '
					query_reply_text = query_reply_text.replace('h', ' hour') if 'h' in query_reply_text else query_reply_text.replace('m', ' minute')
					query_reply_text += 'enabled 🔔' if new_state == 1 else 'disabled 🔕'

					bot.answerCallbackQuery(query_id, text=query_reply_text, show_alert=True)

			# load notification preferences for chat, and map to emoji
			notif_prefs = get_notif_preference(chat)
			bell_dict = {1: '🔔', 0: '🔕'}

			new_prefs_text = f'''
			⏰ *Notification settings*

			By default, notifications are sent 24h, 12h, 1h, and 5 minutes before a launch. 

			You can change this behavior here.

			🔔 = currently enabled
			🔕 = *not* enabled
			'''

			keyboard = InlineKeyboardMarkup(
				inline_keyboard = [
					[InlineKeyboardButton(text=f'24 hours before {bell_dict[notif_prefs[0]]}', callback_data=f'prefs/notifs/24h')],
					[InlineKeyboardButton(text=f'12 hours before {bell_dict[notif_prefs[1]]}', callback_data=f'prefs/notifs/12h')],
					[InlineKeyboardButton(text=f'1 hour before {bell_dict[notif_prefs[2]]}', callback_data=f'prefs/notifs/1h')],
					[InlineKeyboardButton(text=f'5 minutes before {bell_dict[notif_prefs[3]]}', callback_data=f'prefs/notifs/5m')],
					[InlineKeyboardButton(text='⏮ Return to menu', callback_data=f'prefs/main_menu')]
				]
			)

			bot.editMessageText(msg_identifier, text=inspect.cleandoc(new_prefs_text), reply_markup=keyboard, parse_mode='Markdown')

		elif input_data[1] == 'timezone':
			if input_data[2] == 'menu':
				text = f'''🌎 This tool allows you to set your time zone so notifications can show your local time.

				*Choose which method you'd like to use:*
				- *manual:* no DST support, not recommended.
				
				- *automatic:* uses your location to define your locale (e.g. Europe/Berlin). DST support.

				Your current time zone is *UTC{load_time_zone_status(chat, readable=True)}*'''

				locale_string = load_locale_string(chat)
				if locale_string is not None:
					text += f' *({locale_string})*'

				keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='🌎 Automatic setup', callback_data=f'prefs/timezone/auto_setup')],
						[InlineKeyboardButton(text='🕹 Manual setup', callback_data=f'prefs/timezone/manual_setup')],
						[InlineKeyboardButton(text='🗑 Remove my time zone', callback_data=f'prefs/timezone/remove')],
						[InlineKeyboardButton(text='⏮ Back to menu', callback_data=f'prefs/main_menu')]
					]
				)

				bot.editMessageText(msg_identifier, text=inspect.cleandoc(text), reply_markup=keyboard, parse_mode='Markdown')
				bot.answerCallbackQuery(query_id, '🌎 Time zone settings loaded')


			elif input_data[2] == 'manual_setup':
				current_time_zone = load_time_zone_status(chat, readable=True)

				text = f'''🌎 This tool allows you to set your time zone so notifications can show your local time.
							
				⚠️ *Note:* you need to reset your time zone when your time zone enters/exits DST!

				Need help? https://www.timeanddate.com/time/map/

				Use the buttons below to set the UTC offset to match your time zone.

				🕗 Your time zone is set to: *UTC{current_time_zone}*
				'''

				keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[
							InlineKeyboardButton(text='-5 hours', callback_data='prefs/timezone/set/-5h'),
							InlineKeyboardButton(text='-1 hour', callback_data='prefs/timezone/set/-1h'),
							InlineKeyboardButton(text='+1 hour', callback_data='prefs/timezone/set/+1h'),
							InlineKeyboardButton(text='+5 hours', callback_data='prefs/timezone/set/+5h')
						],
						[
							InlineKeyboardButton(text='-15 minutes', callback_data='prefs/timezone/set/-15m'),
							InlineKeyboardButton(text='+15 minutes', callback_data='prefs/timezone/set/+15m'),
						],
						[InlineKeyboardButton(text='⏮ Back to menu', callback_data='prefs/main_menu')]
					]
				)

				bot.editMessageText(
					msg_identifier, text=inspect.cleandoc(text), parse_mode='Markdown',
					reply_markup=keyboard, disable_web_page_preview=True
				)

			elif input_data[2] == 'start':
				if bot.getChat(chat)['type'] != 'private':
					bot.sendMessage(chat, text=f'⚠️ This method only works for private chats. This is a Telegram API limitation.')

				new_text = '🌎 Set your time zone with the button below, where your keyboard should be. To cancel, select "cancel time zone setup" from the message above.'

				# construct the keyboard so we can request a location
				keyboard = ReplyKeyboardMarkup(
					resize_keyboard=True,
					one_time_keyboard=True,
					keyboard=[
						[KeyboardButton(text='📍 Set your time zone', request_location=True)]
					]
				)

				new_inline_text = '❗️ To cancel time zone setup and remove the keyboard, use the button below.'
				inline_keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='🚫 Cancel time zone setup', callback_data='prefs/timezone/cancel')]
					]
				)

				bot.editMessageText(msg_identifier, text=new_inline_text, reply_markup=inline_keyboard, parse_mode='Markdown')
				sent_message = bot.sendMessage(chat_id=chat, text=new_text, reply_markup=keyboard, parse_mode='Markdown')
				bot.editMessageReplyMarkup((sent_message['chat']['id'], sent_message['message_id']), ForceReply(selective=True))
				bot.answerCallbackQuery(query_id, text=f'🌎 Time zone setup loaded')

				#time_zone_setup_users.append(chat)

			elif input_data[2] == 'cancel':
				rand_planet = random.choice(('🌍', '🌎', '🌏'))
				message_text = f'''
				⚙️ *This tool* allows you to edit your chat's preferences.

				These include...
				⏰ Launch notification types (24 hour/12 hour etc.)
				{rand_planet} Your time zone
				🛰 Command permissions

				Your time zone is used when sending notifications to show your local time, instead of the default UTC+0.
				
				*Note:* time zone and command permission support is coming later.
				'''

				sent_message = bot.sendMessage(
					chat, inspect.cleandoc(message_text),
					parse_mode='Markdown',
					reply_markup=ReplyKeyboardRemove(remove_keyboard=True)
				)

				msg_identifier = (sent_message['chat']['id'], sent_message['message_id'])
				bot.deleteMessage(msg_identifier)

				keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='⏰ Notification settings', callback_data=f'prefs/notifs')],
						[InlineKeyboardButton(text='⏮ Back to main menu', callback_data=f'notify/main_menu/refresh_text')]
					]
				)

				sent_message = bot.sendMessage(
					chat, inspect.cleandoc(message_text),
					parse_mode='Markdown',
					reply_markup=keyboard
				)

				bot.answerCallbackQuery(query_id, text=f'✅ Operation canceled!')

			elif input_data[2] == 'set':
				update_time_zone_value(chat, input_data[3])
				current_time_zone = load_time_zone_status(chat, readable=True)

				text = f'''🌎 This tool allows you to set your time zone so notifications can show your local time.

				Need help? https://www.timeanddate.com/time/map/

				Use the buttons below to set the UTC offset to match your time zone.

				🕗 Your time zone is set to: *UTC{current_time_zone}*
				'''

				keyboard = InlineKeyboardMarkup(inline_keyboard = [
					[
						InlineKeyboardButton(text='-5 hours', callback_data=f'prefs/timezone/set/-5h'),
						InlineKeyboardButton(text='-1 hour', callback_data=f'prefs/timezone/set/-1h'),
						InlineKeyboardButton(text='+1 hour', callback_data=f'prefs/timezone/set/+1h'),
						InlineKeyboardButton(text='+5 hours', callback_data=f'prefs/timezone/set/+5h')
					],
					[
						InlineKeyboardButton(text='-15 minutes', callback_data=f'prefs/timezone/set/-15m'),
						InlineKeyboardButton(text='+15 minutes', callback_data=f'prefs/timezone/set/+15m'),
					],
					[InlineKeyboardButton(text='⏮ Back to menu', callback_data=f'prefs/main_menu')]
					]
				)

				bot.answerCallbackQuery(query_id, text=f'🌎 Time zone set to UTC{current_time_zone}')
				bot.editMessageText(
					msg_identifier, text=inspect.cleandoc(text), reply_markup=keyboard,
					parse_mode='Markdown', disable_web_page_preview=True
				)

			elif input_data[2] == 'auto_setup':
				# send message with ForceReply()
				text = '''🌎 Automatic time zone setup

				⚠️ Your exact location is *NOT* stored or logged anywhere. You can remove your time zone at any time.

				Your coordinates are converted to a locale, e.g. Europe/Berlin, or America/Lima, which is used for the UTC off-set. This allows us to support DST.
				
				🌎 *To set your time zone, do the following:*
				1. make sure you're replying to *this* message
				2. tap the file attachment button to the left of the text field (📎)
				3. choose "location"
				4. send the bot an approximate location, but *make sure* it's within the same time zone as you are in
				'''

				bot.deleteMessage(msg_identifier)
				sent_message = bot.sendMessage(
					chat, text=inspect.cleandoc(text),
					reply_markup=ForceReply(selective=True), parse_mode='Markdown')

				time_zone_setup_chats[chat] = [sent_message['message_id'], from_id]

			elif input_data[2] == 'remove':
				remove_time_zone_information(chat)
				bot.answerCallbackQuery(query_id, f'✅ Your time zone information was deleted from the server', show_alert=True)

				text = f'''🌎 This tool allows you to set your time zone so notifications can show your local time.

				*Choose which method you'd like to use:*
				- *manual:* no DST support, not recommended.
				
				- *automatic:* uses your location to define your locale (e.g. Europe/Berlin). DST support.

				Your current time zone is *UTC{load_time_zone_status(chat, readable=True)}*
				'''

				keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='🌎 Automatic setup', callback_data=f'prefs/timezone/auto_setup')],
						[InlineKeyboardButton(text='🕹 Manual setup', callback_data=f'prefs/timezone/manual_setup')],
						[InlineKeyboardButton(text='🗑 Remove my time zone', callback_data=f'prefs/timezone/remove')],
						[InlineKeyboardButton(text='⏮ Back to menu', callback_data=f'prefs/main_menu')]
					]
				)

				try:
					bot.editMessageText(msg_identifier, text=inspect.cleandoc(text), reply_markup=keyboard, parse_mode='Markdown')
				except:
					pass


	elif input_data[0] == 'stats':
		if input_data[1] == 'refresh':
			if chat != OWNER:
				logging.info(f'🔄 {anonymize_id(chat)} refreshed statistics')

			new_text = inspect.cleandoc(statistics(chat, 'refresh'))
			if msg['message']['text'] == new_text.replace('*',''):
				bot.answerCallbackQuery(query_id, text='🔄 Statistics are up to date!')
				return

			keyboard = InlineKeyboardMarkup(
				inline_keyboard=[[InlineKeyboardButton(text='🔄 Refresh statistics', callback_data='stats/refresh')]])

			bot.editMessageText(msg_identifier, text=new_text, reply_markup=keyboard, parse_mode='Markdown')
			bot.answerCallbackQuery(query_id, text='🔄 Statistics refreshed!')

	# update stats, except if command was a stats refresh
	if input_data[0] != 'stats':
		update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)


def timer_handle(command, chat, user):
	''' Summary
	Restrict command send frequency to avoid spam, by storing
	user IDs and when they have called a command in memory.
	'''
	# remove the '/' command prefix
	command = command.strip('/')
	chat = str(chat)

	if '@' in command:
		command = command.split('@')[0]

	# get current time
	now_called = datetime.datetime.today()

	# load timer for command (command_cooldowns)
	try:
		cooldown = float(command_cooldowns['commandTimers'][command])
	except KeyError:
		command_cooldowns['commandTimers'][command] = '0.35'
		cooldown = float(command_cooldowns['commandTimers'][command])

	if cooldown <= -1:
		return False

	# checking if the command has been called previously (chat_command_calls)
	# load time the command was previously called
	if chat not in chat_command_calls:
		chat_command_calls[chat] = {}

	# never called, set to 0
	if command not in chat_command_calls[chat]:
		chat_command_calls[chat][command] = '0'

	try:
		last_called = chat_command_calls[chat][command]
	except KeyError:
		if chat not in chat_command_calls:
			chat_command_calls[chat] = {}

		if command not in chat_command_calls[chat]:
			chat_command_calls[chat][command] = '0'

		last_called = chat_command_calls[chat][command]

	if last_called == '0': # never called; store now
		chat_command_calls[chat][command] = str(now_called) # stringify datetime object, store

	else:
		last_called = datetime.datetime.strptime(last_called, "%Y-%m-%d %H:%M:%S.%f") # unstring datetime object
		time_since = abs(now_called - last_called)

		if time_since.seconds > cooldown:
			chat_command_calls[chat][command] = str(now_called) # stringify datetime object, store
		else:
			class Spammer:
				def __init__(self, uid):
					self.id = uid
					self.offenses = 1
					self.spam_times = [timer()]


				def get_offenses(self):
					return self.offenses

				def add_offense(self):
					self.offenses += 1
					self.spam_times.append(timer())

				def clear_offenses(self):
					self.offenses = 0
					self.spam_times = []

				def offense_delta(self):
					pass

			spammer = next((spammer for spammer in spammers if spammer.id == user), None)
			if spammer is not None:
				spammer.add_offense()

				logging.info(f'⚠️ User {anonymize_id(user)} now has {spammer.get_offenses()} spam offenses.')

				if spammer.get_offenses() >= 10:
					run_time = datetime.datetime.now() - STARTUP_TIME
					if run_time.seconds > 60:
						ignored_users.add(user)
						logging.info(f'⚠️⚠️⚠️ User {anonymize_id(user)} is now ignored due to excessive spam!')

						bot.sendMessage(
							chat,
							'⚠️ *Please do not spam the bot.* Your user ID has been blocked and all commands by you will be ignored for an indefinite amount of time.',
							parse_mode='Markdown')
					else:
						logging.info(f'''✅ Successfully avoided blocking a user on bot startup! Run_time was {run_time.seconds} seconds.
							Spam offenses set to 0 for user {anonymize_id(user)} from original {spammer.get_offenses()}''')
						spammer.clear_offenses()

					return False

			else:
				spammers.add(Spammer(user))
				logging.info(f'⚠️ Added user {anonymize_id(user)} to spammers.')

			return False

	return True


def chat_preferences(chat):
	'''
	This function is called when user wants to interact with their preferences.
	Sends the user an interactive keyboard to view and edit their prefs with.

	Functions:
	- set timezone
	- set notification times
	- allow/disallow regular users to call bot's commands
	'''
	if not os.path.isfile(os.path.join(DATA_DIR, 'launchbot-data.db')):
		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		c = conn.cursor()
		try:
			# chat - notififcations - postpone - timezone - commands
			c.execute("CREATE TABLE preferences (chat TEXT, notifications TEXT, timezone TEXT, timezone_str TEXT, postpone INTEGER, commands TEXT, PRIMARY KEY (chat))")
			conn.commit()
		except sqlite3.OperationalError:
			pass

		conn.close()

	rand_planet = random.choice(('🌍', '🌎', '🌏'))
	message_text = f'''
	⚙️ *This tool* allows you to edit your chat's preferences.

	These include...
	⏰ Launch notification types (24 hour/12 hour etc.)
	{rand_planet} Your time zone
	🛰 Command permissions

	Your time zone is used when sending notifications to show your local time, instead of the default UTC+0.
	
	Note: time zone and command permission support is coming later.
	'''

	'''
	keyboard = InlineKeyboardMarkup(
				inline_keyboard = [
					[InlineKeyboardButton(text=f'{rand_planet} Timezone settings', callback_data=f'prefs/timezone')],
					[InlineKeyboardButton(text='⏰ Notification settings', callback_data=f'prefs/notifs')],
					[InlineKeyboardButton(text='🛰 Command settings', callback_data=f'prefs/cmds')],
					[InlineKeyboardButton(text='✅ Exit', callback_data=f'prefs/done')]
				]
			)
	'''

	keyboard = InlineKeyboardMarkup(
				inline_keyboard = [
					[InlineKeyboardButton(text='⏰ Notification settings', callback_data=f'prefs/notifs')],
					[InlineKeyboardButton(text='✅ Exit', callback_data=f'prefs/done')]
				]
			)

	bot.sendMessage(
		chat, inspect.cleandoc(message_text),
		parse_mode='Markdown',
		reply_markup=keyboard
		)


def name_from_provider_id(lsp_id):
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	c = conn.cursor()

	# get provider name corresponding to this ID
	c.execute("SELECT lsp_name FROM launches WHERE lsp_id = ?",(provider_id,))
	query_return = c.fetchall()

	if len(query_return) != 0:
		return query_return[0][0]

	return provider_id


def notify(msg):
	content_type, chat_type, chat = telepot.glance(msg, flavor='chat')

	# check if notification database exists
	if not os.path.isfile(os.path.join(DATA_DIR, 'launchbot-data.db')):
		create_notify_database(DATA_DIR)

	# send the user the base keyboard where we start working up from.
	message_text = f'''
	🛰 Hi there, nice to see you! Let's set some notifications for you.

	You can search for launch providers, like SpaceX (🇺🇸) or ISRO (🇮🇳), using the flags, or simply enable all!

	You can also edit your notification preferences, like your time zone, from the preferences menu (⚙️).

	🔔 = *currently enabled*
	🔕 = *currently disabled*
	'''

	# figure out what the text for the "enable all/disable all" button should be
	providers = []
	for val in provider_by_cc.values():
		for provider in val:
			providers.append(provider)

	notification_statuses, disabled_count = get_user_notifications_status(chat, providers), 0
	disabled_count = 1 if 0 in notification_statuses.values() else 0

	rand_planet = random.choice(('🌍', '🌎', '🌏'))
	global_text = f'{rand_planet} Press to enable all' if disabled_count != 0 else f'{rand_planet} Press to disable all'
	keyboard = InlineKeyboardMarkup(
			inline_keyboard = [
				[InlineKeyboardButton(text=global_text, callback_data=f'notify/toggle/all/all')],
				
				[InlineKeyboardButton(text='🇪🇺 EU', callback_data=f'notify/list/EU'),
				InlineKeyboardButton(text='🇺🇸 USA', callback_data=f'notify/list/USA')],
				
				[InlineKeyboardButton(text='🇷🇺 Russia', callback_data=f'notify/list/RUS'),
				InlineKeyboardButton(text='🇨🇳 China', callback_data=f'notify/list/CHN')],

				[InlineKeyboardButton(text='🇮🇳 India', callback_data=f'notify/list/IND'),
				InlineKeyboardButton(text='🇯🇵 Japan', callback_data=f'notify/list/JPN')],

				[InlineKeyboardButton(text='⚙️ Edit your preferences', callback_data=f'prefs/main_menu')],
				
				[InlineKeyboardButton(text='✅ Save and exit', callback_data=f'notify/done')]
			]
		)

	bot.sendMessage(
		chat, inspect.cleandoc(message_text),
		parse_mode='Markdown',
		reply_markup=keyboard
		)


def feedback(msg):
	'''
	Receive feedback from users.
	'''
	content_type, chat_type, chat = telepot.glance(msg, flavor='chat')

	# feedback called by $chat; send the user a message with ForceReply in it, so we can get a response
	message_text = f'''
	✍️ *Hi there!* This is a way of sharing feedback and reporting issues to the developer of the bot. All feedback is anonymous.

	Please note that it is impossible for me to reply to your feedback, but you can be sure I'll read it!
	Just write your feedback *as a reply to this message* (otherwise I won't see it due to the bot's privacy settings)
	'''

	ret = bot.sendMessage(
		chat, inspect.cleandoc(message_text), parse_mode='Markdown',
		reply_markup=ForceReply(selective=True), reply_to_message_id=msg['message_id'])

	feedback_message_IDs.add(ret['message_id'])


def flight_schedule(msg, command_invoke, call_type):
	'''
	Display a very simple schedule for all upcoming flights.
	'''
	if command_invoke:
		content_type, chat_type, chat = telepot.glance(msg, flavor='chat')
	else:
		chat = msg['message']['chat']['id']

	# open db connection
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
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
		9: 'September', 10: 'October', 11: 'November', 12: 'December' }

	# if a shortened name makes no sense, use this
	providers_short = {
		'RL': 'Rocket Lab',
		'RFSA': 'Roscosmos',
		'VO': 'Virgin Orbit'}

	flag_map = {
		'FR': '🇪🇺', 'USA': '🇺🇸', 'EU': '🇪🇺', 'RUS': '🇷🇺',
		'CHN': '🇨🇳', 'IND': '🇮🇳', 'JPN': '🇯🇵', 'IRN': '🇮🇷',
		'FRA': '🇪🇺'}

	vehicle_map = {
		'Falcon 9 Block 5': 'Falcon 9 B5'}

	# pick 5 dates, map missions into dict with dates
	sched_dict = {}
	for row, i in zip(query_return, range(len(query_return))):
		launch_unix = datetime.datetime.utcfromtimestamp(row[9])
		#launch_unix += 3600 * load_time_zone_status(chat, readable=False)

		provider = row[3] if len(row[3]) <= len('Arianespace') else row[4]
		mission = row[0]

		verified_date = True if row[20] == 0 else False
		verified_time = True if row[21] == 0 else False

		if mission[0] == ' ':
			mission = mission[1:]

		if '(' in mission:
			mission = mission[0:mission.index('(')]

		if provider in providers_short.keys():
			provider = providers_short[provider]

		vehicle = row[5].split('/')[0]

		country_code, flag = row[8], None
		if country_code in flag_map.keys():
			flag = flag_map[country_code]

		# shorten some vehicle names
		if vehicle in vehicle_map.keys():
			vehicle = vehicle_map[vehicle]

		# shorten monospaced text length
		provider = ' '.join("`{}`".format(word) for word in provider.split(' '))
		vehicle = ' '.join("`{}`".format(word) for word in vehicle.split(' '))
		mission = ' '.join("`{}`".format(word) for word in mission.split(' '))

		# start the string with the flag of the provider's country
		flt_str = flag if flag is not None else ''

		# add a button indicating the status of the launch
		if verified_date and verified_time:
			flt_str += '🟢'
		else:
			flt_str += '🟡'

		if call_type == 'vehicle':
			flt_str += f' {provider} {vehicle}'

		elif call_type == 'mission':
			flt_str += f' {mission}'

		utc_str = f'{launch_unix.year}-{launch_unix.month}-{launch_unix.day}'

		if utc_str not in sched_dict:
			if len(sched_dict.keys()) == 5:
				break

			sched_dict[utc_str] = [flt_str]
		else:
			sched_dict[utc_str].append(flt_str)

	schedule_msg, i = '', 0
	today = datetime.datetime.utcnow()
	for key, val in sched_dict.items():
		if i != 0:
			schedule_msg += '\n\n'

		# create the date string; key in the form of year-month-day
		ymd_split = key.split('-')
		try:
			if int(ymd_split[2]) in {11, 12, 13}:
				suffix = 'th'
			else:
				suffix = {1: 'st', 2: 'nd', 3: 'rd'}[int(str(ymd_split[2])[-1])]
		except:
			suffix = 'th'

		# calc how many days until this date
		launch_date = datetime.datetime.strptime(key, '%Y-%m-%d')
		time_delta = abs(launch_date - today)

		if (launch_date.day, launch_date.month) == (today.day, today.month):
			eta_days = 'today'

		else:
			if launch_date.month == today.month:
				if launch_date.day - today.day == 1:
					eta_days = 'tomorrow'
				else:
					eta_days = f'in {launch_date.day - today.day} days'
			else:
				sec_time = time_delta.seconds + time_delta.days * 3600 * 24
				days = math.floor(sec_time/(3600*24))
				hours = (sec_time/(3600) - math.floor(sec_time/(3600*24))*24)
				
				if today.hour + hours >= 24:
					days += 1
				
				eta_days = f'in {days+1} days'

		eta_days = provider = ' '.join("`{}`".format(word) for word in eta_days.split(' '))

		schedule_msg += f'*{month_map[int(ymd_split[1])]} {ymd_split[2]}{suffix}* {eta_days}\n'
		for mission, j in zip(val, range(len(val))):
			if j != 0:
				schedule_msg += '\n'

			schedule_msg += mission

			if j == 2 and len(val) > 3:
				upcoming_flight_count = 'flight' if len(val) - 3 == 1 else 'flights'
				schedule_msg += f'\n+ {len(val)-3} more {upcoming_flight_count}'
				break

		i += 1

	schedule_msg = reconstruct_message_for_markdown(schedule_msg)
	header = f'📅 *5\-day flight schedule*\n'
	header_note = f'Dates are subject to change. For detailed flight information, use /next@{BOT_USERNAME}.'
	footer_note = '\n\n🟢 = verified launch date\n🟡 = exact time to be determined'
	footer = f'_{reconstruct_message_for_markdown(footer_note)}_'
	header_info = f'_{reconstruct_message_for_markdown(header_note)}\n\n_'
	schedule_msg = header + header_info + schedule_msg + footer

	# call change button
	switch_text = '🚀 Vehicles' if call_type == 'mission' else '🛰 Missions'

	inline_keyboard = []
	inline_keyboard.append([
		InlineKeyboardButton(text='🔄 Refresh', callback_data=f'schedule/refresh/{call_type}'),
		InlineKeyboardButton(text=switch_text, callback_data=f"schedule/{'mission' if call_type == 'vehicle' else 'vehicle'}")]
		)

	keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

	if not command_invoke:
		return schedule_msg, keyboard

	bot.sendMessage(chat, schedule_msg, reply_markup=keyboard, parse_mode='MarkdownV2')
	return


# handles /next by polling the launch database
def next_flight(msg, current_index, command_invoke, cmd):
	if command_invoke:
		content_type, chat_type, chat = telepot.glance(msg, flavor='chat')
		command_split = msg['text'].strip().split(" ")
		cmd = ' '.join(command_split[1:])

		if cmd == ' ' or cmd == '':
			cmd = None

		elif cmd == 'all':
			pass

		elif len(difflib.get_close_matches(cmd, ['all'])) == 1:
			cmd = 'all'

		else:
			resp_str = '⚠️ Not a valid query type – currently supported queries are `/next` and `/next all`.'
			resp_str += '\n\n`/next` shows the next launch you have enabled notifications for.'
			bot.sendMessage(chat, resp_str, parse_mode='Markdown')
			return
	else:
		chat = msg['message']['chat']['id']
		if cmd == 'None':
			cmd = None

	# load UTC offset
	utc_offset = 3600 * load_time_zone_status(chat, readable=False)

	# if command was "all", no need to perform a special select
	# if no command, we'll need to figure out what LSPs the user has set notifs for
	notify_conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	notify_cursor = notify_conn.cursor()

	try:
		notify_cursor.execute('''SELECT * FROM notify WHERE chat = ?''', (chat,))
	except:
		create_notify_database(DATA_DIR)

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
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	c = conn.cursor()

	# datetimes
	today_unix = time.mktime(datetime.datetime.today().timetuple())

	# perform the select; if cmd == all, just pull the next launch
	if cmd == 'all':
		c.execute('SELECT * FROM launches WHERE NET >= ?',(today_unix,))
		query_return = c.fetchall()

	# if no next command, assume the user wants to know the next launch they're interested in
	elif cmd is None:
		if all_flag:
			if len(disabled) > 0:
				query_str = f"SELECT * FROM launches WHERE NET >= {today_unix} AND lsp_name NOT IN ({','.join(['?']*len(disabled))})"
				c.execute(query_str, disabled)
				query_return = c.fetchall()

				query_str = f"SELECT * FROM launches WHERE NET >= {today_unix} AND lsp_short NOT IN ({','.join(['?']*len(disabled))})"
				ret = c.fetchall()
				for i in ret:
					query_return.append(i)

			else:
				c.execute('SELECT * FROM launches WHERE NET >= ?',(today_unix,))
				query_return = c.fetchall()
		else:
			query_str = f"SELECT * FROM launches WHERE NET >= {today_unix} AND lsp_name IN ({','.join(['?']*len(notif_providers))})"
			c.execute(query_str, notif_providers)
			query_return = c.fetchall()

			query_str = f"SELECT * FROM launches WHERE NET >= {today_unix} AND lsp_short IN ({','.join(['?']*len(notif_providers))})"
			c.execute(query_str, notif_providers)
			ret = c.fetchall()
			for i in ret:
				query_return.append(i)

	# sort ascending by NET, pick smallest
	max_index = len(query_return)
	if max_index > 0:
		query_return.sort(key=lambda tup: tup[9])
		try:
			query_return = query_return[current_index]
		except Exception as error:
			logging.exception(f'⚠️ Exception setting query_return: {error}')
			query_return = query_return[0]
	else:
		msg_text = '🔄 No launches found! Try enabling notifications for other providers, or searching for all flights.'
		inline_keyboard = []
		inline_keyboard.append([InlineKeyboardButton(text='🔔 Adjust your notification settings', callback_data='notify/main_menu/refresh_text')])
		inline_keyboard.append([InlineKeyboardButton(text='🔎 Search for all flights', callback_data='next_flight/refresh/0/all')])
		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

		bot.sendMessage(chat, msg_text, reply_markup=keyboard)
		logging.info('🔎 No launches found in next. Sent user the "No launches found" message.')

		return

	# close connection
	conn.close()

	# pull relevant info from query return
	mission_name = query_return[0].strip()
	lsp_id = query_return[2]
	lsp_name = query_return[3]
	lsp_short = query_return[4]
	vehicle_name = query_return[5]
	pad_name = query_return[6]
	info = query_return[7]
	country_code = query_return[8]
	flight_unix_net = query_return[9]

	# new info
	tbd_date = query_return[20]
	tbd_time = query_return[21]
	launch_prob = query_return[22]
	in_hold = query_return[18]

	tbd_date = bool(tbd_date == 1)
	tbd_time = bool(tbd_time == 1)
	in_hold = bool(in_hold == 1)

	if lsp_name == 'SpaceX':
		# spx_info_str, spx_orbit_info = spx_info_str_gen(launch_name, 0, utc_timestamp)

		logging.info(f'Next of SpX launch: calling spx_info_str_gen with ({launch_name}, 0, {utc_timestamp})')

		spx_info_str, spx_orbit_info = spx_info_str_gen(mission_name, 0, flight_unix_net)
		if spx_info_str is not None:
			spx_str = True
		else:
			spx_str = False
	else:
		spx_str = False

	'''
	print(f'lsp_name: {lsp_name}')
	print(f'lsp_short: {lsp_short}')
	print(f'cmd: {cmd}')
	print(f'lsp_name in enabled? {lsp_name in enabled}')
	print(f'lsp_name in disabled? {lsp_name in disabled}')
	print(f'lsp_short in enabled? {lsp_short in enabled}')
	print(f'lsp_short in disabled? {lsp_short in disabled}')
	'''

	if cmd == 'all' and lsp_name in disabled:
		user_notif_enabled = False

	# check if user has notifications enabled
	if user_notif_enabled is None:
		if lsp_name in enabled or lsp_short in enabled:
			user_notif_enabled = True
		elif len(difflib.get_close_matches(lsp_name, enabled)) == 1:
			user_notif_enabled = True
		elif len(difflib.get_close_matches(lsp_short, enabled)) == 1:
			user_notif_enabled = True
		elif lsp_name in disabled or lsp_short in disabled:
			user_notif_enabled = False
		else:
			logging.info(f'⚠️ failed to set user_notif_enabled: lsp: {lsp_name}, diff: {difflib.get_close_matches(lsp_name, notif_providers)}\
					, notif_providers: {notif_providers}')
			user_notif_enabled = False

	# load UTC offset if available
	utc_timestamp = query_return[9]
	utc_timestamp = utc_timestamp + utc_offset
	launch_unix = datetime.datetime.utcfromtimestamp(utc_timestamp)

	if launch_unix.minute < 10:
		min_time = f'0{launch_unix.minute}'
	else:
		min_time = launch_unix.minute

	launch_time = f'{launch_unix.hour}:{min_time}'

	if tbd_time is True:
		launch_time = f'launch time TBD'

	net_stamp = datetime.datetime.fromtimestamp(query_return[9])
	eta = abs(datetime.datetime.today() - net_stamp)

	if eta.days >= 365: # over 1 year
		t_y = math.floor(eta.days/365)
		t_m = math.floor(t_y*365 - eta.days)

		year_suff = 'year' if t_y == 1 else 'years'
		month_suff = 'month' if t_m == 1 else 'months'
		eta_str = f'{t_y} {year_suff}, {t_m} {month_suff}'

	elif eta.days < 365 and eta.days >= 31: # over 1 month
		t_m = math.floor(eta.days/30)
		t_d = math.floor(eta.days - t_m*30)

		month_suff = 'month' if t_m == 1 else 'months'
		day_suff = 'day' if t_d == 1 else 'days'
		eta_str = f'{t_m} {month_suff}, {t_d} {day_suff}'

	elif eta.days >= 1 and eta.days < 31: # over a day
		t_d = eta.days
		t_h = math.floor(eta.seconds/3600)
		t_m = math.floor((eta.seconds-t_h*3600)/60)

		day_suff = 'day' if t_d == 1 else 'days'
		min_suff = 'minute' if t_m == 1 else 'minutes'
		h_suff = 'hour' if t_h == 1 else 'hours'
		eta_str = f'{t_d} {day_suff}, {t_h} {h_suff}, {t_m} {min_suff}'

	elif (eta.seconds/3600) < 24 and (eta.seconds/3600) >= 1: # under a day, more than an hour
		t_h = math.floor(eta.seconds/3600)
		t_m = math.floor((eta.seconds-t_h*3600)/60)
		t_s = math.floor(eta.seconds-t_h*3600-t_m*60)

		h_suff = 'hour' if t_h == 1 else 'hours'
		min_suff = 'minute' if t_m == 1 else 'minutes'
		s_suff = 'second' if t_s == 1 else 'seconds'
		eta_str = f'{t_h} {h_suff}, {t_m} {min_suff}, {t_s} {s_suff}'

	elif (eta.seconds/3600) < 1:
		t_m = math.floor(eta.seconds/60)
		t_s = math.floor(eta.seconds-t_m*60)

		min_suff = 'minute' if t_m == 1 else 'minutes'
		s_suff = 'second' if t_s == 1 else 'seconds'

		if t_m > 0:
			eta_str = f'{t_m} {min_suff}, {t_s} {s_suff}'
		elif t_m == 0:
			if t_s <= 10:
				if t_s > 0:
					eta_str = f'T-{t_s}, terminal countdown'
				else:
					if t_s == 0:
						eta_str = 'T-0, launch commit'
					else:
						eta_str = 'T-0'
			else:
				eta_str = f'T- {t_s} {s_suff}'

	flag_map = {
		'FRA':	'🇪🇺',
		'FR': 	'🇪🇺',
		'USA': 	'🇺🇸',
		'EU': 	'🇪🇺',
		'RUS': 	'🇷🇺',
		'CHN': 	'🇨🇳',
		'IND': 	'🇮🇳',
		'JPN': 	'🇯🇵',
		'IRN':	'🇮🇷'
	}

	if int(lsp_id) in LSP_IDs:
		lsp_name = LSP_IDs[int(lsp_id)][0]
		lsp_flag = LSP_IDs[int(lsp_id)][1]
	else:
		if len(lsp_name) > len('Virgin Orbit'):
			lsp_name = lsp_short
		try:
			lsp_flag = flag_map[country_code]
		except:
			lsp_flag = None

	# parse pad to convert common names to shorter ones
	if 'LC-' not in pad_name:
		pad_name = pad_name.replace('Space Launch Complex ', 'SLC-').replace('Launch Complex ', 'LC-')

	# inform the user whether they'll be notified or not
	if user_notif_enabled:
		notify_str = '🔔 You will be notified of this launch!'
	else:
		notify_str = f'🔕 You will *not* be notified of this launch.\nℹ️ *To enable:* /notify@{BOT_USERNAME}'

	if info is not None:
		# if the info text is longer than 60 words, pick the first three sentences.
		if len(info.split(' ')) > 60:
			info = f'{". ".join(info.split(". ")[0:2])}.'

		if 'DM2' in mission_name:
			info = 'A new era of human spaceflight is set to begin as 🇺🇸-astronauts once again launch to orbit on a 🇺🇸-rocket from 🇺🇸-soil, almost a decade after the retirement of the Space Shuttle fleet in 2011.'
			mission_name = 'SpX-DM2'
		elif 'Starlink' in mission_name and '8' not in mission_name:
			info = "60 satellites for the Starlink satellite constellation, SpaceX's project for providing global, high-speed internet access."

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
		9: 'September', 10: 'October', 11: 'November', 12: 'December'
	}

	try:
		if int(launch_unix.day) in {11, 12, 13}:
			suffix = 'th'
		else:
			suffix = {1: 'st', 2: 'nd', 3: 'rd'}[int(str(launch_unix.day)[-1])]
	except:
		suffix = 'th'

	date_str = f'{month_map[launch_unix.month]} {launch_unix.day}{suffix}'
	date_str = ' '.join("`{}`".format(word) for word in date_str.split(' '))

	# construct the message
	if lsp_flag is not None:
		header = f'🚀 *Next launch* is by {lsp_name} {lsp_flag}\n*Mission* {mission_name}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'
	else:
		header = f'🚀 *Next launch* is by {lsp_name}\n*Mission* {mission_name}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'

	if lsp_name.replace('`','') == 'SpaceX':
		if spx_orbit_info not in {'', None}:
			orbit_map = {
			'VLEO': 'Very low-Earth orbit',
			'SO': 'Sub-orbital',
			'LEO': 'Low-Earth orbit',
			'SSO': 'Sun-synchronous',
			'MEO': 'Medium-Earth orbit',
			'GEO': 'Geostationary (direct)',
			'GTO': 'Geostationary (transfer)',
			'ISS': 'ISS'
			}

			if spx_orbit_info in orbit_map.keys():
				spx_orbit_info = orbit_map[spx_orbit_info]
				spx_orbit_info = ' '.join("`{}`".format(word) for word in spx_orbit_info.split(' '))
			else:
				spx_orbit_info = f'`{spx_orbit_info}`'

			header += f'\n*Orbit* {spx_orbit_info}'

	if tbd_date is False: # verified launch date
		if tbd_time is False: # verified launch time
			# load UTC offset in readable format
			readable_utc_offset = load_time_zone_status(chat, readable=True)

			time_str = f'📅 {date_str}`,` `{launch_time} UTC{readable_utc_offset} `\n⏱ {eta_str}'
		else: # unverified launch time
			launch_time = ' '.join("`{}`".format(word) for word in launch_time.split(' '))
			time_str = f'📅 {date_str}`,` {launch_time}\n⏱ {eta_str}'

	else: # unverified launch date
		time_str = f'🗓 `Not` `` `before` `` {date_str}\n⏱ {eta_str}'

	# add probability if found
	if launch_prob != -1 and launch_prob is not None:
		if launch_prob >= 80:
			prob_str = f'☀️ {launch_prob} % probability of launch'
		elif launch_prob >= 60:
			prob_str = f'🌤 {launch_prob} % probability of launch'
		elif launch_prob >= 40:
			prob_str = f'🌥 {launch_prob} % probability of launch'
		elif launch_prob >= 20:
			prob_str = f'☁️ {launch_prob} % probability of launch'
		else:
			prob_str = f'🌪 {launch_prob} % probability of launch'

		prob_str = ' '.join("`{}`".format(word) for word in prob_str.split(' '))
		time_str += f'\n{prob_str}'

	elif in_hold:
		prob_str = f'🟡 Countdown on hold'
		prob_str = ' '.join("`{}`".format(word) for word in prob_str.split(' '))
		time_str += f'\n{prob_str}'

	# if close to launch, include video url if possible
	vid_url = query_return[19]
	if vid_url != '' and eta.seconds <= 3600 and eta.days == 0:
		vid_str = '🔴 Watch the launch LinkTextGoesHere'
	elif vid_url != '' and eta.seconds <= 3600*4 and 'DM2' in mission_name and eta.days == 0:
		vid_str = '🔴 Watch the launch LinkTextGoesHere'
	else:
		vid_str = None

	# not a spx launch, or no info available
	if not spx_str:
		if info_msg is not None:
			msg_text = f'{header}\n\n{time_str}\n\n{info_msg}\n'
		else:
			msg_text = f'{header}\n\n{time_str}\n'

	# spx info string provided
	else:
		if info_msg is not None:
			msg_text = f'{header}\n\n{time_str}\n\n{spx_info_str}\n\n{info_msg}\n'

		else:
			msg_text = f'{header}\n\n{time_str}\n\n{spx_info_str}\n'

	# add vid_str if needed
	if vid_str is not None:
		msg_text += f'\n{vid_str}'

	# add notify str
	msg_text += f'\n{notify_str}'

	# reconstruct
	msg_text = reconstruct_message_for_markdown(msg_text)

	# add proper URL, format for MarkdownV2
	if vid_str is not None:
		link_text = reconstruct_link_for_markdown(vid_url)
		msg_text = msg_text.replace('LinkTextGoesHere', f'[live\!]({link_text})')

	if max_index > 1:
		inline_keyboard = [[]]
		back, fwd = False, False

		if current_index != 0:
			back = True
			inline_keyboard[0].append(
					InlineKeyboardButton(text='⏪ Previous', callback_data=f'next_flight/prev/{current_index}/{cmd}'))

		# if we can go forward, add a next button
		if current_index+1 < max_index:
			fwd = True
			inline_keyboard[0].append(InlineKeyboardButton(text='Next ⏩', callback_data=f'next_flight/next/{current_index}/{cmd}'))

		# if the length is one, make the button really wide
		if len(inline_keyboard[0]) == 1:
			# only forwards, so the first entry; add a refresh button
			if fwd:
				inline_keyboard = [[]]
				inline_keyboard[0].append(InlineKeyboardButton(text='🔄 Refresh', callback_data=f'next_flight/refresh/0/{cmd}'))
				inline_keyboard[0].append(InlineKeyboardButton(text='Next ⏩', callback_data=f'next_flight/next/{current_index}/{cmd}'))
			elif back:
				inline_keyboard = [([InlineKeyboardButton(text='⏪ Previous', callback_data=f'next_flight/prev/{current_index}/{cmd}')])]
				inline_keyboard.append([(InlineKeyboardButton(text='⏮ First', callback_data=f'next_flight/prev/1/{cmd}'))])

		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

	elif max_index == 1:
		inline_keyboard = []
		inline_keyboard.append([InlineKeyboardButton(text='🔄 Refresh', callback_data=f'next_flight/prev/1/{cmd}')])
		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

	if current_index == 0 and command_invoke:
		if max_index > 1:
			bot.sendMessage(chat, msg_text, parse_mode='MarkdownV2', reply_markup=keyboard)
		else:
			bot.sendMessage(chat, msg_text, parse_mode='MarkdownV2')
	else:
		return msg_text, keyboard

	return


def statistics(chat: str, mode: str):
	'''
	Return statistics for LaunchBot.
	'''
	# read stats db
	stats_conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	stats_conn.row_factory = sqlite3.Row
	stats_cursor = stats_conn.cursor()

	try:
		# select stats field
		stats_cursor.execute("SELECT * FROM stats")
		stats = [dict(row) for row in stats_cursor.fetchall()][0]

		# parse returned global data
		notifs = stats['notifications']
		api_reqs = stats['api_requests']
		db_updates = stats['db_updates']
		commands = stats['commands']
		data = stats['data']
		last_db_update = stats['last_api_update']

	except sqlite3.OperationalError:
		notifs = api_reqs = db_updates = commands = data = last_db_update = 0

	# get system load average
	load_avgs = os.getloadavg() # [1 min, 5 min, 15 min]
	load_avg_str = f'{load_avgs[0]:.2f} {load_avgs[1]:.2f} {load_avgs[2]:.2f}'

	# format transfered API data to MB, GB
	data_suffix = 'GB' if data/10**9 >= 1 else 'MB'
	data = data/10**9 if data/10**9 >= 1 else data/10**6

	# get amount of stored data
	try:
		db_storage = 0.00
		db_storage += os.path.getsize(os.path.join(DATA_DIR, 'launchbot-data.db'))
		db_storage += os.path.getsize(os.path.join(DATA_DIR, 'bot-settings.json'))
		db_storage += os.path.getsize(os.path.join(DATA_DIR, 'log-file.log'))
	except:
		db_storage = 0.00

	# format stored data to MB, GB
	db_storage_prefix = 'GB' if db_storage/10**9 >= 1 else 'MB'
	db_storage = db_storage/10**9 if db_storage/10**9 >= 1 else db_storage/10**6

	# convert time since last db update to a readable ETA, add suffix
	last_db_update = time_delta_to_legible_eta(int(time.time()) - last_db_update)
	last_db_update_suffix = 'ago' if last_db_update not in {'never', 'just now'} else ''

	# connect to notifications db
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	cursor = conn.cursor()

	# pull all rows with enabled = 1
	cursor.execute('SELECT chat FROM notify WHERE enabled = 1')
	notification_recipients = len(set(row[0] for row in cursor.fetchall()))

	# close conn
	conn.close()

	reply_str = f'''
	📊 *LaunchBot global statistics*
	Notifications delivered: {notifs}
	Notification recipients: {notification_recipients}
	Commands parsed: {commands}

	🛰 *Network statistics*
	Data transferred: {data:.2f} {data_suffix}
	API requests made: {api_reqs}

	💾 *Database information*
	Storage used: {db_sizes:.2f} {db_size_class}
	Last updated: {last_db_update} {last_db_update_suffix}

	🎛 *Server information*
	Uptime {time_delta_to_legible_eta(uptime())}
	Load {load_avg_str}
	LaunchBot version *{VERSION}* 🚀
	'''

	# if we're refreshing an existing message, return now
	if mode == 'refresh':
		return inspect.cleandoc(reply_str)

	# if a new message, add a keyboard for refreshing
	keyboard = InlineKeyboardMarkup(
		inline_keyboard=[[InlineKeyboardButton(
			text='🔄 Refresh statistics', callback_data='stats/refresh')]])

	# send message
	bot.sendMessage(chat, inspect.cleandoc(reply_str), reply_markup=keyboard, parse_mode='Markdown')


# if running for the first time
def first_run():
	print("Looks like you're running LaunchBot for the first time!")
	print("Let's start off by creating some folders.")
	time.sleep(2)

	# create directories
	if not os.path.isdir(DATA_DIR):
		os.makedirs(DATA_DIR)
		print("Folders created!\n")

	time.sleep(1)

	print('To function, LaunchBot needs a bot API key;')
	print('to get one, send a message to @botfather on Telegram.')

	# create a settings file for the bot; we'll store the API keys here
	if not os.path.isfile(os.path.join(DATA_DIR, 'bot-settings.json')):
		update_token(['botToken'])
		time.sleep(2)
		print('\n')


# update bot token
def update_token(update_tokens):
	# create /data and /chats
	if not os.path.isdir(DATA_DIR):
		first_run()

	if not os.path.isfile(os.path.join(DATA_DIR, 'bot-settings.json')):
		with open(os.path.join(DATA_DIR, 'bot-settings.json'), 'w') as json_data:
			setting_map = {} # empty .json file
	else:
		with open(os.path.join(DATA_DIR, 'bot-settings.json'), 'r') as json_data:
				setting_map = json.load(json_data) # use old .json

	if 'botToken' in update_tokens:
		token_input = str(input('Enter the bot token for LaunchBot: '))
		while ':' not in token_input:
			print('Please try again – bot-tokens look like "123456789:ABHMeJViB0RHL..."')
			token_input = str(input('Enter the bot token for launchbot: '))

		setting_map['botToken'] = token_input

	with open(os.path.join(DATA_DIR, 'bot-settings.json'), 'w') as json_data:
		json.dump(setting_map, json_data, indent=4)

	time.sleep(2)
	print('Token update successful!\n')


def sigterm_handler(signal, frame):
	logging.info(f'✅ Got SIGTERM. Runtime: {datetime.datetime.now() - STARTUP_TIME}.')
	sys.exit(0)


if __name__ == '__main__':
	# some global vars for use in other functions
	global TOKEN, OWNER, VERSION, BOT_ID, BOT_USERNAME
	global DATA_DIR, STARTUP_TIME
	global bot

	# current version
	VERSION = '1.6-alpha'

	# default start mode, log start time
	start = debug_mode = False
	STARTUP_TIME = datetime.datetime.now()

	# list of args the program accepts
	start_args = {'start', '-start'}
	debug_args = {'log', '-log', 'debug', '-debug'}
	bot_token_args = {'newbottoken', '-newbottoken'}

	if len(sys.argv) == 1:
		print('''Give at least one of the following arguments:
		\tlaunchbot.py [-start, -newBotToken, -log]\n
		
		E.g.: python3 launchbot.py -start
		\t-start starts the bot
		\t-newBotToken changes the bot API token
		\t-log stores some logs\n''')
		sys.exit('Program stopping...')

	else:
		update_tokens = set()
		for arg in sys.argv:
			if arg.lower() in start_args:
				start = True

			# update tokens if instructed to
			if arg in bot_token_args:
				update_tokens.add('botToken')

			if arg in debug_args:
				if arg in ('log', '-log'):
					if not os.path.isdir(DATA_DIR):
						first_run()

					log = os.path.join(DATA_DIR, 'log-file.log')

					# disable logging for urllib and requests because jesus fuck they make a lot of spam
					logging.getLogger('requests').setLevel(logging.CRITICAL)
					logging.getLogger('urllib3').setLevel(logging.CRITICAL)
					logging.getLogger('schedule').setLevel(logging.CRITICAL)
					logging.getLogger('chardet.charsetprober').setLevel(logging.CRITICAL)
					logging.getLogger('telepot.exception.TelegramError').setLevel(logging.CRITICAL)

					# start log
					logging.basicConfig(
						filename=log, level=logging.DEBUG, format='%(asctime)s %(message)s',
						datefmt='%d/%m/%Y %H:%M:%S')

				if arg in ('debug', '-debug'):
					debug_mode = True

		if len(update_tokens) != 0:
			update_token(update_tokens)

		if start is False:
			sys.exit('No start command given – exiting. To start the bot, include -start in startup options.')

	# if data folder isn't found, we haven't run before (or someone pressed the wrong button)
	if not os.path.isdir(DATA_DIR):
		first_run()

	try:
		bot_settings_path = os.path.join(DATA_DIR, 'bot-settings.json')
		with open(bot_settings_path, 'r') as json_data:
			try:
				setting_map = json.load(json_data)
			except:
				os.remove(os.path.join(DATA_DIR, 'bot-settings.json'))
				first_run()

	except FileNotFoundError:
		first_run()

		with open(bot_settings_path, 'r') as json_data:
			setting_map = json.load(json_data)

	# token for the Telegram API; get from args or as a text file
	if len(setting_map['botToken']) == 0 or ':' not in setting_map['botToken']:
		first_run()
	else:
		TOKEN = setting_map['botToken']

		try:
			OWNER = setting_map['owner']
		except:
			OWNER = 0

	# create the bot
	bot = telepot.Bot(TOKEN)

	# handle ssl exceptions
	ssl._create_default_https_context = ssl._create_unverified_context

	# get the bot's username and id
	bot_specs = bot.getMe()
	BOT_USERNAME = bot_specs['username']
	BOT_ID = bot_specs['id']

	# valid commands we monitor for
	global VALID_COMMANDS
	VALID_COMMANDS = {
		'/start', '/help', '/next', '/notify',
		'/statistics', '/schedule', '/feedback'
	}

	# generate the "alternate" commands we listen for, as in ones suffixed with the bot's username
	alt_commands = set()
	for command in VALID_COMMANDS:
		alt_commands.add(f'{command}@{BOT_USERNAME.lower()}')

	VALID_COMMANDS = VALID_COMMANDS.union(alt_commands)

	# all the launch providers supported; used in many places, so declared globally here
	global provider_by_cc
	provider_by_cc = {
		'USA': [
			'NASA',
			'SpaceX',
			'ULA',
			'Rocket Lab Ltd',
			'Astra Space',
			'Virgin Orbit',
			'Firefly Aerospace',
			'Northrop Grumman',
			'International Launch Services'],

		'EU': [
			'Arianespace',
			'Eurockot',
			'Starsem SA'],

		'CHN': [
			'CASC',
			'ExPace'],

		'RUS': [
			'KhSC',
			'ISC Kosmotras',
			'Russian Space Forces',
			'Eurockot',
			'Sea Launch',
			'Land Launch',
			'Starsem SA',
			'International Launch Services',
			'ROSCOSMOS'],

		'IND': [
			'ISRO',
			'Antrix Corporation'],

		'JPN': [
			'JAXA',
			'Mitsubishi Heavy Industries',
			'Interstellar Technologies']
	}

	global provider_name_map
	provider_name_map = {
		'Rocket Lab': 'Rocket Lab Ltd',
		'Northrop Grumman': 'Northrop Grumman Innovation Systems',
		'ROSCOSMOS': 'Russian Federal Space Agency (ROSCOSMOS)'
	}

	global time_zone_setup_chats
	time_zone_setup_chats = {}

	''' LSP ID -> name, flag dictionary
	Used to shorten the names, so we don't end up with super long messages

	This dictionary also maps custom shortened names (Northrop Grumman, Starsem)
	to their real ID. Also used in cases where a weird name is used by LL, like...
		RFSA for Roscosmos
	'''
	global LSP_IDs
	LSP_IDs = {
	121: 	['SpaceX', '🇺🇸'],
	147: 	['Rocket Lab', '🇺🇸'],
	265:	['Firefly', '🇺🇸']
	99: 	['Northrop Grumman', '🇺🇸'],
	115: 	['Arianespace', '🇪🇺'],
	124: 	['ULA', '🇺🇸'],
	98: 	['Mitsubishi Heavy Industries', '🇯🇵'],
	1002:	['Interstellar Tech.', '🇯🇵']
	88: 	['CASC', '🇨🇳'],
	190: 	['Antrix Corporation', '🇮🇳'],
	122: 	['Sea Launch', '🇷🇺'],
	118: 	['ILS', '🇺🇸🇷🇺'],
	193: 	['Eurockot', '🇪🇺🇷🇺'],
	119:	['ISC Kosmotras', '🇷🇺🇺🇦🇰🇿'],
	123:	['Starsem', '🇪🇺🇷🇺'],
	194:	['ExPace', '🇨🇳'],
	63:		['Roscosmos', '🇷🇺']
	}

	# start command timers, store in memory instead of storage to reduce disk writes
	global command_cooldowns, chat_command_calls, spammers, ignored_users
	command_cooldowns, chat_command_calls = {}, {}
	spammers, ignored_users = set(), set()

	# initialize the timer dict to avoid spam
	command_cooldowns['commandTimers'] = {}
	for command in VALID_COMMANDS:
		command_cooldowns['commandTimers'][command.replace('/','')] = '1'

	# init the feedback store; used to store the message IDs so we can store feedback
	global feedback_message_IDs
	feedback_message_IDs = set()

	MessageLoop(bot, {'chat': handle, 'callback_query': callback_handler}).run_as_thread()
	time.sleep(1)

	if not debug_mode:
		print(f"| LaunchBot.py version {VERSION}")
		print("| Don't close this window or set the computer to sleep. Quit: ctrl + c.")
		time.sleep(0.5)
		sys.stdout.write('%s\r' % '  Connected to Telegram! ✅')

	# schedule regular database updates and NET checks
	schedule.every(2).minutes.do(get_launch_updates, launch_ID=None)
	schedule.every(2).minutes.do(spx_api_handler)
	schedule.every(30).seconds.do(launch_update_check)

	# run all scheduled jobs now, so we don't have to sit in the dark for a while
	get_launch_updates(None)
	spx_api_handler()
	launch_update_check()

	# handle sigterm
	signal.signal(signal.SIGTERM, sigterm_handler)

	# fancy prints so the user can tell that we're actually doing something
	if not debug_mode:
		# hide cursor for pretty print
		cursor.hide()

		try:
			while True:
				schedule.run_pending()
				for char in ('|', '/', '—', '\\'):
					sys.stdout.write('%s\r' % char)
					sys.stdout.flush()
					time.sleep(1)

		except KeyboardInterrupt:
			# on exit, show cursor as otherwise it'll stay hidden
			cursor.show()
			sys.exit(f'Program ending... Runtime: {datetime.datetime.now() - STARTUP_TIME}.')

	else:
		while True:
			schedule.run_pending()
			time.sleep(3)
