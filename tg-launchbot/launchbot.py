'''
launchbot.py is the main module used by launchbot. The module handles
all command and callback query requests, and is responsible for starting
API and notification scheduling.
'''
import os
import sys
import time
import datetime
import logging
import math
import inspect
import random
import sqlite3
import signal
import argparse

from timeit import default_timer as timer

import git
import psutil
import cursor
import pytz
import coloredlogs
import telegram
import redis

from uptime import uptime
from timezonefinder import TimezoneFinder
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.events import EVENT_JOB_ERROR
from telegram import ReplyKeyboardRemove, ForceReply
from telegram import InlineKeyboardButton, InlineKeyboardMarkup
from telegram.ext import Updater, CommandHandler, MessageHandler, Filters
from telegram.ext import CallbackQueryHandler

from api import api_call_scheduler
from config import load_config, store_config
from db import (update_stats_db, create_chats_db)
from utils import (
	anonymize_id, time_delta_to_legible_eta, map_country_code_to_flag,
	timestamp_to_legible_date_string, short_monospaced_text,
	reconstruct_message_for_markdown, reconstruct_link_for_markdown,
	suffixed_readable_int, retry_after)
from timezone import (
	load_locale_string, remove_time_zone_information, update_time_zone_string,
	update_time_zone_value, load_time_zone_status)
from notifications import (
	get_user_notifications_status, toggle_notification,
	update_notif_preference, get_notif_preference, toggle_launch_mute, clean_chats_db)


# redis db connection
rd = redis.Redis(host='localhost', port=6379, db=0, decode_responses=True)

try:
	# verify redis connection so we don't run into issues later on
	ret = rd.setex(name='foo', value='bar', time=datetime.timedelta(seconds=1))
except redis.exceptions.ConnectionError:
	sys.exit('🛑 Error connecting to redis instance! Verify redis-server configuration.')


def api_update_on_restart():
	'''
	Updates launch database to force API update on next bot restart.
	Used as both a CLI argument and through Telegram.
	'''
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	cursor_ = conn.cursor()

	try:
		cursor_.execute('UPDATE stats SET last_api_update = ?', (None,))
	except sqlite3.OperationalError:
		logging.warning('Database doesn\'t exist! Ignoring flag...')

	conn.commit()
	conn.close()


def admin_handler(update, context):
	'''
	Allow bot owner to export logs and database remotely. Can only be called
	in private chat with owner.
	'''
	def restart_program():
		'''
		Restarts the current program, with file objects and descriptors
		cleanup
		'''
		try:
			proc = psutil.Process(os.getpid())
			for handler in proc.open_files() + proc.connections():
				os.close(handler.fd)
		except Exception as error:
			logging.error(f'Error in restart_program: {error}')

		python = sys.executable
		os.execl(python, python, *sys.argv)

	# extract chat information
	chat = update.message.chat

	# return logs if command used
	if update.message.text == '/debug export-logs':
		log_path = os.path.join(DATA_DIR, 'log-file.log')
		log_fsz = os.path.getsize(log_path) / 10**6

		context.bot.send_message(
			chat_id=chat.id, text=f'🔄 Exporting logs (`{log_fsz:.2f} MB`)...',
			parse_mode='Markdown')

		logging.info('🔄 Exporting logs...')
		with open(log_path, 'rb') as log_file:
			context.bot.send_document(
				chat_id=chat.id, document=log_file,
				filename=f'log-export-{int(time.time())}.log')

	elif update.message.text == '/debug export-db':
		context.bot.send_message(chat_id=chat.id, text='🔄 Exporting database...')
		logging.info('🔄 Exporting database...')

		with open(os.path.join(DATA_DIR, 'launchbot-data.db'), 'rb') as db_file:
			context.bot.send_document(
				chat_id=chat.id, document=db_file,
				filename=f'db-export-{int(time.time())}.db')

	elif update.message.text == '/debug restart':
		running = time_delta_to_legible_eta(int(time.time() - STARTUP_TIME), True)
		logging.info(f'⚠️ Restarting program... Runtime: {running}.')

		context.bot.send_message(
			chat_id=chat.id, text=f'⚠️ *Restarting...* Runtime: {running}.',
			parse_mode='Markdown')
		restart_program()

	elif update.message.text == '/debug git-pull':
		context.bot.send_message(
				chat_id=chat.id, text='🐙 Pulling master...', parse_mode='Markdown')

		repo = git.Repo('../')
		current = repo.head.commit
		repo.remotes.origin.pull()

		if current != repo.head.commit:
			last_commit = repo.heads[0].commit.hexsha
			context.bot.send_message(
				chat_id=chat.id, text=f'🐙 Git pull completed!\n\nLast commit: `{last_commit}`',
				parse_mode='Markdown')
		else:
			context.bot.send_message(
				chat_id=chat.id, text='🐙 Git pull completed: no changes.',
				parse_mode='Markdown')

		repo.close()

	elif update.message.text == '/debug force-api-update':
		logging.info('⚠️ Updating stats to enable immediate API update...')
		api_update_on_restart()
		context.bot.send_message(chat_id=chat.id, text='⚠️ DB updated for immediate API update')

	else:
		args_list = (
			'`export-logs`', '`export-db`', '`force-api-update`', '`git-pull`', '`restart`')

		context.bot.send_message(
			chat_id=chat.id,
			parse_mode='Markdown',
			text=f'ℹ️ *Invalid input!* Arguments: {", ".join(args_list)}.')


def generic_update_handler(update, context):
	'''
	[Description here]
	'''
	if update.message is None:
		if update.channel_post is not None:
			return

		logging.warning(f'update.message == none!\n{update}')
		return

	chat = update.message.chat
	if update.message.left_chat_member not in (None, False):
		if update.message.left_chat_member.id == BOT_ID:
			# bot kicked; remove corresponding chat IDs from notification database
			conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
			cursor_ = conn.cursor()

			try:
				cursor_.execute("DELETE FROM chats WHERE chat = ?", (chat.id,))
			except Exception as error:
				logging.exception(f'⚠️ Error removing chat from chats db! {error}')

			conn.commit()
			conn.close()

			logging.info(
				f'⚠️ Bot removed from chat {anonymize_id(chat.id)} – notifications database cleaned [2]')

	elif update.message.group_chat_created not in (None, False):
		# a new group chat created, with the bot in it
		logging.info('✨ Group chat created! (update.message.group_chat_created is not None)')
		start(update, context)

	elif update.message.migrate_from_chat_id not in (None, False):
		# chat migrated from id in the migrate obj. to chat.id
		logging.info(f'✅ Chat migrated from {update.message.migrate_from_chat_id} to {chat.id}')
		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		cursor_ = conn.cursor()

		try:
			cursor_.execute('UPDATE chats SET chat = ? WHERE chat = ?',
				(chat.id, update.message.migrate_from_chat_id))
		except Exception:
			logging.exception(f'Unable to migrate {update.message.migrate_from_chat_id} to {chat.id}!')

		conn.commit()
		conn.close()

	elif update.message.new_chat_members not in (None, False):
		if BOT_ID in [user.id for user in update.message.new_chat_members]:
			# bot added to the chat
			logging.info('✨ Bot added to group! (update.message.new_chat_member.id == BOT_ID)')
			start(update, context)


def command_pre_handler(update, context, skip_timer_handle):
	'''
	Before every command is processed, command_pre_handler is ran.
	The purpose is to filter out spam and unallowed callers, update
	statistics, handle exceptions, etc.
	'''
	# extract chat information
	try:
		chat = update.message.chat
	except AttributeError:
		logging.warning(f'Unable to set chat: update.message has not property! {update}')
		return False

	# verify that the user who sent this is not in spammers
	if update.message.from_user.id in ignored_users:
		logging.info('😎 Message from spamming user ignored successfully')
		return False

	# all users don't have a user ID, so check for the regular username as well
	if update.message.author_signature in ignored_users:
		logging.info('😎 Message from spamming user (no UID) ignored successfully')
		return False

	''' for admin/private chat checks; also might throw an error when kicked out of a group,
	so handle that here as well '''
	try:
		try:
			chat_type = chat.type
		except KeyError:
			chat_type = context.bot.getChat(chat).type

	except telegram.error.RetryAfter as error:
			''' Rate-limited by Telegram
			https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this '''
			logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
			retry_after(error.retry_after)

			return False

	except telegram.error.TimedOut as error:
		logging.exception('🚧 Got a telegram.error.TimedOut!.')
		return False

	except telegram.error.ChatMigrated as error:
		logging.info(f'⚠️ Group {anonymize_id(chat.id)} migrated to {anonymize_id(error.new_chat_id)}')

		# establish connection
		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		cursor_ = conn.cursor()

		# replace old ids with new ids
		cursor_.execute("UPDATE chats SET chat = ? WHERE chat = ?", (error.new_chat_id, chat.id))
		conn.commit()
		conn.close()

		logging.info('✅ Chat data migration complete!')
		return True

	except telegram.error.Unauthorized as error:
		logging.info('⚠️ Unauthorized in command_pre_handler')

		# known error: clean the chat from the chats db
		logging.info('🗃 Cleaning chats database...')
		clean_chats_db(DATA_DIR, chat.id)

		# succeeded in (not) sending the message
		return False

	except telegram.error.TelegramError as error:
		'''
		These exceptions are effectively only triggered when we're handling a message
		_after_ the bot has been kicked, e.g. after a bot restart.
		'''
		if 'chat not found' in error.message:
			logging.exception(f'⚠️ Chat {anonymize_id(chat.id)} not found.')

		elif 'bot was blocked' in error.message:
			logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat.id)}.')

		elif 'user is deactivated' in error.message:
			logging.exception(f'⚠️ User {anonymize_id(chat.id)} was deactivated.')

		elif 'bot was kicked from the supergroup chat' in error.message:
			logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat.id)}.')

		elif 'bot is not a member of the supergroup chat' in error.message:
			logging.exception(f'⚠️ Bot was kicked from supergroup {anonymize_id(chat.id)}.')

		elif "Can't parse entities" in error.message:
			logging.exception('🛑 Error parsing message markdown!')
			return False

		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		cursor_ = conn.cursor()

		cursor_.execute("DELETE FROM chats WHERE chat = ?", (chat.id),)
		conn.commit()
		conn.close()

		logging.info(f'⚠️ Bot removed from chat {anonymize_id(chat.id)} – notifications database cleaned [1]')
		return False

	# filter spam
	try:
		cmd = update.message.text.split(' ')[0]
	except AttributeError:
		logging.warning(f'(ignored) Error setting value for cmd (AttrError). Update.message: {update.message}')
		return True
	except KeyError:
		logging.warning(f'Error setting value for cmd (KeyError). Update.message: {update.message}')
		return False

	if not skip_timer_handle:
		if not timer_handle(update, context, cmd, chat.id, update.message.from_user.id):
			blocked_user = update.message.from_user.id
			blocked_chat = chat.id

			logging.info(f'✋ [{cmd}] Spam prevented from {blocked_chat} by {blocked_user}.')
			return False

	# check if sender is an admin/creator, and/or if we're in a public chat
	if chat_type != 'private':
		try:
			all_admins = update.message.chat.all_members_are_administrators
		except Exception:
			all_admins = False

		if not all_admins:
			sender = context.bot.get_chat_member(chat.id, update.message.from_user.id)
			if sender.status not in ('creator', 'administrator'):
				# check for bot's admin status and whether we can remove the message
				bot_chat_specs = context.bot.get_chat_member(chat.id, context.bot.getMe().id)
				if bot_chat_specs.status == 'administrator':
					try:
						if context.bot.deleteMessage(chat.id, update.message.message_id):
							logging.info(f'✋ {cmd} called by a non-admin in {anonymize_id(chat.id)} ({anonymize_id(update.message.from_user.id)}): successfully deleted message! ✅')
						else:
							logging.info(f'✋ {cmd} called by a non-admin in {anonymize_id(chat.id)} ({anonymize_id(update.message.from_user.id)}): unable to delete message (success != True. Type:{type(success)}, val:{success}) ⚠️')
					except telegram.error.RetryAfter as error:
						# sleep for a while
						logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
						retry_after(error.retry_after)

						try:
							if context.bot.deleteMessage(chat.id, update.message.message_id):
								logging.info(f'✋ {cmd} called by a non-admin in {anonymize_id(chat.id)}: removed!')
							else:
								logging.info(f'✋ {cmd} called by a non-admin in {anonymize_id(chat.id)}: failed to remove!')
						except Exception:
							pass

					except telegram.error.BadRequest:
						logging.warning('Unable to remove message sent by non-admin due to BadRequest')

					except Exception as error:
						logging.exception(f'⚠️ Could not delete message sent by non-admin: {error}')

				else:
					logging.info(f'✋ {cmd} called by a non-admin in {anonymize_id(chat.id)} ({anonymize_id(update.message.from_user.id)}): could not remove.')

				return False

	return True


def callback_handler(update, context):
	'''
	Handles responses to callbacks.
	'''
	def update_main_view(chat, msg, text_refresh):
		'''
		Updates the main view for the notify message.
		'''
		# figure out what the text for the "enable all/disable all" button should be
		providers = set()
		for val in provider_by_cc.values():
			for provider in val:
				if provider in provider_name_map.keys():
					providers.add(provider_name_map[provider])
				else:
					providers.add(provider)

		notification_statuses = get_user_notifications_status(
			DATA_DIR, chat, providers, provider_name_map)

		disabled_count, all_flag = 0, False
		if 0 in notification_statuses.values():
			disabled_count = 1

		try:
			if notification_statuses['All'] == 1:
				all_flag = True
		except KeyError:
			pass

		rand_planet = random.choice(('🌍', '🌎', '🌏'))

		if all_flag:
			toggle_text = 'enable' if disabled_count != 0 else 'disable'
		elif not all_flag:
			toggle_text = 'enable'

		global_text = f'{rand_planet} Press to {toggle_text} all'

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

		# now we have the keyboard; update the previous keyboard
		if text_refresh:
			message_text = '''
			🚀 *LaunchBot* | Notification settings

			You can search for launch providers, like SpaceX (🇺🇸) or ISRO (🇮🇳), using the flags, or simply enable all!

			You can also edit your notification preferences, like your time zone, from the preferences menu (⚙️).

			🔔 = *currently ON* (press to disable)
			🔕 = *currently OFF* (press to enable)
			'''

			try:
				query.edit_message_text(text=inspect.cleandoc(message_text),
					reply_markup=keyboard, parse_mode='Markdown')
			except:
				logging.exception('Error updating main view message text!')
		else:
			try:
				query.edit_message_reply_markup(reply_markup=keyboard)
			except telegram.error.BadRequest:
				pass
			except:
				logging.exception('Error updating main view message reply markup!')


	def update_list_view(msg, chat, provider_list):
		'''
		Updates the country_code list view in the notify message.
		'''
		# get the user's current notification settings for all the providers so we can add the bell emojis
		notification_statuses = get_user_notifications_status(
			DATA_DIR, chat, provider_list, provider_by_cc)

		# get status for the "enable all" toggle for the country code
		providers = []
		for provider in provider_by_cc[country_code]:
			if provider in provider_name_map.keys():
				providers.append(provider_name_map[provider])
			else:
				providers.append(provider)

		notification_statuses = get_user_notifications_status(DATA_DIR, chat, providers, provider_by_cc)
		disabled_count = 0
		for key, val in notification_statuses.items():
			if val == 0 and key != 'All':
				disabled_count += 1
				break

		local_text = 'Press to enable all' if disabled_count != 0 else 'Press to disable all'

		# we now have the list of providers for this country code. Generate the buttons dynamically.
		inline_keyboard = [[
			InlineKeyboardButton(
				text=f'{map_country_code_to_flag(country_code)} {local_text}',
				callback_data=f'notify/toggle/country_code/{country_code}/{country_code}')
		]]

		# in the next part we need to sort the provider_list, which is a set: convert to a list
		provider_list = list(provider_list)

		''' dynamically creates a two-row keyboard that's as short as possible but still
		readable with the long provider names. '''
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
		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

		# now we have the keyboard; update the previous keyboard
		try:
			query.edit_message_reply_markup(reply_markup=keyboard)
		except telegram.error.BadRequest:
			pass

		if chat != OWNER:
			logging.info(f'🔀 {map_country_code_to_flag(country_code)}-view loaded for {anonymize_id(chat)}')

	try:
		query = update.callback_query
		query_data = update.callback_query.data
		from_id = update.callback_query.from_user.id
	except Exception as caught_exception:
		logging.exception(f'⚠️ Exception in callback_handler: {caught_exception}')
		return

	# start timer
	start = timer()

	# verify input, assume (command/data/...) | (https://core.telegram.org/bots/api#callbackquery)
	input_data = query_data.split('/')
	msg = update.callback_query.message
	chat = msg.chat.id

	# check that the query is from an admin or an owner
	try:
		chat_type = msg.chat.type
	except:
		chat_type = context.bot.getChat(chat).type

	if chat_type != 'private':
		try:
			all_admins = msg.chat.all_members_are_administrators
		except:
			all_admins = False

		if not all_admins:
			try:
				sender = context.bot.get_chat_member(chat, from_id)
			except telegram.error.Unauthorized:
				if 'bot was kicked' in error.message:
					logging.info('🗃 Bot was kicked: cleaning chats database...')
					clean_chats_db(DATA_DIR, chat.id)
				else:
					logging.exception('Unknown Unauthorized error')
					return

			if sender.status == 'left':
				try:
					query.answer(text="⚠️ Send the /command again! (chat migrated) ⚠️")
				except Exception as error:
					logging.exception(f'⚠️ Ran into error when answering callbackquery (left): {error}')
					return

				logging.info(f'✋ Callback query called by a left member in {anonymize_id(chat)}')
				return

			if sender.status not in ('creator', 'administrator'):
				try:
					query.answer(text="⚠️ This button is only callable by admins! ⚠️")
				except Exception as error:
					logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

				logging.info(f'✋ Callback query called by a non-admin in {anonymize_id(chat)}, returning | {(1000*(timer() - start)):.0f} ms')
				return

	# callbacks only supported for notify at the moment; verify it's a notify command
	if input_data[0] not in ('notify', 'mute', 'next_flight', 'schedule', 'prefs', 'stats'):
		logging.info(f'''
			⚠️ Incorrect input data in callback_handler! input_data={input_data} | 
			{(1000*(timer() - start)):.0f} ms''')
		return

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
				query.answer(text=f'{map_country_code_to_flag(country_code)}')
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

		# user requests to return to the main menu; send the main keyboard
		elif input_data[1] == 'main_menu':
			try:
				if input_data[2] == 'refresh_text':
					update_main_view(chat, msg, True)
			except:
				update_main_view(chat, msg, False)

			try:
				query.answer(text='⏮ Returned to main menu')
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

			if chat != OWNER:
				logging.info(f'⏮ {anonymize_id(chat)} (main-view update) | {(1000*(timer() - start)):.0f} ms')

		# user requested to toggle a notification
		elif input_data[1] == 'toggle':
			def get_new_notify_group_toggle_state(toggle_type, country_code, chat):
				'''
				Function returns the status to toggle the notification state to
				for multiple entries: either all, or by a country code.
				'''
				providers = set()
				if toggle_type == 'all':
					for val in provider_by_cc.values():
						for provider in val:
							providers.add(provider)

				elif toggle_type == 'country_code':
					for provider in provider_by_cc[country_code]:
						providers.add(provider)

				notification_statuses = get_user_notifications_status(DATA_DIR, chat, providers, provider_name_map)
				disabled_count = 0
				for key, val in notification_statuses.items():
					if toggle_type == 'country_code' and key != 'All':
						if val == 0:
							disabled_count += 1
							break

					elif toggle_type in ('all', 'lsp'):
						if val == 0:
							disabled_count += 1
							break

				return 1 if disabled_count != 0 else 0

			if input_data[2] not in ('country_code', 'lsp', 'all'):
				return

			if input_data[2] == 'all':
				all_toggle_new_status = get_new_notify_group_toggle_state('all', None, chat)

			else:
				country_code = input_data[3]
				if input_data[2] == 'country_code':
					all_toggle_new_status = get_new_notify_group_toggle_state('country_code', country_code, chat)
				else:
					all_toggle_new_status = 0

			''' Toggle the notification state. Input: chat, type, lsp_name '''
			new_status = toggle_notification(
				DATA_DIR, chat, input_data[2], input_data[3], all_toggle_new_status,
				provider_by_cc, provider_name_map)

			if input_data[2] == 'lsp':
				reply_text = {
					1:f'🔔 Enabled notifications for {input_data[3]}',
					0:f'🔕 Disabled notifications for {input_data[3]}'}[new_status]
			elif input_data[2] == 'country_code':
				reply_text = {
					1:f'🔔 Enabled notifications for {map_country_code_to_flag(input_data[3])}',
					0:f'🔕 Disabled notifications for {map_country_code_to_flag(input_data[3])}'}[new_status]
			elif input_data[2] == 'all':
				reply_text = {
					1:'🔔 Enabled all notifications 🌍',
					0:'🔕 Disabled all notifications 🌍'}[new_status]

			# give feedback to the button press
			try:
				query.answer(text=reply_text, show_alert=True)
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

			# clear /next cache for user
			if rd.exists(f'next-{chat}-maxindex'):
				logging.debug(f'📕 Expiring /next cache for {chat} after notify toggle...')
				max_index = rd.get(f'next-{chat}-maxindex')

				# expire keys in 0.1 seconds
				rd.expire(f'next-{chat}-maxindex', datetime.timedelta(seconds=0.1))

				for i in range(0, int(max_index)):
					rd.expire(f'next-{chat}-{i}', datetime.timedelta(seconds=0.1))
					rd.expire(f'next-{chat}-{i}-net', datetime.timedelta(seconds=0.1))

			# update main view if "enable all" button was pressed, as in this case we're in the main view
			else:
				update_main_view(chat, msg, False)

		# user is done; remove the keyboard
		elif input_data[1] == 'done':
			# new callback text
			reply_text = '✅ All done!'

			# new message text
			msg_text = f'''
			🚀 *LaunchBot* | Notification settings

			✅ All done! If you need to adjust your settings in the future, use /notify@{BOT_USERNAME} to access these same settings.
			'''

			# add a button to go back
			inline_keyboard = [[InlineKeyboardButton(text="⏮ I wasn't done!", callback_data='notify/main_menu')]]
			keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

			# update message
			try:
				query.edit_message_text(text=inspect.cleandoc(msg_text), reply_markup=keyboard, parse_mode='Markdown')
			except telegram.error.BadRequest:
				pass

			try:
				query.answer(text=reply_text)
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

			if chat != OWNER:
				logging.info(f'💫 {anonymize_id(chat)} finished setting notifications with the "Done" button! | {(1000*(timer() - start)):.0f} ms')

	elif input_data[0] == 'mute':
		# user wants to mute a launch from notification inline keyboard
		# /mute/$launch_id/(0/1) | 1=muted (true), 0=not muted

		# reverse the button status on press
		new_toggle_state = 0 if int(input_data[2]) == 1 else 1
		new_text = {0:'🔊 Unmute this launch', 1:'🔇 Mute this launch'}[new_toggle_state]
		new_data = f'mute/{input_data[1]}/{new_toggle_state}'

		# maximum number of bytes telegram's bot API supports in callback_data is 64 bytes
		if len(new_data.encode('utf-8')) > 64:
			logging.warning(f'Bytelen of new_data is >64! new_data: {new_data}')

		# create new keyboard
		inline_keyboard = [[InlineKeyboardButton(text=new_text, callback_data=new_data)]]
		keyboard = InlineKeyboardMarkup(
			inline_keyboard=inline_keyboard)

		# tuple containing necessary information to edit the message
		callback_text = '🔇 Launch muted!' if input_data[2] == '1' else '🔊 Launch unmuted!'

		try:
			query.edit_message_reply_markup(reply_markup=keyboard)

			try:
				query.answer(text=callback_text)
			except Exception as error:
				logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')

			if chat != OWNER:
				if new_toggle_state == 0:
					logging.info(f'🔇 {anonymize_id(chat)} muted a launch for launch_id={input_data[1]} | {(1000*(timer() - start)):.0f} ms')
				else:
					logging.info(f'🔊 {anonymize_id(chat)} unmuted a launch for launch_id={input_data[1]} | {(1000*(timer() - start)):.0f} ms')

		except Exception as exception:
			logging.exception(
				f'⚠️ User attempted to mute/unmute a launch, but no reply could be provided (sending message...): {exception}')

			try:
				query.send_message(chat, callback_text, parse_mode='Markdown')
			except Exception as exception:
				logging.exception(f'🛑 Ran into an error sending the mute/unmute message to chat={chat}! {exception}')

		# toggle the mute here, so we can give more responsive feedback
		toggle_launch_mute(db_path=DATA_DIR, chat=chat, launch_id=input_data[1], toggle=int(input_data[2]))

	elif input_data[0] == 'next_flight':
		# next_flight(msg, current_index, command_invoke, cmd):
		# next_flight/{next/prev}/{current_index}/{cmd}
		# next_flight/refresh/0/{cmd}'
		if input_data[1] not in ('next', 'prev', 'refresh'):
			logging.info(f'⚠️ Error with callback_handler input_data[1] for next_flight. input_data={input_data}')
			return

		current_index = input_data[2]
		if input_data[1] == 'next':
			new_message_text, keyboard = generate_next_flight_message(chat, int(current_index)+1)

		elif input_data[1] == 'prev':
			new_message_text, keyboard = generate_next_flight_message(chat, int(current_index)-1)

		elif input_data[1] == 'refresh':
			try:
				new_message_text, keyboard = generate_next_flight_message(chat, int(current_index))

			except TypeError:
				new_message_text = '🔄 No launches found! Try enabling notifications for other providers, or searching for all flights.'
				inline_keyboard = []
				inline_keyboard.append([InlineKeyboardButton(text='🔔 Adjust your notification settings', callback_data=f'notify/main_menu/refresh_text')])
				inline_keyboard.append([InlineKeyboardButton(text='🔎 Search for all flights', callback_data=f'next_flight/refresh/0')])
				keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

				logging.info(
					'🔎 No launches found after next refresh. Sent user the "No launches found" message.')

			except Exception as e:
				new_message_text, keyboard = generate_next_flight_message(chat, int(current_index))
				logging.exception(f'⚠️ No launches found after refresh! {e}')

		# query reply text
		query_reply_text = {'next': 'Next flight ⏩', 'prev': '⏪ Previous flight', 'refresh': '🔄 Refreshed data!'}[input_data[1]]

		# now we have the keyboard; update the previous keyboard
		try:
			query.edit_message_text(text=new_message_text, reply_markup=keyboard, parse_mode='MarkdownV2')
		except telegram.error.TelegramError as exception:
			if 'Message is not modified' in exception.message:
				pass
			else:
				logging.exception(f'⚠️ TelegramError updating message text: {exception}, {vars(exception)}')
				logging.warning(f'⚠️new_message_text: {new_message_text}')

		try:
			query.answer(text=query_reply_text)
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
				new_schedule_msg, keyboard = generate_schedule_message(input_data[2], chat)
			except IndexError: # let's not break """legacy""" compatibility
				new_schedule_msg, keyboard = generate_schedule_message('vehicle', chat)
		else:
			new_schedule_msg, keyboard = generate_schedule_message(input_data[1], chat)

		try:
			query.edit_message_text(text=new_schedule_msg, reply_markup=keyboard, parse_mode='MarkdownV2')

			if input_data[1] == 'refresh':
				query_reply_text = '🔄 Schedule updated!'
			else:
				query_reply_text = '🚀 Vehicle schedule loaded!' if input_data[1] == 'vehicle' else '🛰 Mission schedule loaded!'

			query.answer(text=query_reply_text)

		except telegram.error.TelegramError as exception:
			if 'Message is not modified' in exception.message:
				try:
					query_reply_text = '🔄 Schedule refreshed – no changes detected!'
					query.answer(text=query_reply_text)
				except Exception as error:
					logging.exception(f'⚠️ Ran into error when answering callbackquery: {error}')
				pass
			else:
				logging.exception(f'⚠️ TelegramError updating message text: {exception}')

	elif input_data[0] == 'prefs':
		if input_data[1] not in ('timezone', 'notifs', 'cmds', 'done', 'main_menu'):
			return

		if input_data[1] == 'done':
			query.answer(text='✅ All done!')
			message_text = '💫 Your preferences were saved!'
			try:
				query.edit_message_text(text=message_text, reply_markup=None, parse_mode='Markdown')
			except telegram.error.BadRequest:
				pass

		elif input_data[1] == 'main_menu':
			rand_planet = random.choice(('🌍', '🌎', '🌏'))
			query.answer(text='⏮ Main menu')
			message_text = f'''
			⚙️ *LaunchBot* | Chat preferences

			*Editable preferences*
			⏰ Launch notification types (24 hour/12 hour etc.)
			{rand_planet} Time zone settings
			🛰 Command permissions (_coming soon!_)

			Your time zone is used when sending notifications to show your local time, instead of the default UTC+0.
			'''

			keyboard = InlineKeyboardMarkup(
				inline_keyboard = [
					[InlineKeyboardButton(text=f'{rand_planet} Time zone settings', callback_data='prefs/timezone/menu')],
					[InlineKeyboardButton(text='⏰ Notification settings', callback_data='prefs/notifs')],
					[InlineKeyboardButton(text='⏮ Back to main menu', callback_data='notify/main_menu/refresh_text')]])

			'''
			# TODO update to this keyboard once command permissions is implemented
			keyboard = InlineKeyboardMarkup(
				inline_keyboard = [
					[InlineKeyboardButton(text=f'{rand_planet} Timezone settings', callback_data=f'prefs/timezone')],
					[InlineKeyboardButton(text='⏰ Notification settings', callback_data=f'prefs/notifs')],
					[InlineKeyboardButton(text='🛰 Command settings', callback_data=f'prefs/cmds')],
					[InlineKeyboardButton(text='✅ Exit', callback_data=f'prefs/done')]])
			'''

			try:
				query.edit_message_text(text=inspect.cleandoc(message_text),
					reply_markup=keyboard, parse_mode='Markdown')
			except telegram.error.BadRequest:
				pass

		elif input_data[1] == 'notifs':
			if len(input_data) == 3:
				if input_data[2] in ('24h', '12h', '1h', '5m'):
					new_state = update_notif_preference(
						db_path=DATA_DIR, chat=chat, notification_type=input_data[2])

					# generate reply text
					query_reply_text = f'{input_data[2]} notifications '

					if 'h' in query_reply_text:
						query_reply_text = query_reply_text.replace('h', ' hour')
					else:
						query_reply_text.replace('m', ' minute')

					query_reply_text += 'enabled 🔔' if new_state == 1 else 'disabled 🔕'
					query.answer(text=query_reply_text, show_alert=True)

			# load notification preferences for chat, and map to emoji
			notif_prefs = get_notif_preference(db_path=DATA_DIR, chat=chat)
			bell_dict = {1: '🔔', 0: '🔕'}

			new_prefs_text = '''
			⏰ *Notification settings*

			By default, notifications are sent 24h, 12h, 1h, and 5 minutes before a launch. 

			You can change this behavior here.

			🔔 = currently enabled
			🔕 = *not* enabled
			'''

			keyboard = InlineKeyboardMarkup(
				inline_keyboard = [
					[InlineKeyboardButton(
						text=f'24 hours before {bell_dict[notif_prefs[0]]}',
						callback_data='prefs/notifs/24h')],
					[InlineKeyboardButton(
						text=f'12 hours before {bell_dict[notif_prefs[1]]}',
						callback_data='prefs/notifs/12h')],
					[InlineKeyboardButton(
						text=f'1 hour before {bell_dict[notif_prefs[2]]}',
						callback_data='prefs/notifs/1h')],
					[InlineKeyboardButton(
						text=f'5 minutes before {bell_dict[notif_prefs[3]]}',
						callback_data='prefs/notifs/5m')],
					[InlineKeyboardButton(
						text='⏮ Return to menu',
						callback_data='prefs/main_menu')]])
			try:
				query.edit_message_text(
					text=inspect.cleandoc(new_prefs_text), reply_markup=keyboard, parse_mode='Markdown')
			except telegram.error.BadRequest:
				pass

		elif input_data[1] == 'timezone':
			if input_data[2] == 'menu':
				text = f'''
				🌎 *LaunchBot* | Time zone preferences

				This tool allows you to set your time zone so notifications can show your local time.

				*Choose which method you'd like to use:*
				🌎 *automatic:* uses your location to define your locale (e.g. Europe/Berlin). DST support.

				🕹 *manual:* no DST support, not recommended.

				Your current time zone is *UTC{load_time_zone_status(DATA_DIR, chat, readable=True)}*'''

				locale_string = load_locale_string(DATA_DIR, chat)
				if locale_string is not None:
					text += f' *({locale_string})*'

				keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='🌎 Automatic setup', callback_data='prefs/timezone/auto_setup')],
						[InlineKeyboardButton(text='🕹 Manual setup', callback_data='prefs/timezone/manual_setup')],
						[InlineKeyboardButton(text='🗑 Remove my time zone', callback_data='prefs/timezone/remove')],
						[InlineKeyboardButton(text='⏮ Back to menu', callback_data='prefs/main_menu')]
					]
				)

				try:
					query.edit_message_text(
						text=inspect.cleandoc(text), reply_markup=keyboard, parse_mode='Markdown')
				except telegram.error.BadRequest:
					pass

				query.answer('🌎 Time zone settings loaded')


			elif input_data[2] == 'manual_setup':
				current_time_zone = load_time_zone_status(DATA_DIR, chat, readable=True)

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

				try:
					query.edit_message_text(
						text=inspect.cleandoc(text), parse_mode='Markdown',
						reply_markup=keyboard, disable_web_page_preview=True
					)
				except telegram.error.BadRequest:
					pass

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
				try:
					sent_message = context.bot.send_message(
						chat, inspect.cleandoc(message_text),
						parse_mode='Markdown',
						reply_markup=ReplyKeyboardRemove(remove_keyboard=True)
					)
				except telegram.error.RetryAfter as error:
					logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
					retry_after(error.retry_after)

					sent_message = context.bot.send_message(
						chat, inspect.cleandoc(message_text),
						parse_mode='Markdown',
						reply_markup=ReplyKeyboardRemove(remove_keyboard=True)
					)


				try:
					context.bot.deleteMessage(sent_message.chat.id, sent_message.message_id)
				except telegram.error.RetryAfter as error:
					logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
					retry_after(error.retry_after)

					context.bot.deleteMessage(sent_message.chat.id, sent_message.message_id)

				keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='⏰ Notification settings', callback_data='prefs/notifs')],
						[InlineKeyboardButton(text='⏮ Back to main menu', callback_data='notify/main_menu/refresh_text')]
					]
				)

				try:
					sent_message = context.bot.send_message(
						chat, inspect.cleandoc(message_text),
						parse_mode='Markdown',
						reply_markup=keyboard)
				except telegram.error.RetryAfter as error:
					logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
					retry_after(error.retry_after)

					sent_message = context.bot.send_message(
						chat, inspect.cleandoc(message_text),
						parse_mode='Markdown',
						reply_markup=keyboard)

				query.answer(text='✅ Operation canceled!')

			elif input_data[2] == 'set':
				update_time_zone_value(DATA_DIR, chat, input_data[3])
				current_time_zone = load_time_zone_status(data_dir=DATA_DIR, chat=chat, readable=True)

				text = f'''🌎 This tool allows you to set your time zone so notifications can show your local time.

				Need help? https://www.timeanddate.com/time/map/

				Use the buttons below to set the UTC offset to match your time zone.

				🕗 Your time zone is set to: *UTC{current_time_zone}*
				'''

				keyboard = InlineKeyboardMarkup(inline_keyboard = [
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

				query.answer(text=f'🌎 Time zone set to UTC{current_time_zone}')
				try:
					query.edit_message_text(
						text=inspect.cleandoc(text), reply_markup=keyboard,
					parse_mode='Markdown', disable_web_page_preview=True)
				except telegram.error.BadRequest:
					pass

			elif input_data[2] == 'auto_setup':
				# send message with ForceReply()
				query.answer('🌎 Automatic time zone setup loaded')
				text = '''🌎 Automatic time zone setup

				⚠️ Your exact location is *NOT* stored or logged anywhere. You can remove your time zone at any time.

				Your coordinates are converted to a locale, e.g. Europe/Berlin, or America/Lima, which is used for the UTC off-set. This allows us to support DST.
				
				🌎 *To set your time zone, do the following:*
				1. make sure you're replying to *this* message
				2. tap the file attachment button to the left of the text field (📎)
				3. choose "location"
				4. send the bot an approximate location, but *make sure* it's within the same time zone as you are in
				'''

				context.bot.delete_message(msg.chat.id, msg.message_id)
				sent_message = context.bot.send_message(
					chat, text=inspect.cleandoc(text),
					reply_markup=ForceReply(selective=True), parse_mode='Markdown')

				time_zone_setup_chats[chat] = [sent_message.message_id, from_id]

			elif input_data[2] == 'remove':
				remove_time_zone_information(DATA_DIR, chat)
				query.answer('✅ Your time zone information was deleted from the server', show_alert=True)

				text = f'''🌎 This tool allows you to set your time zone so notifications can show your local time.

				*Choose which method you'd like to use:*
				🌎 *automatic:* uses your location to define your locale (e.g. Europe/Berlin). DST support.

				🕹 *manual:* no DST support, not recommended.

				Your current time zone is *UTC{load_time_zone_status(data_dir=DATA_DIR, chat=chat, readable=True)}*
				'''

				keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='🌎 Automatic setup', callback_data='prefs/timezone/auto_setup')],
						[InlineKeyboardButton(text='🕹 Manual setup', callback_data='prefs/timezone/manual_setup')],
						[InlineKeyboardButton(text='🗑 Remove my time zone', callback_data='prefs/timezone/remove')],
						[InlineKeyboardButton(text='⏮ Back to menu', callback_data='prefs/main_menu')]
					]
				)

				try:
					query.edit_message_text(
						text=inspect.cleandoc(text), reply_markup=keyboard, parse_mode='Markdown')
				except:
					pass


	elif input_data[0] == 'stats':
		if input_data[1] == 'refresh':
			if chat != OWNER:
				logging.info(f'🔄 {anonymize_id(chat)} refreshed statistics')

			new_text = generate_statistics_message()
			if msg.text == new_text.replace('*',''):
				query.answer(text='🔄 Statistics are up to date!')
				return

			keyboard = InlineKeyboardMarkup(
				inline_keyboard=[[
					InlineKeyboardButton(text='🔄 Refresh statistics', callback_data='stats/refresh')]])

			try:
				query.edit_message_text(
					text=new_text, reply_markup=keyboard, parse_mode='Markdown',
					disable_web_page_preview=True)
			except telegram.error.BadRequest:
				pass

			query.answer(text='🔄 Statistics refreshed!')

	# update stats, except if command was a stats refresh
	if input_data[0] != 'stats':
		update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)


def feedback_handler(update, context):
	'''
	Handles the feedback command flow

	Args:
		update (TYPE): Description
		context (TYPE): Description
	'''
	# pull chat object
	if update.message is None:
		if update.channel_post is not None:
			return

		if update.edited_message is not None:
			return

		logging.warning(f'Error parsing chat in feedback_handler!!\n{update}')
		return

	chat = update.message.chat
	if update.message.reply_to_message is not None:
		if update.message.reply_to_message.message_id in feedback_message_IDs:
			if len(update.message.text) > 1000:
				update.message.text = update.message.text[0:1001] + ' <CUT\_OFF:LENGTH>'

			logging.info(f'✍️ Received feedback: {update.message.text}')

			sender = context.bot.get_chat_member(chat.id, update.message.from_user.id)
			if sender.status in ('creator', 'administrator') or chat.type == 'private':
				context.bot.send_message(
					chat.id,
					'😄 Thank you for your feedback!',
					reply_to_message_id=update.message.message_id)

				try: # remove the original feedback message
					context.bot.deleteMessage(chat.id, update.message.reply_to_message.message_id)
				except Exception:
					logging.exception(f'''Unable to remove sent feedback message with params
						chat={chat.id}, message_id={update.message.reply_to_message.message_id}''')

				#if OWNER != 0:
				#	# parse the message so it's suitable for markdown
				#	update.message.text = reconstruct_message_for_markdown(update.message.text)
				#
				#	# if owner is defined, notify of a new feedback message
				#	feedback_notify_msg = f'''
				#		✍️ *Received feedback* from `{anonymize_id(update.message.from_user.id)}`\n
				#		{update.message.text}'''
				#
				#	context.bot.send_message(
				#		OWNER, inspect.cleandoc(feedback_notify_msg),parse_mode='MarkdownV2')


def location_handler(update, context):
	# if location in message, verify it's a time zone setup reply
	chat = update.message.chat

	# verify it's a reply to a message
	if update.message.reply_to_message is not None:
		if chat.id not in time_zone_setup_chats.keys():
			logging.info('🗺 Location received, but chat not in time_zone_setup_chats')
			return

		if (update.message.from_user.id == time_zone_setup_chats[chat.id][1]
			and update.message.reply_to_message.message_id == time_zone_setup_chats[chat.id][0]):

			# delete old message
			context.bot.deleteMessage(chat.id, time_zone_setup_chats[chat.id][0])

			try:
				# remove the message sent by the user so their location isn't visible for long
				context.bot.deleteMessage(chat.id, update.message.message_id)
			except:
				pass

			latitude = update.message.location.latitude
			longitude = update.message.location.longitude

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

			new_text = f'''🌍 *LaunchBot* | Time zone settings

			✅ Time zone successfully set!
			Your time zone is *UTC{utc_offset_str} ({timezone_str})*

			You can now return to other settings.'''

			# construct keyboard
			keyboard = InlineKeyboardMarkup(
				inline_keyboard = [[
					InlineKeyboardButton(text='⏮ Return to menu', callback_data='prefs/main_menu')]])

			# send message
			context.bot.send_message(
				chat.id, text=inspect.cleandoc(new_text),
				reply_markup=keyboard, parse_mode='Markdown')

			# store user's timezone_str
			update_time_zone_string(DATA_DIR, chat.id, timezone_str)


def timer_handle(update, context, command, chat, user):
	''' Summary
	Restrict command send frequency to avoid spam, by storing
	user IDs and when they have called a command in memory.

	TODO use redis/memcached
	'''
	# remove the '/' command prefix
	command = command.strip('/')
	chat = str(chat)

	if '@' in command:
		command = command.split('@')[0]

	# get current time
	now_called = time.time()

	# load timer for command (command_cooldowns)
	try:
		cooldown = command_cooldowns['command_timers'][command]
	except KeyError:
		command_cooldowns['command_timers'][command] = 1
		cooldown = command_cooldowns['command_timers'][command]

	if cooldown <= -1:
		return False

	# checking if the command has been called previously (chat_command_calls)
	# load time the command was previously called
	if chat not in chat_command_calls:
		chat_command_calls[chat] = {}

	# never called, set to 0
	if command not in chat_command_calls[chat]:
		chat_command_calls[chat][command] = 0

	try:
		last_called = chat_command_calls[chat][command]
	except KeyError:
		if chat not in chat_command_calls:
			chat_command_calls[chat] = {}

		if command not in chat_command_calls[chat]:
			chat_command_calls[chat][command] = 0

		last_called = chat_command_calls[chat][command]

	if last_called == 0:
		# never called: stringify datetime object, store
		chat_command_calls[chat][command] = now_called

	else:
		# unstring datetime object
		time_since = now_called - last_called

		if time_since > cooldown:
			# stringify datetime object, store
			chat_command_calls[chat][command] = now_called
		else:
			# pull spammers from redis db
			if rd.exists('spammers'):
				if rd.hexists('spammers', user):
					offenses = int(rd.hget('spammers', user))
				else:
					offenses = None
			else:
				offenses = None

			if offenses is not None:
				# add offense, log
				offenses += 1
				logging.info(f'⚠️ User {anonymize_id(user)} now has {offenses} spam offenses.')

				# if more than 10 offenses, block user
				if offenses >= 10:
					bot_running = time.time() - STARTUP_TIME
					if bot_running > 60:
						ignored_users.add(user)
						logging.info(f'⚠️⚠️⚠️ User {anonymize_id(user)} is now ignored due to excessive spam!')

						context.bot.send_message(
							chat,
							'⚠️ *Please do not spam the bot.* Your user ID has been blocked and all commands by you will be ignored for an indefinite amount of time.',
							parse_mode='Markdown')
					else:
						run_time_ = int(time.time()) - STARTUP_TIME
						logging.info(f'''
							✅ Successfully avoided blocking a user on bot startup! Run_time was {run_time_} seconds.
							Spam offenses set to 0 for user {anonymize_id(user)} from original {offenses}''')

						# clear offenses
						rd.hset('spammers', user, 0)

					return False

				# update database
				rd.hset('spammers', user, offenses)
			else:
				rd.hset('spammers', user, 1)
				logging.info(f'⚠️ Added user {anonymize_id(user)} to spammers.')

			return False

	return True


def start(update, context):
	'''
	Responds to /start and /help commands.
	'''
	# run pre-handler, skip timer_handle
	if not command_pre_handler(update, context, True):
		return

	try:
		# pull the specific command (help or start)
		command_ = update.message.text.strip().split(' ')[0]
	except:
		command_ = '/start'

	# log command
	if update.message.chat.id != OWNER:
		logging.info(f'⌨️ {command_} called by {update.message.from_user.id} in {update.message.chat.id}')

	# construct message
	reply_msg = f'''🚀 *Hi there!* I'm *LaunchBot*, a launch information and notifications bot!

	*List of commands*
	🔔 /notify adjust notification settings
	🚀 /next shows the next launches
	🗓 /schedule displays a simple flight schedule
	📊 /statistics tells various statistics about the bot
	✍️ /feedback send feedback/suggestion to the developer

	⚠️ *Note for group chats* ⚠️ 
	- Commands are only callable by group *admins* and *moderators* to reduce group spam
	- If LaunchBot is made an admin (permission to delete messages), it will automatically remove commands it doesn't answer to

	❓ *Frequently asked questions* ❓
	_How do I turn off a notification?_
	- Use /notify@{BOT_USERNAME}: find the launch provider you want to turn notifications off for.

	_I want less notifications!_
	- You can choose at what times you receive notifications with /notify@{BOT_USERNAME}. You can edit these at the preferences menu (⚙️).

	_Why does the bot only answer to some people?_
	- You have to be an admin in a group to send commands.

	LaunchBot version *{VERSION}* ✨
	'''

	# pull chat id, send message
	chat_id = update.message.chat.id
	context.bot.send_message(chat_id, inspect.cleandoc(reply_msg), parse_mode='Markdown')

	# /start, send also the inline keyboard
	try:
		if command_ == '/start':
			notify(update, context)
			logging.info(f'🌟 Bot added to a new chat! chat_id={anonymize_id(chat_id)}. Sent user the new inline keyboard. [2]')
	except:
		notify(update, context)
		logging.info(f'🌟 Bot added to a new chat! chat_id={anonymize_id(chat_id)}. Sent user the new inline keyboard. [2]')

	# update stats
	update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)


def name_from_provider_id(lsp_id):
	'''
	Sometimes we may need to convert an lsp id to a name: this
	function does exactly that, by querying the launch db table
	for a matching id/name combination.
	'''
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	cursor_ = conn.cursor()

	# get provider name corresponding to this ID
	cursor_.execute("SELECT lsp_name FROM launches WHERE lsp_id = ?", (lsp_id,))
	query_return = cursor_.fetchall()

	if len(query_return) != 0:
		return query_return[0][0]

	return lsp_id


def notify(update, context):
	'''
	Handles responding to the /notify command, which also generated the
	base message than can be manipulated with callback queries.
	'''
	# run pre-handler
	if not command_pre_handler(update, context, False):
		return

	if update.message.chat.id != OWNER:
		logging.info(f'⌨️ /notify called by {update.message.from_user.id} in {update.message.chat.id}')

	message_text = '''
	🚀 *LaunchBot* | Notification settings

	You can search for launch providers, like SpaceX (🇺🇸) or ISRO (🇮🇳), using the flags, or simply enable all!

	You can also edit your notification preferences, like your time zone, from the preferences menu (⚙️).

	🔔 = *currently ON* (press to disable)
	🔕 = *currently OFF* (press to enable)
	'''

	# chat id
	chat = update.message.chat.id

	# create a "full" set of launch service providers by merging the by-cc sets
	lsp_set = set()
	for cc_lsp_set in provider_by_cc.values():
		lsp_set = lsp_set.union(cc_lsp_set)

	# get a dict composed of lsp:enabled_bool entries.
	notification_statuses = get_user_notifications_status(
		db_dir=DATA_DIR, chat=chat, provider_set=lsp_set,
		provider_name_map=provider_name_map)

	# count for the toggle all button
	disabled_count = 1 if 0 in notification_statuses.values() else 0

	# icon, text for the "toggle all" button
	rand_planet = random.choice(('🌍', '🌎', '🌏'))
	toggle_text = 'enable' if disabled_count != 0 else 'disable'
	global_text = f'{rand_planet} Press to {toggle_text} all'

	keyboard = InlineKeyboardMarkup(
			inline_keyboard = [
				[InlineKeyboardButton(text=global_text, callback_data='notify/toggle/all/all')],

				[InlineKeyboardButton(text='🇪🇺 EU', callback_data='notify/list/EU'),
				InlineKeyboardButton(text='🇺🇸 USA', callback_data='notify/list/USA')],

				[InlineKeyboardButton(text='🇷🇺 Russia', callback_data='notify/list/RUS'),
				InlineKeyboardButton(text='🇨🇳 China', callback_data='notify/list/CHN')],

				[InlineKeyboardButton(text='🇮🇳 India', callback_data='notify/list/IND'),
				InlineKeyboardButton(text='🇯🇵 Japan', callback_data='notify/list/JPN')],

				[InlineKeyboardButton(text='⚙️ Edit your preferences', callback_data='prefs/main_menu')],

				[InlineKeyboardButton(text='✅ Save and exit', callback_data='notify/done')]])

	try:
		context.bot.send_message(
			chat, inspect.cleandoc(message_text), parse_mode='Markdown', reply_markup=keyboard)
	except telegram.error.RetryAfter as error:
		logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
		retry_after(error.retry_after)
		context.bot.send_message(
			chat, inspect.cleandoc(message_text), parse_mode='Markdown', reply_markup=keyboard)

	# update stats
	update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)


def feedback(update, context):
	'''
	Receive feedback from users.
	'''
	# run pre-handler
	if not command_pre_handler(update, context, False):
		return

	if update.message.chat.id != OWNER:
		logging.info(f'⌨️ /feedback called by {update.message.from_user.id} in {update.message.chat.id}')

	chat_id = update.message.chat.id

	# feedback called by $chat; send the user a message with ForceReply in it, so we can get a response
	message_text = '''
	✍️ This is a way of sharing feedback and reporting issues to the developer of the bot.

	*All feedback is anonymous.*

	Please note that it is impossible for me to reply to your feedback, but you can be sure I'll read it!

	Just write your feedback *as a reply to this message* (otherwise I won't see it due to the bot's privacy settings)

	You can also provide feedback at the bot's GitHub repository.
	'''

	try:
		sent_msg = context.bot.send_message(
			chat_id, inspect.cleandoc(message_text), parse_mode='Markdown',
			reply_markup=ForceReply(selective=True), reply_to_message_id=update.message.message_id)
	except telegram.error.Unauthorized as error:
		logging.info(f'Unauthorized to send message! Error.message: {error.message}')
		clean_chats_db(db_path=DATA_DIR, chat=chat_id)
	except telegram.error.RetryAfter as error:
		logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
		retry_after(error.retry_after)

		sent_msg = context.bot.send_message(
			chat_id, inspect.cleandoc(message_text), parse_mode='Markdown',
			reply_markup=ForceReply(selective=True), reply_to_message_id=update.message.message_id)
	except telegram.error.TimedOut as error:
		logging.info(f'Error: timed out! Error.message: {error.message}')
		time.sleep(1)
		feedback(update, context)

	''' add sent message id to feedback_message_IDs, so we can keep
	track of what to parse as feedback, and what not to '''
	feedback_message_IDs.add(sent_msg.message_id)

	# update stats
	update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)


def generate_schedule_message(call_type: str, chat: str):
	'''
	Generates the schedule message and keyboard.

	TODO: add "only show launches with verified dates" button
	TODO: add "only show subscribed launches" button
	'''
	# open db connection
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor_ = conn.cursor()

	# perform the select; if cmd == all, just pull the next launch
	cursor_.execute('SELECT * FROM launches WHERE net_unix >= ?',(int(time.time()),))

	# sort in place by NET, convert to dicts
	query_return = [dict(row) for row in cursor_.fetchall()]
	query_return.sort(key=lambda tup: tup['net_unix'])

	# close db
	conn.close()

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

	vehicle_map = {
		'Falcon 9 Block 5': 'Falcon 9 B5'}

	# pull user time zone preferences, set tz_offset from hours to seconds
	# TODO cache in redis under chat ID, update on tz update
	user_tz_offset = 3600 * load_time_zone_status(DATA_DIR, chat, readable=False)

	# pick 5 dates, map missions into dict with dates
	sched_dict = {}
	for i, row in enumerate(query_return):
		launch_unix = datetime.datetime.utcfromtimestamp(row['net_unix'] + user_tz_offset)

		if len(row['lsp_name']) <= len('Arianespace'):
			provider = row['lsp_name']
		else:
			if row['lsp_short'] not in (None, ''):
				provider = row['lsp_short']
			else:
				provider = row['lsp_name']

		try:
			mission = row['name'].split('|')[1].strip()
		except IndexError:
			mission = row['name'].strip()

		if mission[0] == ' ':
			mission = mission[1:]

		if '(' in mission:
			mission = mission[0:mission.index('(')]

		if provider in providers_short.keys():
			provider = providers_short[provider]

		vehicle = row['rocket_name'].split('/')[0]

		country_code= row['lsp_country_code']
		flag = map_country_code_to_flag(country_code)

		# shorten some vehicle names
		if vehicle in vehicle_map.keys():
			vehicle = vehicle_map[vehicle]

		# shorten monospaced text length
		provider = short_monospaced_text(provider)
		vehicle = short_monospaced_text(vehicle)
		mission = short_monospaced_text(mission)

		# start the string with the flag of the provider's country
		flt_str = flag if flag is not None else ''

		# add a button indicating the status of the launch
		# GO: green, TBC: yellow, TBD: red
		go_status = row['status_state']
		if go_status == 'GO':
			flt_str += '🟢'
		elif go_status == 'TBC':
			flt_str += '🟡'
		elif go_status == 'TBD':
			flt_str += '🔴'

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
	for key, val in sched_dict.items():
		if i != 0:
			schedule_msg += '\n\n'

		# create the date string; key in the form of year-month-day
		ymd_split = key.split('-')
		try:
			if int(ymd_split[2]) in (11, 12, 13):
				suffix = 'th'
			else:
				suffix = {1: 'st', 2: 'nd', 3: 'rd'}[int(str(ymd_split[2])[-1])]
		except:
			suffix = 'th'

		# calc how many days until this date
		launch_date = datetime.datetime.strptime(key, '%Y-%m-%d')

		# get today based on chat preferences: if not available, use UTC+0
		today = datetime.datetime.utcfromtimestamp(time.time() + user_tz_offset)
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

	# parse message body for markdown
	schedule_msg = reconstruct_message_for_markdown(schedule_msg)

	# get user's time zone string
	utc_offset = load_time_zone_status(DATA_DIR, chat, readable=True)

	# add header and footer
	header = '📅 *5\-day flight schedule*\n'
	header_note = f'For detailed flight information, use /next@{BOT_USERNAME}. Dates relative to UTC{utc_offset}.'
	footer_note = '\n\n🟢 = verified launch time\n🟡 = unconfirmed launch time\n🔴 = unknown launch time'

	# parse for markdown
	footer = f'_{reconstruct_message_for_markdown(footer_note)}_'
	header_info = f'_{reconstruct_message_for_markdown(header_note)}\n\n_'

	# final message
	schedule_msg = header + header_info + schedule_msg + footer

	# call change button
	switch_text = '🚀 Vehicles' if call_type == 'mission' else '🛰 Missions'

	inline_keyboard = []
	inline_keyboard.append([
		InlineKeyboardButton(text='🔄 Refresh', callback_data=f'schedule/refresh/{call_type}'),
		InlineKeyboardButton(text=switch_text, callback_data=f"schedule/{'mission' if call_type == 'vehicle' else 'vehicle'}")])

	keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

	return schedule_msg, keyboard


def flight_schedule(update, context):
	'''
	Display a very simple schedule for all upcoming flights.
	'''
	# run pre-handler
	if not command_pre_handler(update, context, False):
		return

	if update.message.chat.id != OWNER:
		logging.info(f'⌨️ /schedule called by {update.message.from_user.id} in {update.message.chat.id}')

	chat_id = update.message.chat.id

	# generate message
	schedule_msg, keyboard = generate_schedule_message(call_type='vehicle', chat=chat_id)

	# send
	try:
		context.bot.send_message(chat_id, schedule_msg, reply_markup=keyboard, parse_mode='MarkdownV2')

	except telegram.error.Unauthorized as error:
		logging.info(f'Unauthorized to send message! Error.message: {error.message}')
		clean_chats_db(db_path=DATA_DIR, chat=chat_id)

	except telegram.error.RetryAfter as error:
		logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
		retry_after(error.retry_after)

		context.bot.send_message(chat_id, schedule_msg, reply_markup=keyboard, parse_mode='MarkdownV2')

	except telegram.error.TimedOut as error:
		logging.info(f'Error: timed out! Error.message: {error.message}')
		time.sleep(1)
		flight_schedule(update, context)

	# update stats
	update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)


def generate_next_flight_message(chat, current_index: int):
	'''
	Generates the message text for use in the next-command.

	Caching to redis with setex:
		"next-$chat-$index" -> str

	TODO cache the entire string, replace ETA str with "<*ETA_STR*>"
	-> recalculate ETA on cache pull

	Purge cache on API updates
	'''
	def cached_response():
		# generate the keyboard here
		try:
			max_index = int(rd.get(f'next-{chat}-maxindex'))
		except TypeError:
			generate_next_flight_message(chat, current_index)
			return

		if max_index > 1:
			inline_keyboard = [[]]
			back, fwd = False, False

			if current_index != 0:
				back = True
				inline_keyboard[0].append(
						InlineKeyboardButton(
							text='⏪ Previous', callback_data=f'next_flight/prev/{current_index}'))

			inline_keyboard[0].append(
				InlineKeyboardButton(
					text='🔄 Refresh', callback_data=f'next_flight/refresh/{current_index}'))

			# if we can go forward, add a next button
			if current_index+1 < max_index:
				fwd = True
				inline_keyboard[0].append(
					InlineKeyboardButton(text='Next ⏩', callback_data=f'next_flight/next/{current_index}'))

			# if the length is one, make the button really wide
			if len(inline_keyboard[0]) == 1:
				# only forwards, so the first entry; add a refresh button
				if fwd:
					inline_keyboard = [[]]
					inline_keyboard[0].append(InlineKeyboardButton(
						text='🔄 Refresh', callback_data=f'next_flight/refresh/0'))
					inline_keyboard[0].append(InlineKeyboardButton(
						text='Next ⏩', callback_data=f'next_flight/next/{current_index}'))
				elif back:
					inline_keyboard = [([InlineKeyboardButton(
						text='⏪ Previous', callback_data=f'next_flight/prev/{current_index}')])]
					inline_keyboard.append([(InlineKeyboardButton(
						text='⏮ First', callback_data=f'next_flight/prev/1'))])

			keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

		elif max_index == 1:
			inline_keyboard = []
			inline_keyboard.append([InlineKeyboardButton(
				text='🔄 Refresh', callback_data=f'next_flight/prev/1')])

			keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

		launch_net = int(rd.get(f'next-{chat}-{current_index}-net'))
		eta = abs(int(time.time()) - launch_net)

		next_str = rd.get(f'next-{chat}-{current_index}')

		launch_status = rd.get(f'next-{chat}-{current_index}-status')
		if launch_status is not False:
			if launch_status in ('GO', 'TBC', 'TBD'):
				if launch_net < int(time.time()):
					eta_str = '⏸ Waiting for status update'
					next_str = next_str.replace('⏰ ???ETASTR???', short_monospaced_text(eta_str))
				else:
					eta_str = time_delta_to_legible_eta(time_delta=eta, full_accuracy=True)
					next_str = next_str.replace('???ETASTR???', short_monospaced_text(eta_str))
			else:
				if launch_status == 'HOLD':
					t_prefx, eta_str = '⏸', 'Launch in hold: stand by'

				elif launch_status == 'FLYING':
					t_prefx, eta_str = '🚀', 'Vehicle in flight: stand by'

				else:
					logging.warning(f'Unknown status_state: {launch["status_state"]}')
					t_prefx, eta_str = '⚠️', 'Internal server error'

				next_str = next_str.replace(
					'⏰ ???ETASTR???',
					f'{t_prefx} {short_monospaced_text(eta_str)}')
		else:
			eta_str = time_delta_to_legible_eta(time_delta=eta, full_accuracy=True)
			next_str = next_str.replace('???ETASTR???', short_monospaced_text(eta_str))

		# return msg + keyboard
		return inspect.cleandoc(next_str), keyboard

	# check if a cached response exists
	if rd.exists(f'next-{chat}-{current_index}'):
		if chat != OWNER:
			logging.debug(f'🐇 cache-hit for next/{chat}/{current_index}')
		return cached_response()

	# start db connection
	conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor_ = conn.cursor()

	# verify db exists
	cursor_.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'chats'))
	if len(cursor_.fetchall()) == 0:
		create_chats_db(db_path=DATA_DIR, cursor=cursor_)
		conn.commit()

	# find what launches the chat is subscribed to
	cursor_.execute('''SELECT * FROM chats WHERE chat = ?''', (chat,))

	# convert rows into dictionaries for super easy parsing
	query_return = [dict(row) for row in cursor_.fetchall()]

	# flag for all notifications enabled
	all_flag = False

	# chat has no enabled notifications; pull from all
	if len(query_return) == 0:
		cmd, user_notif_enabled = 'all', False
		enabled, disabled = [], []
	else:
		user_notif_enabled = None
		cmd = None

		# db row for chat
		chat_row = query_return[0]

		# parse the strings into lists
		enabled, disabled = [], []

		try:
			enabled = chat_row['enabled_notifications'].split(',')
		except AttributeError:
			enabled = []

		try:
			disabled = chat_row['disabled_notifications'].split(',')
		except AttributeError:
			disabled = []

		# remove possible empty entires
		if '' in enabled:
			enabled.remove('')

		# remove possible empty entires
		if '' in disabled:
			disabled.remove('')

		# if All found, toggle flag
		if 'All' in enabled:
			all_flag, user_notif_enabled = True, True
			if len(disabled) == 0:
				cmd = 'all'
		else:
			all_flag = False
			user_notif_enabled = True

		if len(enabled) == 0:
			user_notif_enabled = False

	# if chat has no notifications enabled, use cmd=all
	if len(enabled) == 0:
		cmd = 'all'

	# datetimes
	today_unix = int(time.time())

	# perform the select; if cmd == all, just pull the next launch
	if cmd == 'all':
		cursor_.execute('''
			SELECT * FROM launches WHERE net_unix >= ? OR launched = 0 
			AND status_state = ? OR status_state = ?''',
			(today_unix, 'HOLD', 'FLYING'))
		query_return = cursor_.fetchall()

	elif cmd is None:
		if all_flag:
			if len(disabled) > 0:
				disabled_str = ''
				for enum, lsp in enumerate(disabled):
					disabled_str += f"'{lsp}'"
					if enum < len(disabled) - 1:
						disabled_str += ','

				query_str = f'''SELECT * FROM launches WHERE net_unix >= ? AND lsp_name NOT IN ({disabled_str})
				AND lsp_short NOT IN ({disabled_str})'''

				cursor_.execute(query_str, (today_unix,))
				query_return = cursor_.fetchall()

			else:
				cursor_.execute('SELECT * FROM launches WHERE net_unix >= ?',(today_unix,))
				query_return = cursor_.fetchall()
		else:
			# if no all_flag set, simply select all that are enabled
			enabled_str = ''
			for enum, lsp in enumerate(enabled):
				enabled_str += f"'{lsp}'"
				if enum < len(enabled) - 1:
					enabled_str += ','

			query_str = f'''SELECT * FROM launches WHERE net_unix >= ? AND lsp_name IN ({enabled_str})
			OR net_unix >= ? AND lsp_short IN ({enabled_str})'''

			cursor_.execute(query_str, (today_unix,today_unix))
			query_return = cursor_.fetchall()

	# close connection
	conn.close()

	# sort ascending by NET, pick smallest
	max_index = len(query_return) - 1
	if max_index > 0:
		query_return.sort(key=lambda tup: tup[3])
		try:
			launch = dict(query_return[current_index])
		except Exception as error:
			logging.exception(f'⚠️ Exception setting launch via current_index: {error}')
			launch = dict(query_return[0])
	else:
		msg_text = '🔄 No launches found! Try enabling notifications for other providers, or searching for all flights.'
		inline_keyboard = []
		inline_keyboard.append([InlineKeyboardButton(text='🔔 Adjust your notification settings', callback_data='notify/main_menu/refresh_text')])
		inline_keyboard.append([InlineKeyboardButton(text='🔎 Search for all flights', callback_data='next_flight/refresh/0/all')])
		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

		logging.info('🔎 No launches found in next. Sent user the "No launches found" message.')
		return reconstruct_message_for_markdown(msg_text), keyboard

	# launch name
	try:
		launch_name = launch['name'].split('|')[1].strip()
	except IndexError:
		launch_name = launch['name'].strip()

	# shorten long launch service provider name
	if len(launch['lsp_name']) > len('Galactic Energy'):
		if launch['lsp_id'] in LSP_IDs.keys():
			lsp_name = LSP_IDs[launch['lsp_id']][0]
		else:
			if launch['lsp_short'] not in (None, ''):
				lsp_name = launch['lsp_short']
			else:
				lsp_name = launch['lsp_name']
	else:
		lsp_name = launch['lsp_name']

	if launch['lsp_id'] in LSP_IDs.keys():
		lsp_flag = LSP_IDs[launch['lsp_id']][1]
	else:
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
	location = f'{launch["pad_name"]}, {launch_site} {location_flag}'

	# generate ETA string
	eta = abs(int(time.time()) - launch['net_unix'])

	# if not holding/flying, use regular ETA
	if launch['status_state'] in ('GO', 'TBD', 'TBC'):
		t_prefx, eta_str = '⏰', time_delta_to_legible_eta(time_delta=eta, full_accuracy=True)
	elif launch['status_state'] == 'HOLD':
		t_prefx, eta_str = '⏸', 'Launch in hold: stand by'
	elif launch['status_state'] == 'FLYING':
		t_prefx, eta_str = '🚀', 'Vehicle in flight: stand by'
	else:
		logging.warning(f'Unknown status_state: {launch["status_state"]}')
		t_prefx, eta_str = '⚠️', 'Internal server error'

	# # pull user time zone preferences, set tz_offset from hours to seconds
	user_tz_offset = 3600 * load_time_zone_status(DATA_DIR, chat, readable=False)

	# generate launch time string
	launch_datetime = datetime.datetime.utcfromtimestamp(launch['net_unix'] + user_tz_offset)
	if launch_datetime.minute < 10:
		min_time = f'0{launch_datetime.minute}'
	else:
		min_time = launch_datetime.minute

	launch_time = f'{launch_datetime.hour}:{min_time}'

	# generate date string
	date_str = timestamp_to_legible_date_string(launch['net_unix'] + user_tz_offset)

	# verified launch date
	if launch['status_state'] in ('GO', 'TBC', 'FLYING'):
		# load UTC offset in readable format
		readable_utc_offset = load_time_zone_status(data_dir=DATA_DIR, chat=chat, readable=True)

		if launch['status_state'] in ('GO', 'FLYING'):
			# verified launch date and launch time
			time_str = f'{date_str}, {launch_time} UTC{readable_utc_offset}'
		else:
			# verified launch date, but unverified launch time
			time_str = f'{date_str}, NET {launch_time} UTC{readable_utc_offset}'
	else:
		# unverified launch date (status_state == TBD)
		if launch['status_state'] == 'TBD':
			time_str = f'Not before {date_str}'
		else:
			if launch['status_state'] == 'HOLD':
				# holding: time unknown
				time_str = 'Waiting for new launch date'
			else:
				logging.warning(f'Unknown status state for time_str ({launch["status_state"]})')

	# add mission information: type, orbit
	mission_type = launch['mission_type'].capitalize() if launch['mission_type'] is not None else 'Unknown purpose'

	# TODO add orbits for TMI and TLI, once these pop up for the first time
	orbit_map = {
		'Sub': 'Sub-orbital', 'VLEO': 'Very-low Earth orbit', 'LEO': 'Low Earth orbit',
		'SSO': 'Sun-synchronous orbit', 'MEO': 'Medium Earth orbit', 'GTO': 'Geostationary (transfer)',
		'Direct-GEO': 'Geostationary (direct)', 'GSO': 'Geosynchronous orbit', 'LO': 'Lunar orbit'
	}

	try:
		orbit_info = '🌒' if 'LO' in launch['mission_orbit_abbrev'] else '🌍'
		if launch['mission_orbit_abbrev'] in orbit_map.keys():
			orbit_str = orbit_map[launch['mission_orbit_abbrev']]
		else:
			orbit_str = launch['mission_orbit'] if launch['mission_orbit_abbrev'] is not None else 'Unknown'
			if 'Starlink' in launch_name:
				orbit_str = 'Very-low Earth orbit'
	except:
		orbit_info = '🌍'
		orbit_str = 'Unknown orbit'

	# add crew str here, if the launch has astros on board
	if launch['spacecraft_crew_count'] not in (None, 0):
		if 'Dragon' in launch['spacecraft_name']:
			spacecraft_info = f'''
			*Dragon information* 🐉
			*Crew* {short_monospaced_text("👨‍🚀" * launch["spacecraft_crew_count"])}
			*Capsule* {short_monospaced_text(launch["spacecraft_sn"])}
			'''
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

			# append .x to F9 core names
			if lsp_name == 'SpaceX' and core_str[0:2] == 'B1':
				core_str += f'.{int(reuse_count)}'

			reuse_str = f'{core_str} ({suffixed_readable_int(reuse_count)} flight ♻️)'
		else:
			# append .x to F9 core names
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

		if lsp_name == 'SpaceX' and 'Starship' in launch["rocket_name"]:
			location = f'SpaceX South Texas Launch Site, Boca Chica {location_flag}'
			recovery_str = f'''
			*Vehicle information* 🚀
			*Starship* {short_monospaced_text(reuse_str)}
			*Landing* {short_monospaced_text(landing_str)}
			'''
		else:
			recovery_str = f'''
			*Vehicle information* 🚀
			*Core* {short_monospaced_text(reuse_str)}
			*Landing* {short_monospaced_text(landing_str)}
			'''
	else:
		recovery_str = None

	# pull launch info
	info_str = launch['mission_description']
	if info_str is None:
		info_str = 'No launch information available.'
	else:
		info_str = '.'.join(info_str.split('\n')[0].split('.')[0:3])

	# inform the user whether they'll be notified or not
	if user_notif_enabled:
		user_notif_states = chat_row['notify_time_pref']
		if '1' in user_notif_states:
			notify_str = '🔔 You will be notified of this launch!'
		else:
			notify_str = '🔕 You will *not* be notified of this launch!'
			notify_str += f'\nℹ️ *To enable:* /notify@{BOT_USERNAME} (change notification settings with ⚙️)'
	else:
		notify_str = '🔕 You will *not* be notified of this launch.'
		notify_str += f'\nℹ️ *To enable:* /notify@{BOT_USERNAME}'

	next_str = f'''
	🚀 *Next launch* | {short_monospaced_text(lsp_name)} {lsp_flag}
	*Mission* {short_monospaced_text(launch_name)}
	*Vehicle* {short_monospaced_text(launch["rocket_name"])}
	*Pad* {short_monospaced_text(location)}

	📅 {short_monospaced_text(time_str)}
	⏰ ???ETASTR???

	*Mission information* {orbit_info}
	*Type* {short_monospaced_text(mission_type)}
	*Orbit* {short_monospaced_text(orbit_str)}
	'''

	if spacecraft_info is not None:
		next_str += spacecraft_info

	if recovery_str is not None:
		next_str += recovery_str

	# {spacecraft_info if spacecraft_info is not None else ""}
	# {recovery_str if recovery_str is not None else ""}

	next_str += f'''
	ℹ️ {info_str}

	{notify_str}
	'''

	next_str = next_str.replace('\t', '')

	# generate the keyboard here
	if max_index > 1:
		inline_keyboard = [[]]
		back, fwd = False, False

		if current_index != 0:
			back = True
			inline_keyboard[0].append(
					InlineKeyboardButton(
						text='⏪ Previous', callback_data=f'next_flight/prev/{current_index}'))

		inline_keyboard[0].append(
			InlineKeyboardButton(
				text='🔄 Refresh', callback_data=f'next_flight/refresh/{current_index}'))

		# if we can go forward, add a next button
		if current_index+1 < max_index:
			fwd = True
			inline_keyboard[0].append(
				InlineKeyboardButton(text='Next ⏩', callback_data=f'next_flight/next/{current_index}'))

		# if the length is one, make the button really wide
		if len(inline_keyboard[0]) == 1:
			# only forwards, so the first entry; add a refresh button
			if fwd:
				inline_keyboard = [[]]
				inline_keyboard[0].append(InlineKeyboardButton(
					text='🔄 Refresh', callback_data=f'next_flight/refresh/0'))
				inline_keyboard[0].append(InlineKeyboardButton(
					text='Next ⏩', callback_data=f'next_flight/next/{current_index}'))
			elif back:
				inline_keyboard = [([InlineKeyboardButton(
					text='⏪ Previous', callback_data=f'next_flight/prev/{current_index}')])]
				inline_keyboard.append([(InlineKeyboardButton(
					text='⏮ First', callback_data=f'next_flight/prev/1'))])

		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

	elif max_index == 1:
		inline_keyboard = []
		inline_keyboard.append([InlineKeyboardButton(
			text='🔄 Refresh', callback_data=f'next_flight/prev/1')])

		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

	# parse for markdown
	next_str = reconstruct_message_for_markdown(next_str)

	# expire keys 15 seconds after next api update
	if rd.exists('next-api-update'):
		to_next_update = int(float(rd.get('next-api-update'))) - int(time.time()) + 30
	else:
		to_next_update = 30*60

	if to_next_update < 0:
		to_next_update = 60

	# cache string, NET timestamp, launch status
	rd.setex(f'next-{chat}-maxindex',
		datetime.timedelta(seconds=to_next_update), value=max_index)
	rd.setex(f'next-{chat}-{current_index}',
		datetime.timedelta(seconds=to_next_update), value=next_str)
	rd.setex(f'next-{chat}-{current_index}-net',
		datetime.timedelta(seconds=to_next_update), value=launch['net_unix'])
	rd.setex(f'next-{chat}-{current_index}-status',
		datetime.timedelta(seconds=to_next_update), value=launch['status_state'])

	# TODO replace ETA
	next_str = next_str.replace('⏰ ???ETASTR???', f'{t_prefx} {short_monospaced_text(eta_str)}')

	# return msg + keyboard
	return inspect.cleandoc(next_str), keyboard


def next_flight(update, context):
	'''
	Return the next flight. Message is generated
	with the helper function generate_next_flight_message.
	'''
	# run pre-handler
	if not command_pre_handler(update, context, False):
		return

	if update.message.chat.id != OWNER:
		logging.info(f'⌨️ /next called by {update.message.from_user.id} in {update.message.chat.id}')

	# chat ID
	chat_id = update.message.chat.id

	# generate message and keyboard
	message, keyboard = generate_next_flight_message(chat_id, 0)

	# send message
	try:
		context.bot.send_message(
			chat_id, message, reply_markup=keyboard, parse_mode='MarkdownV2')

	except telegram.error.Unauthorized as error:
		logging.info(f'Unauthorized to send message! Error.message: {error.message}')
		clean_chats_db(db_path=DATA_DIR, chat=chat_id)

	except telegram.error.RetryAfter as error:
		logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
		retry_after(error.retry_after)

		context.bot.send_message(
			chat_id, message, reply_markup=keyboard, parse_mode='MarkdownV2')

	except telegram.error.TimedOut as error:
		logging.info(f'Error: timed out! Error.message: {error.message}')
		time.sleep(1)
		next_flight(update, context)

	# update stats
	update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)


def generate_statistics_message() -> str:
	'''
	Returns the message body for statistics command. Only a helper function,
	which allows us to respond to callback queries as well.
	'''
	# verify stats exist in hot db
	if not rd.exists('stats'):
		# pull from disk
		stats_conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		stats_conn.row_factory = sqlite3.Row
		stats_cursor = stats_conn.cursor()

		try:
			# select stats field
			stats_cursor.execute("SELECT * FROM stats")
			stats = [dict(row) for row in stats_cursor.fetchall()][0]

			# parse returned global data
			data = stats['data']
			last_db_update = stats['last_api_update']
		except sqlite3.OperationalError:
			notifs = api_reqs = commands = data = last_db_update = 0

		# insert into redis | stats: {key:val, key:val, key:val}
		rd.hmset('stats', stats)

	# pull from redis
	stats = rd.hgetall('stats')

	# fields we operate on
	data = int(stats['data'])
	last_db_update = int(stats['last_api_update'])

	# get system load average for linux/darwin/aix systems
	if rd.exists('load_avg'):
		load_avg_str = rd.get('load_avg')
	else:
		if sys.platform != 'win32':
			load_avgs = os.getloadavg() # [1 min, 5 min, 15 min]
			load_avg_str = f'{load_avgs[0]:.2f} {load_avgs[1]:.2f} {load_avgs[2]:.2f}'
		else:
			load_avg_str = 'unknown'

		rd.setex('load_avg', datetime.timedelta(minutes=5), value=load_avg_str)

	# format transfered API data to MB, GB
	data_suffix = 'GB' if data/10**9 >= 1 else 'MB'
	data = data/10**9 if data/10**9 >= 1 else data/10**6

	# get amount of stored data
	if rd.exists('disk_storage'):
		db_storage = float(rd.get('disk_storage'))
	else:
		try:
			db_storage = 0.00
			db_storage += os.path.getsize(os.path.join(DATA_DIR, 'launchbot-data.db'))
			db_storage += os.path.getsize(os.path.join(DATA_DIR, 'bot-config.json'))
		except:
			db_storage = 0.00

		rd.setex('disk_storage', datetime.timedelta(minutes=60), value=db_storage)

	# format stored data to KB, MB, GB
	if db_storage/10**6 >= 1:
		db_storage_suffix = 'GB' if db_storage/10**9 >= 1 else 'MB'
		db_storage = db_storage/10**9 if db_storage/10**9 >= 1 else db_storage/10**6
	else:
		db_storage_suffix = 'KB'
		db_storage = db_storage/10**3

	# convert time since last db update to a readable ETA, add suffix
	db_update_delta = int(time.time()) - last_db_update
	last_db_update = time_delta_to_legible_eta(time_delta=db_update_delta, full_accuracy=False)
	last_db_update_suffix = 'ago' if last_db_update not in ('never', 'just now') else ''

	# check if we have enabled_notifications count cached: if not, pull from disk and cache
	if rd.exists('subscribed_chats'):
		notification_recipients = rd.get('subscribed_chats')
	else:
		# connect to notifications db
		conn = sqlite3.connect(os.path.join(DATA_DIR, 'launchbot-data.db'))
		cursor_ = conn.cursor()

		try:
			# pull all rows with enabled = 1
			cursor_.execute('''SELECT COUNT(chat) FROM chats
				WHERE enabled_notifications NOT NULL AND enabled_notifications != ""''')

			notification_recipients = cursor_.fetchall()[0][0]
			conn.close()
		except sqlite3.OperationalError:
			logging.exception('Error parsing notification_recipients!')
			notification_recipients = 0

		# cache value
		rd.setex(
			'subscribed_chats', datetime.timedelta(minutes=60),
			value=notification_recipients)

	# get repo head's commit hex hash
	if rd.exists('git_head_hash'):
		head_hash = rd.get('git_head_hash')
	else:
		try:
			head_hash = git.Repo('../').heads[0].commit.hexsha[0:7]
		except Exception as error:
			logging.exception(f'[?] unable to set head_hash: {error}')
			head_hash = 'unknown version'

		rd.set('head_hash', head_hash)

	if rd.exists('parsed_github_link'):
		parsed_github_link = rd.get('parsed_github_link')
	else:
		parsed_github_link = reconstruct_link_for_markdown(
			'https://github.com/499602D2/tg-launchbot')

		rd.set('parsed_github_link', parsed_github_link)

	# add thousand separators to all number values

	stats_str = f'''
	📊 *LaunchBot global statistics*
	Notifications delivered: {int(stats['notifications']):,}
	Notification recipients: {notification_recipients}
	Commands parsed: {int(stats['commands']):,}

	🛰 *Network statistics*
	Data transferred: {data:.2f} {data_suffix}
	API requests made: {int(stats['api_requests']):,}

	💾 *Database information*
	Storage used: {db_storage:.2f} {db_storage_suffix}
	Updated: {last_db_update} {last_db_update_suffix}

	🎛 *Server information*
	Uptime {time_delta_to_legible_eta(time_delta=uptime(), full_accuracy=False)}
	Load {load_avg_str}
	LaunchBot *{VERSION}* [({head_hash})]({parsed_github_link}) 🚀
	'''

	return inspect.cleandoc(stats_str)


def statistics(update, context):
	'''
	Return statistics for LaunchBot. Statistics are generated
	with the helper function generate_statistics_message.
	'''
	# run pre-handler
	if not command_pre_handler(update, context, False):
		return

	if update.message.chat.id != OWNER:
		logging.info(f'⌨️ /statistics called by {update.message.from_user.id} in {update.message.chat.id}')

	# chat ID
	chat_id = update.message.chat.id

	# update stats
	update_stats_db(stats_update={'commands':1}, db_path=DATA_DIR)

	# generate message
	stats_str = generate_statistics_message()

	# add a keyboard for refreshing
	keyboard = InlineKeyboardMarkup(
		inline_keyboard=[[
			InlineKeyboardButton(text='🔄 Refresh statistics', callback_data='stats/refresh')]])

	try:
		context.bot.send_message(
			chat_id, stats_str, reply_markup=keyboard, parse_mode='Markdown',
			disable_web_page_preview=True
			)

	except telegram.error.Unauthorized as error:
		logging.info(f'Unauthorized to send message! Error.message: {error.message}')
		clean_chats_db(db_path=DATA_DIR, chat=chat_id)

	except telegram.error.RetryAfter as error:
		logging.exception(f'🚧 Got a telegram.error.retryAfter: sleeping for {error.retry_after} sec.')
		retry_after(error.retry_after)

		context.bot.send_message(
			chat_id, stats_str, reply_markup=keyboard, parse_mode='Markdown',
			disable_web_page_preview=True
			)

	except telegram.error.TimedOut as error:
		logging.info(f'Error: timed out! Error.message: {error.message}')
		time.sleep(1)
		statistics(update, context)


def update_token(data_dir: str, new_tokens: set):
	'''
	Used to update the bot token.
	'''
	# create /data and /chats
	config_ = load_config(data_dir)

	if 'bot_token' in new_tokens:
		token_input = str(input('Enter the bot token for LaunchBot: '))
		while ':' not in token_input:
			print('Please try again – bot-tokens look like "123456789:ABHMeJViB0RHL..."')
			token_input = str(input('Enter the bot token for launchbot: '))

		config_['bot_token'] = token_input

	store_config(config_, data_dir)

	time.sleep(2)
	print('Token update successful!\n')


def sigterm_handler(signal, frame):
	'''
	Logs program run time when we get sigterm.
	'''
	logging.info(f'✅ Got SIGTERM. Runtime: {datetime.datetime.now() - STARTUP_TIME}.')
	logging.info(f'Signal: {signal}, frame: {frame}.')
	sys.exit(0)


def apscheduler_event_listener(event):
	'''
	Listens to exceptions coming in from apscheduler's threads.
	'''
	if event.exception:
		logging.critical(f'Error: scheduled job raised an exception: {event.exception}')
		logging.critical('Exception traceback follows:')
		logging.critical(event.traceback)


if __name__ == '__main__':
	# some global vars for use in other functions
	global VERSION, OWNER
	global BOT_ID, BOT_USERNAME
	global DATA_DIR, STARTUP_TIME

	# current version, set DATA_DIR
	VERSION = '1.7.4'
	DATA_DIR = 'launchbot'

	# log startup time
	STARTUP_TIME = time.time()

	# setup argparse
	parser = argparse.ArgumentParser('launchbot.py')

	# add args
	parser.add_argument(
		'-start', dest='start', help='Starts the bot', action='store_true')
	parser.add_argument(
		'-debug', dest='debug', help='Disabled the activity indicator', action='store_true')
	parser.add_argument(
		'--new-bot-token', dest='update_token', help='Set a new bot token', action='store_true')
	parser.add_argument(
		'--force-api-update', dest='force_api_update',
		help='Force an API update now', action='store_true')

	# set defaults, parse
	parser.set_defaults(start=False, newBotToken=False, debug=False)
	args = parser.parse_args()

	if args.update_token:
		update_token(data_dir=DATA_DIR, new_tokens={args.update_token})

	if not args.start:
		sys.exit('No start command given, exiting. To start the bot, include -start in options.')

	# load config, create bot
	config = load_config(data_dir=DATA_DIR)
	updater = Updater(config['bot_token'], use_context=True)

	# get the bot: if we get a telegram.error.Unauthorized, the token is incorrect
	try:
		bot_specs = updater.bot.getMe()
	except telegram.error.Unauthorized:
		sys.exit('⚠️ Error: unable to init bot! Double-check your API token in bot-config.json!')

	# get the bot's username and id
	BOT_USERNAME = bot_specs.username
	BOT_ID = bot_specs.id
	OWNER = config['owner']

	# valid commands we monitor for
	global VALID_COMMANDS
	VALID_COMMANDS = {
		'/start', '/help', '/next', '/notify',
		'/statistics', '/schedule', '/feedback'}

	# generate the "alternate" commands we listen for, as in ones suffixed with the bot's username
	alt_commands = set()
	for command in VALID_COMMANDS:
		alt_commands.add(f'{command}@{BOT_USERNAME.lower()}')

	# update valid_commands to include the "alternate" commands by doing a set union
	VALID_COMMANDS = tuple(VALID_COMMANDS.union(alt_commands))

	# all the launch providers supported; used in many places, so declared globally here
	# TODO move to utils
	global provider_by_cc
	provider_by_cc = {
		'USA': {
			'NASA', 'SpaceX', 'ULA', 'Rocket Lab Ltd', 'Blue Origin', 'Astra Space', 'Virgin Orbit',
			'Firefly Aerospace', 'Northrop Grumman', 'International Launch Services'},

		'EU': {
			'Arianespace', 'Eurockot', 'Starsem SA'},

		'CHN': {
			'CASC', 'ExPace', 'Galactic Energy'},

		'RUS': {
			'KhSC', 'ISC Kosmotras', 'Russian Space Forces', 'Eurockot', 'Sea Launch',
			'Land Launch', 'Starsem SA', 'International Launch Services', 'ROSCOSMOS'},

		'IND': {
			'ISRO', 'Antrix Corporation'},

		'JPN': {
			'JAXA', 'Mitsubishi Heavy Industries', 'Interstellar Technologies'}
	}

	''' This is effectively a reverse-map, mapping the short names used in the notify-command's
	buttons into the proper LSP names, as found in the database. '''
	global provider_name_map
	provider_name_map = {
		'Rocket Lab': 'Rocket Lab Ltd',
		'Northrop Grumman': 'Northrop Grumman Innovation Systems',
		'ROSCOSMOS': 'Russian Federal Space Agency (ROSCOSMOS)'}

	'''
	Keep track of chats doing time zone setup, so we don't update
	the time zone if someone responds to a bot message with a location,
	for whatever reason. People are weird.

	TODO use redis
	'''
	global time_zone_setup_chats
	time_zone_setup_chats = {}

	'''
	LSP ID -> name, flag dictionary

	Used to shorten the names, so we don't end up with super long messages

	This dictionary also maps custom shortened names (Northrop Grumman, Starsem)
	to their real ID. Also used in cases where a weird name is used by LL, like
	RFSA for Roscosmos.
	'''
	global LSP_IDs
	LSP_IDs = {
		121: ['SpaceX', '🇺🇸'], 147: ['Rocket Lab', '🇺🇸'], 265:['Firefly', '🇺🇸'],
		141: ['Blue Origin', '🇺🇸'], 99: ['Northrop Grumman', '🇺🇸'],
		115: ['Arianespace', '🇪🇺'], 124: ['ULA', '🇺🇸'], 98: ['Mitsubishi Heavy Industries', '🇯🇵'],
		1002:['Interstellar Tech.', '🇯🇵'], 88: ['CASC', '🇨🇳'], 190: ['Antrix Corporation', '🇮🇳'],
		122: ['Sea Launch', '🇷🇺'], 118: ['ILS', '🇺🇸🇷🇺'], 193: ['Eurockot', '🇪🇺🇷🇺'],
		119: ['ISC Kosmotras', '🇷🇺🇺🇦🇰🇿'], 123: ['Starsem', '🇪🇺🇷🇺'], 194: ['ExPace', '🇨🇳'],
		63: ['Roscosmos', '🇷🇺']
	}

	# start command timers, store in memory instead of storage to reduce disk writes
	# TODO use redis or memcached
	global command_cooldowns, chat_command_calls, spammers, ignored_users
	command_cooldowns, chat_command_calls = {}, {}
	spammers, ignored_users = set(), set()

	# initialize the timer dict to avoid spam
	# TODO use redis or memcached
	command_cooldowns['command_timers'] = {}
	for command in VALID_COMMANDS:
		command_cooldowns['command_timers'][command.replace('/','')] = 1

	# init the feedback store; used to store the message IDs so we can store feedback
	# TODO use redis or memcached
	global feedback_message_IDs
	feedback_message_IDs = set()

	# handle sigterm
	signal.signal(signal.SIGTERM, sigterm_handler)

	# save log to disk
	log = os.path.join(DATA_DIR, 'log-file.log')

	# init log (disk)
	logging.basicConfig(
		filename=log, level=logging.DEBUG, format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')

	# disable logging for urllib and requests because jesus fuck they make a lot of spam
	logging.getLogger('requests').setLevel(logging.CRITICAL)
	logging.getLogger('urllib3').setLevel(logging.CRITICAL)
	logging.getLogger('chardet.charsetprober').setLevel(logging.CRITICAL)
	logging.getLogger('apscheduler').setLevel(logging.WARNING)
	logging.getLogger('git').setLevel(logging.WARNING)
	logging.getLogger('telegram').setLevel(logging.ERROR)
	logging.getLogger('telegram.bot').setLevel(logging.ERROR)
	logging.getLogger('telegram.ext.updater').setLevel(logging.ERROR)
	logging.getLogger('telegram.vendor').setLevel(logging.ERROR)
	logging.getLogger('telegram.error.TelegramError').setLevel(logging.ERROR)

	if not args.debug:
		# init console log if not in debug mode
		console = logging.StreamHandler()
		console.setLevel(logging.DEBUG)

		# add the handler to the root logger
		logging.getLogger().addHandler(console)

	# add color
	coloredlogs.install(level='DEBUG')

	# if not in debug mode, show pretty prints
	if not args.debug:
		print(f"🚀 LaunchBot | version {VERSION}")
		print("Don't close this window or set the computer to sleep. Quit: ctrl + c.")
		time.sleep(0.5)

	# init and start scheduler
	scheduler = BackgroundScheduler()
	scheduler.start()

	# add event listener to scheduler
	scheduler.add_listener(apscheduler_event_listener, EVENT_JOB_ERROR)

	# get the dispatcher to register handlers
	dispatcher = updater.dispatcher

	# register command handlers
	dispatcher.add_handler(
		CommandHandler(command='notify', callback=notify))
	dispatcher.add_handler(
		CommandHandler(command='next', callback=next_flight))
	dispatcher.add_handler(
		CommandHandler(command='feedback', callback=feedback))
	dispatcher.add_handler(
		CommandHandler(command='statistics', callback=statistics))
	dispatcher.add_handler(
		CommandHandler(command='schedule', callback=flight_schedule))
	dispatcher.add_handler(
		CommandHandler(command=('start', 'help'), callback=start))

	# register callback handler
	dispatcher.add_handler(
		CallbackQueryHandler(callback=callback_handler, run_async=True))

	# register specific handlers (text for feedback, location for time zone stuff)
	dispatcher.add_handler(MessageHandler(
		Filters.reply & ~Filters.forwarded & ~Filters.command & ~Filters.location,
		callback=feedback_handler))
	dispatcher.add_handler(MessageHandler(
		Filters.reply & Filters.location & ~Filters.forwarded & ~Filters.command,
		callback=location_handler))
	dispatcher.add_handler(MessageHandler(
		Filters.status_update, callback=generic_update_handler))

	# if owner has been set, add handler for debug command in private
	if OWNER != 0:
		dispatcher.add_handler(
			CommandHandler(command='debug', callback=admin_handler, filters=Filters.chat(OWNER)))

	# all up to date, start polling
	updater.start_polling()

	# check if --force-api-update flag was given
	if args.force_api_update:
		logging.info('--force-api-update given: updating db...')
		api_update_on_restart()

	# start API and notification scheduler
	api_call_scheduler(
		db_path=DATA_DIR, ignore_60=False, scheduler=scheduler, bot_username=BOT_USERNAME,
		bot=updater.bot)

	# send startup message
	if OWNER != 0:
		try:
			updater.bot.send_message(
				OWNER, f'🤖 Bot started with args: `{sys.argv}`', parse_mode='Markdown')
		except telegram.error.Unauthorized:
			pass

	# fancy prints so the user can tell that we're actually doing something
	if not args.debug:
		# hide cursor for pretty print
		cursor.hide()
		try:
			while True:
				for char in ('⠷', '⠯', '⠟', '⠻', '⠽', '⠾'):
					sys.stdout.write('%s\r' % '  Connected to Telegram! To quit: ctrl + c.')
					sys.stdout.write('\033[92m%s\r\033[0m' % char)
					sys.stdout.flush()
					time.sleep(0.1)

		except KeyboardInterrupt:
			# on exit, show cursor as otherwise it'll stay hidden
			cursor.show()
			scheduler.shutdown()
			run_time = time_delta_to_legible_eta(int(time.time() - STARTUP_TIME), True)
			run_time_str = f'\n🔶 Program ending... Runtime: {run_time}.'
			logging.warning(run_time_str)

			sys.exit('Press ctrl+c again to quit!')

		except Exception as error:
			updater.bot.send_message(OWNER, f'⚠️ Shutting down! exception: {error}')

	else:
		while True:
			time.sleep(10)
