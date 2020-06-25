# -*- coding: utf-8 -*-
# /usr/bin/python3
import os, sys, time, ssl, datetime, logging, math, requests
import inspect, signal, random, sqlite3, difflib

import telepot, cursor, schedule, pytz
import ujson as json

from hashlib import sha1
from uptime import uptime
from timeit import default_timer as timer
from timezonefinder import TimezoneFinder
from telepot.loop import MessageLoop
from telepot.namedtuple import InlineKeyboardMarkup, InlineKeyboardButton, ForceReply, ReplyKeyboardMarkup, KeyboardButton, ReplyKeyboardRemove

# changelog: add "show changelog" button to /help
'''
*Changelog* for version {VERSION.split('.')[0]}.{VERSION.split('.')[1]} (May 2020)
- Added preferences to /notify@{BOT_USERNAME} ⚙️
- You can now choose when you receive notifications (24h/12h/etc.)
- Updates to the schedule command
- Added probability of launch to /next@{BOT_USERNAME}
- /next@{BOT_USERNAME} now indicates if a launch countdown is on hold
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
		conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
		c = conn.cursor()
		
		c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
		conn.commit()
		conn.close()

		if debug_log:
			logging.info(f'⚠️ Bot removed from chat {anonymize_id(chat)} – notifications database cleaned [1]')
		return

	# group upgraded to a supergroup; migrate data
	if 'migrate_to_chat_id' in msg:
		old_ID = chat
		new_ID = msg['migrate_to_chat_id']

		if debug_log:
			logging.info(f'⚠️ Group {anonymize_id(old_ID)} migrated to {anonymize_id(new_ID)} - '
						 f'starting database migration...')

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
	elif 'left_chat_member' in msg and msg['left_chat_member']['id'] == BOT_ID:
		# bot kicked; remove corresponding chat IDs from notification database
		conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
		c = conn.cursor()
		
		c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
		conn.commit()
		conn.close()

		if debug_log:
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

		if debug_log:
			logging.info(f'🌟 Bot added to a new chat! chat_id={anonymize_id(chat)}. Sent user the new inline keyboard. [1]')

		return
	
	try:
		command_split = msg['text'].strip().split(' ')
	except KeyError:
		pass
	except Exception as e:
		if debug_log:
			logging.info(f'🛑 Error generating command split, returning: {e}')
			logging.info(f'msg object: {msg}')
		return

	# verify that the user who sent this is not in spammers
	try:
		if msg['from']['id'] in ignored_users:
			if debug_log:
				logging.info(f'😎 Message from spamming user ignored successfully')
			
			return
	except: # all users don't have a user ID, so check for the regular username as well
		if 'author_signature' in msg:
			if msg['author_signature'] in ignored_users:
				if debug_log:
					logging.info(f'😎 Message from spamming user (no UID) ignored successfully')
			
			return

	# regular text — check if it's feedback. If not, return.
	if content_type == 'text' and command_split[0][0] != '/' and debug_log:
		if 'reply_to_message' in msg:
			if msg['reply_to_message']['message_id'] in feedback_message_IDs and 'text' in msg:
				logging.info(f'✍️ Received feedback: {msg["text"]}')

				sender = bot.getChatMember(chat, msg['from']['id'])
				if sender['status'] == 'creator' or sender['status'] == 'administrator' or chat_type == 'private':
					bot.sendMessage(chat, f'😄 Thank you for your feedback!', reply_to_message_id=msg['message_id'])

					try: # remove the original feedback message
						bot.deleteMessage((chat, msg['reply_to_message']['message_id']))
					except Exception as e:
						if debug_log:
							logging.info(f'Unable to remove sent feedback message with params chat={chat}, message_id={msg["reply_to_message"]["message_id"]}: {e}')

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
			if debug_log:
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
				if debug_log:
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
								if debug_log:
									if success:
										logging.info(f'✋ {command} called by a non-admin in {anonymize_id(chat)} ({anonymize_id(msg["from"]["id"])}): successfully deleted message! ✅')
									else:
										logging.info(f'✋ {command} called by a non-admin in {anonymize_id(chat)} ({anonymize_id(msg["from"]["id"])}): unable to delete message (success != True. Type:{type(success)}, val:{success}) ⚠️')
							except Exception as e:
								if debug_log:
									logging.info(f'⚠️ Could not delete message sent by non-admin: {e}')

						else:
							if debug_log:
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

					if debug_log:
						logging.info(f'🌟 Bot added to a new chat! chat_id={anonymize_id(chat)}. Sent user the new inline keyboard. [2]')

			# /next
			elif command == '/next':
				next_flight(msg, 0, True, None)

			# /notify
			elif command == '/notify':
				notify(msg)

			# /statistics
			elif command == '/statistics':
				statistics(chat, 'cmd')

			# /schedule)
			elif command == '/schedule':
				flight_schedule(msg, True, 'vehicle')

			# /feedback
			elif command == '/feedback':
				feedback(msg)

			if debug_log:
				t_elapsed = timer() - start
				if msg['from']['id'] != OWNER and command != '/start':
					try:
						logging.info(f'🕹 {command} called by {anonymize_id(chat)} | args: {command_split[1:]} | {(1000*t_elapsed):.0f} ms')
					except:
						logging.info(f'🕹 {command} called by {anonymize_id(chat)} | args: [] | {(1000*t_elapsed):.0f} ms')

			# store statistics here, so our stats database can't be spammed either
			update_statistics({'commands':1})

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
			message_text = f'''
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

		local_text = f'Press to enable all' if disabled_count != 0 else f'Press to disable all'

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
		inline_keyboard.append([InlineKeyboardButton(text=f'⏮ Return to menu', callback_data=f'notify/main_menu')])

		keyboard = InlineKeyboardMarkup(
			inline_keyboard=inline_keyboard)

		# tuple containing necessary information to edit the message
		msg_identifier = (msg['message']['chat']['id'],msg['message']['message_id'])

		# now we have the keyboard; update the previous keyboard
		bot.editMessageReplyMarkup(msg_identifier, reply_markup=keyboard)

		if debug_log and chat != OWNER:
			logging.info(f'🔀 {flag_map[country_code]}-view loaded for {anonymize_id(chat)}')

		return

	try:
		query_id, from_id, query_data = telepot.glance(msg, flavor='callback_query')
	
	except Exception as caught_exception:
		if debug_log:
			logging.info(f'⚠️ Exception in callback_handler: {caught_exception}')

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
					if debug_log:
						logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')

				if debug_log:
					logging.info(f'✋ Callback query called by a non-admin in {anonymize_id(chat)}, returning | {(1000*(timer() - start)):.0f} ms')
				
				return

	# callbacks only supported for notify at the moment; verify it's a notify command
	if input_data[0] not in ('notify', 'mute', 'next_flight', 'schedule', 'prefs', 'stats'):
		if debug_log:
			logging.info(f'⚠️ Incorrect input data in callback_handler! input_data={input_data} | {(1000*(timer() - start)):.0f} ms')

		return

	# check if notification database exists
	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir,'notifications.db')):
		create_notify_database()

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
				if debug_log:
					logging.info(f'Error finding country code {country_code} from provider_by_cc!')
				return

			update_list_view(msg, chat, provider_list)

			try:
				bot.answerCallbackQuery(query_id, text=f'{flag_map[country_code]}')
			except Exception as error:
				if debug_log:
					logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')

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
				if debug_log:
					logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')

			if debug_log and chat != OWNER:
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
					1:f'🔔 Enabled all notifications 🌍',
					0:f'🔕 Disabled all notifications 🌍'}[new_status]

			# give feedback to the button press
			try:
				bot.answerCallbackQuery(query_id, text=reply_text, show_alert=True)
			except Exception as error:
				if debug_log:
					logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')

			if debug_log and chat != OWNER:
				logging.info(f'{anonymize_id(chat)} {reply_text} | {(1000*(timer() - start)):.0f} ms')

			# update list view if an lsp button was pressed
			if input_data[2] != 'all':
				country_code = input_data[4]
				try:
					provider_list = provider_by_cc[country_code]
				except:
					if debug_log:
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
				if debug_log:
					logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')

			if debug_log and chat != OWNER:
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
				if debug_log:
					logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')
			
			if debug_log and chat != OWNER:
				if new_toggle_state == 0:
					logging.info(f'🔇 {anonymize_id(chat)} muted a launch for {input_data[1]} (launch_id={input_data[2]}) | {(1000*(timer() - start)):.0f} ms')
				else:
					logging.info(f'🔊 {anonymize_id(chat)} unmuted a launch for {input_data[1]} (launch_id={input_data[2]}) | {(1000*(timer() - start)):.0f} ms')

		except Exception as exception:
			if debug_log:
				logging.info(f'⚠️ User attempted to mute/unmute a launch, but no reply could be provided (sending message...): {exception}')

			try:
				bot.sendMessage(chat, callback_text, parse_mode='Markdown')
			except Exception as exception:
				if debug_log:
					logging.info(f'🛑 Ran into an error sending the mute/unmute message to chat={chat}! {exception}')

		# toggle the mute here, so we can give more responsive feedback
		toggle_launch_mute(chat, input_data[1], input_data[2], input_data[3])	

	elif input_data[0] == 'next_flight':
		# next_flight(msg, current_index, command_invoke, cmd):
		# next_flight/{next/prev}/{current_index}/{cmd}
		# next_flight/refresh/0/{cmd}'
		if input_data[1] not in {'next', 'prev', 'refresh'}:
			if debug_log:
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

				if debug_log:
					logging.info(f'🔎 No launches found after next refresh. Sent user the "No launches found" message.')

			except Exception as e:
				if debug_log:
					logging.info(f'⚠️ No launches found after refresh! {e}')

		# query reply text
		query_reply_text = {'next': 'Next flight ⏩', 'prev': '⏪ Previous flight', 'refresh': '🔄 Refreshed data!'}[input_data[1]]

		# now we have the keyboard; update the previous keyboard
		try:
			bot.editMessageText(msg_identifier, text=new_message_text, reply_markup=keyboard, parse_mode='MarkdownV2')
		except telepot.exception.TelegramError as exception:
			if exception.error_code == 400 and 'Bad Request: message is not modified' in exception.description:
				pass
			else:
				if debug_log:
					logging.info(f'⚠️ TelegramError updating message text: {exception}')
		
		try:
			bot.answerCallbackQuery(query_id, text=query_reply_text)
		except Exception as error:
			if debug_log:
				logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')

		if debug_log and chat != OWNER:
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
					if debug_log:
						logging.info(f'⚠️ Ran into error when answering callbackquery: {error}')
				pass
			else:
				if debug_log:
					logging.info(f'⚠️ TelegramError updating message text: {exception}')

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
				if locale_string != None:
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
				bot.answerCallbackQuery(query_id, f'🌎 Time zone settings loaded')


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

				bot.editMessageText(
					msg_identifier, text=inspect.cleandoc(text), parse_mode='Markdown',
					reply_markup=keyboard, disable_web_page_preview=True
				)

			elif input_data[2] == 'start':
				if bot.getChat(chat)['type'] != 'private':
					bot.sendMessage(chat, text=f'⚠️ This method only works for private chats. This is a Telegram API limitation.')

				new_text = f'🌎 Set your time zone with the button below, where your keyboard should be. To cancel, select "cancel time zone setup" from the message above.'

				# construct the keyboard so we can request a location
				keyboard = ReplyKeyboardMarkup(
					resize_keyboard=True,
					one_time_keyboard=True,
					keyboard=[
						[KeyboardButton(text='📍 Set your time zone', request_location=True)]
					]
				)

				new_inline_text = f'❗️ To cancel time zone setup and remove the keyboard, use the button below.'
				inline_keyboard = InlineKeyboardMarkup(
					inline_keyboard = [
						[InlineKeyboardButton(text='🚫 Cancel time zone setup', callback_data=f'prefs/timezone/cancel')]
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
				text = f'''🌎 Automatic time zone setup

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
			if debug_log and chat != OWNER:
				logging.info(f'🔄 {anonymize_id(chat)} refreshed statistics')

			new_text = inspect.cleandoc(statistics(chat, 'refresh'))
			if msg['message']['text'] == new_text.replace('*',''):
				bot.answerCallbackQuery(query_id, text=f'🔄 Statistics are up to date!')
				return

			keyboard = InlineKeyboardMarkup(
				inline_keyboard=[[InlineKeyboardButton(text='🔄 Refresh statistics', callback_data=f'stats/refresh')]])

			bot.editMessageText(msg_identifier, text=new_text, reply_markup=keyboard, parse_mode='Markdown')
			bot.answerCallbackQuery(query_id, text=f'🔄 Statistics refreshed!')

	# update stats, except if command was a stats refresh
	if input_data[0] != 'stats':
		update_statistics({'commands':1})


def load_locale_string(chat):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
	c = conn.cursor()

	try:
		c.execute("SELECT timezone_str FROM preferences WHERE chat = ?",(chat,))
	except:
		return None

	query_return = c.fetchall()
	if len(query_return) == 0:
		return None

	if query_return[0][0] != None:
		return query_return[0][0]

	return None


def remove_time_zone_information(chat):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
	c = conn.cursor()

	try:
		c.execute("UPDATE preferences SET timezone_str = ?, timezone = ? WHERE chat = ?", (None, None, chat))
		if debug_log:
			logging.info(f'✅ User successfully removed their time zone information!')
	
	except Exception as e:
		if debug_log:
			logging.info(f'❓ User tried to remove their time zone information, but ran into exception: {e}')

	conn.commit()
	conn.close()


# restrict command send frequency to avoid spam
def timer_handle(command, chat, user):
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
					return

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

				if debug_log:
					logging.info(f'⚠️ User {anonymize_id(user)} now has {spammer.get_offenses()} spam offenses.')

				if spammer.get_offenses() >= 10:
					run_time = datetime.datetime.now() - STARTUP_TIME
					if run_time.seconds > 60:
						ignored_users.add(user)
						if debug_log:
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
				if debug_log:
					logging.info(f'⚠️ Added user {anonymize_id(user)} to spammers.')

			return False

	return True


# load the current status of the permissions into memory
def load_permissions_status():
	return


def update_time_zone_string(chat, time_zone):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
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


def update_time_zone_value(chat, offset):
	# connect to database
	conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
	c = conn.cursor()

	# translate offset to hours
	if 'h' in offset:
		offset = int(offset.replace('h',''))
	elif 'm' in offset:
		offset = float(int(offset.replace('m',''))/60)

	current_value = load_time_zone_status(chat, False)
	current_value = 0 if current_value == None else current_value
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


def load_time_zone_status(chat, readable):
	conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
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
		time_zone_string_found = True if query_return[0][1] != None else False

	if not readable:
		if len(query_return) == 0:
			return 0
		else:
			if not time_zone_string_found:
				if query_return[0][0] == None:
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
			if query_return[0][0] == None:
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


def get_user_notifications_status(chat, provider_list):
	'''
	The function takes a list of provider strings as input, and returns a dictionary containing
	the notification status for all of the providers for a given chat.
	'''

	# Establish connection
	launch_dir = 'data/launch'
	conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	c = conn.cursor()

	c.execute("SELECT * FROM notify WHERE chat = ?",(chat,))
	query_return = c.fetchall()
	conn.close()

	notification_statuses = {'All': 0}
	for provider in provider_list:
		if provider in provider_name_map.keys():
			provider = provider_name_map[provider]
		
		notification_statuses[provider] = 0

	if len(query_return) == 0:
		return notification_statuses

	all_flag = False
	for row in query_return:
		provider, enabled_status = row[1], row[3]
		
		if enabled_status == 1:
			notification_statuses[provider] = 1

		if provider == 'All' and enabled_status == 1:
			all_flag = True

	notification_statuses['All'] = 1 if all_flag else 0
	return notification_statuses


# toggle a notification for chat of type (toggle_type, keyword) with the value keyword
def toggle_notification(chat, toggle_type, keyword, all_toggle_new_status):
	# Establish connection
	launch_dir = 'data/launch'
	conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	c = conn.cursor()

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

	# toggle each notification
	if toggle_type == 'lsp':
		for provider in provider_list:
			try: # insert as new; if we run into an exception, get the status and update accordingly
				c.execute("INSERT INTO notify (chat, keyword, muted_launches, enabled) VALUES (?, ?, ?, 1)", (chat, provider, None))
				new_status = 1
			
			except: # already found, update status
				# pull the current status
				c.execute("SELECT * FROM notify WHERE chat = ? AND keyword = ?", (chat, provider))
				query_return = c.fetchall()

				if len(query_return) == 0:
					if debug_log:
						logging.info(f'⚠️ Error getting current status for provider "{provider}" in toggle_notification()')
					return None
				
				new_status = 0 if query_return[0][3] == 1 else 1
				c.execute("UPDATE notify SET enabled = ? WHERE chat = ? AND keyword = ?", (new_status, chat, provider))

	elif toggle_type in {'all', 'country_code'}:
		for provider in provider_list:
			try: # insert as new; if we run into an exception, get the status and update accordingly
				c.execute("INSERT INTO notify (chat, keyword, muted_launches, enabled) VALUES (?, ?, ?, ?)", (chat, provider, None, all_toggle_new_status))
			
			except: # already found, update status
				c.execute("UPDATE notify SET enabled = ? WHERE chat = ? AND keyword = ?", (all_toggle_new_status, chat, provider))

	conn.commit()
	conn.close()

	if toggle_type == 'lsp':
		return new_status
	else:
		return all_toggle_new_status


def update_notif_preference(chat, notification_type):
	# get current status
	old_preferences = list(get_notif_preference(chat))

	update_index = {'24h': 0, '12h': 1, '1h': 2, '5m': 3}[notification_type]
	new_state = 1 if old_preferences[update_index] == 0 else 0

	old_preferences[update_index] = new_state
	new_preferences = ','.join(str(val) for val in old_preferences)

	conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
	c = conn.cursor()

	# preferences (chat TEXT, notifications TEXT, timezone TEXT, postpone INTEGER, commands TEXT, PRIMARY KEY (chat))
	try:
		c.execute("INSERT INTO preferences (chat, notifications, timezone, timezone_str, postpone, commands) VALUES (?, ?, ?, ?, ?, ?)",
		 (chat, new_preferences, None, None, 1, None))
	except:
		c.execute("UPDATE preferences SET notifications = ? WHERE chat = ?", (new_preferences, chat))

	conn.commit()
	conn.close()

	if debug_log and chat != OWNER:
		logging.info(f'📩 {anonymize_id(chat)} {"enabled (🔔)" if new_state == 1 else "disabled (🔕)"} {notification_type} notification')

	return new_state


def get_notif_preference(chat):
	'''
	Returns the notification preferences (24h,12h,1h,5m) as a tuple of boolean values
	'''
	conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
	c = conn.cursor()

	c.execute("SELECT notifications FROM preferences WHERE chat = ?",(chat,))
	query_return = c.fetchall()
	conn.close()

	if len(query_return) == 0:
		return (1, 1, 1, 1)

	notif_preferences = query_return[0][0].split(',')
	return (
		int(notif_preferences[0]), int(notif_preferences[1]),
		int(notif_preferences[2]), int(notif_preferences[3])
		)


def chat_preferences(chat):
	'''
	This function is called when user wants to interact with their preferences.
	Sends the user an interactive keyboard to view and edit their prefs with.

	Functions:
	- set timezone
	- set notification times
	- allow/disallow regular users to call bot's commands
	'''
	if not os.path.isfile(os.path.join('data', 'preferences.db')):
		conn = sqlite3.connect(os.path.join('data', 'preferences.db'))
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


def anonymize_id(chat):
	return sha1(str(chat).encode('utf-8')).hexdigest()[0:6]


def name_from_provider_id(provider_id):
	launch_dir = 'data/launch'
	conn = sqlite3.connect(os.path.join(launch_dir,'launches.db'))
	c = conn.cursor()

	# get provider name corresponding to this ID
	c.execute("SELECT lsp_name FROM launches WHERE keywords = ?",(provider_id,))
	query_return = c.fetchall()

	if len(query_return) != 0:
		return query_return[0][0]
	
	return provider_id


def toggle_launch_mute(chat, launch_provider, launch_id, toggle):
	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir,'notifications.db')):
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
	conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	c = conn.cursor()
	
	# pull the current muted_launches field
	c.execute("SELECT muted_launches FROM notify WHERE chat = ? AND keyword = ?", (chat, launch_provider))
	query_return = c.fetchall()

	# mute
	if toggle == '1':
		if len(query_return) == 0:
			new_mute_string = str(launch_id)
		else:
			if query_return[0][0] == None:
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
			if mute_string == None:
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


def notify(msg):
	content_type, chat_type, chat = telepot.glance(msg, flavor='chat')
	launch_dir = 'data/launch'

	# check if notification database exists
	if not os.path.isfile(os.path.join(launch_dir,'notifications.db')):
		create_notify_database()

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


# receive feedback from users. Mainly as ForceReply and inline-features practice, though.
def feedback(msg):
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


# display a very simple schedule for upcoming flights (all)
def flight_schedule(msg, command_invoke, call_type):
	if command_invoke:
		content_type, chat_type, chat = telepot.glance(msg, flavor='chat')
	else:
		chat = msg['message']['chat']['id']

	# open db connection
	conn = sqlite3.connect(os.path.join('data', 'launch', 'launches.db'))
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
		9: 'Septemper', 10: 'October', 11: 'November', 12: 'December' }

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
		flt_str = flag if flag != None else ''

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
	launch_dir = 'data/launch'
	if command_invoke == True:
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
	notify_conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	notify_cursor = notify_conn.cursor()

	try:
		notify_cursor.execute('''SELECT * FROM notify WHERE chat = ?''', (chat,))
	except:
		create_notify_database()
	
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
		query_return = c.fetchall()

	# if no next command, assume the user wants to know the next launch they're interested in
	elif cmd == None:
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
			if debug_log:
				logging.info(f'⚠️ Exception setting query_return: {error}')

			query_return = query_return[0]
	else:
		msg_text = '🔄 No launches found! Try enabling notifications for other providers, or searching for all flights.'
		inline_keyboard = []
		inline_keyboard.append([InlineKeyboardButton(text='🔔 Adjust your notification settings', callback_data=f'notify/main_menu/refresh_text')])
		inline_keyboard.append([InlineKeyboardButton(text='🔎 Search for all flights', callback_data=f'next_flight/refresh/0/all')])
		keyboard = InlineKeyboardMarkup(inline_keyboard=inline_keyboard)

		bot.sendMessage(chat, msg_text, reply_markup=keyboard)

		if debug_log:
			logging.info(f'🔎 No launches found in next. Sent user the "No launches found" message.')

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

	# new info
	tbd_date = query_return[20]
	tbd_time = query_return[21]
	launch_prob = query_return[22]
	in_hold = query_return[18]

	tbd_date = True if tbd_date == 1 else False
	tbd_time = True if tbd_time == 1 else False
	in_hold = True if in_hold == 1 else False

	if lsp_name == 'SpaceX':
		spx_info_str, spx_orbit_info = spx_info_str_gen(mission_name, 0)
		if spx_info_str != None:
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
	if user_notif_enabled == None:
		if lsp_name in enabled or lsp_short in enabled:
			user_notif_enabled = True
		elif len(difflib.get_close_matches(lsp_name, enabled)) == 1:
			user_notif_enabled = True
		elif len(difflib.get_close_matches(lsp_short, enabled)) == 1:
			user_notif_enabled = True
		elif lsp_name in disabled or lsp_short in disabled:
			user_notif_enabled = False
		else:
			if debug_log:
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

		day_suff = f'day' if t_d == 1 else f'days'
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
		9: 'Septemper', 10: 'October', 11: 'November', 12: 'December'
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
	if lsp_flag != None:
		header = f'🚀 *Next launch* is by {lsp_name} {lsp_flag}\n*Mission* {mission_name}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'
	else:
		header = f'🚀 *Next launch* is by {lsp_name}\n*Mission* {mission_name}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'

	if lsp_name.replace('`','') == 'SpaceX':
		if spx_orbit_info != '' and spx_orbit_info != None:
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
	if launch_prob != -1 and launch_prob != None:
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
		vid_str = f'🔴 Watch the launch LinkTextGoesHere'
	elif vid_url != '' and eta.seconds <= 3600*4 and 'DM2' in mission_name and eta.days == 0:
		vid_str = f'🔴 Watch the launch LinkTextGoesHere'
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

	if current_index == 0 and command_invoke == True:
		if max_index > 1:
			bot.sendMessage(chat, msg_text, parse_mode='MarkdownV2', reply_markup=keyboard)
		else:
			bot.sendMessage(chat, msg_text, parse_mode='MarkdownV2')
	else:
		return msg_text, keyboard

	return


# handles API update requests and decides on which notification to send
def launch_update_check():
	# compare data to data found in local launch database
	# send a notification if launch time is approaching

	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir, 'launches.db')):
		create_launch_database()
		get_launch_updates(None)

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
		NET <= {unix_5m_threshold} AND NET >= {now_timestamp} AND notify5min = 0
		''')

	query_return = c.fetchall()
	if len(query_return) == 0:
		return

	# we presumably have at least one launch now that has an unsent notification
	# update the database, then check again
	if debug_log:
		logging.info(f'⏰ Found {len(query_return)} pending notification(s)... Updating database to verify.')
	
	get_launch_updates(None)
	c.execute(f'''SELECT * FROM launches 
		WHERE 
		NET <= {unix_24h_threshold} AND NET >= {now_timestamp} AND notify24h = 0 OR
		NET <= {unix_12h_threshold} AND NET >= {now_timestamp} AND notify12h = 0 OR 
		NET <= {unix_60m_threshold} AND NET >= {now_timestamp} AND notify60min = 0 OR
		NET <= {unix_5m_threshold} AND NET >= {now_timestamp} AND notify5min = 0''')
	
	query_return = c.fetchall()
	if len(query_return) == 0:
		if debug_log:
			logging.info(f'❓ No notifications found after re-check. Returning.')
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
				logging.info(f'⚠️ Error setting notif_class in notification_handler(): curr_Tminus:{curr_Tminus}, launch:{query_return[0][1]}.\
				 24h: {status_24h}, 12h: {status_12h}, 1h: {status_1h}, 5m: {status_5m}')
			
			return

		else:
			if debug_log:
				logging.info(f'✅ Set {len(notif_class)} notif_classes. Timestamp: {now_timestamp}, flt NET: {NET}')

		# send the notifications
		notification_handler(row, notif_class, False)

	return


# handles r/spacex api requests
def spx_api_handler():
	'''
	This function performs an API call to the r/SpaceX API and updates the database with
	the returned information. 

	construct_params():
		Dynamically constructs the parameters for the request URL so we don't have to do it manually.
	
	multi_parse():
		Parses the returned json-file by iterating over the launches found in the json, and updating
		the database with the information.
	'''

	def construct_params(PARAMS):
		param_url, i = '', 0
		if PARAMS is not None:
			for key, val in PARAMS.items():
				if i == 0:
					param_url += f'?{key}={val}'
				else:
					param_url += f'&{key}={val}'
				i += 1

		return param_url

	def multi_parse(json, launch_count):
		# check if db exists
		launch_dir = 'data/launch'
		if not os.path.isfile(os.path.join(launch_dir, 'spx-launches.db')):
			create_spx_database()

		# open connection
		conn = sqlite3.connect(os.path.join(launch_dir, 'spx-launches.db'))
		c = conn.cursor()

		# launch, id, keywords, countrycode, NET, T-, notify24hour, notify12hour, notify60min, notify5min, success, launched, hold
		for i in range(0, launch_count):
			# json of flight i
			try:
				launch = launch_json[i]
			except KeyError as error:
				try:
					if 'error' in launch_json.keys():
						if debug_log:
							logging.info(f'⚠️ Error in spx_api_handler multi_parse: {launch_json["error"]}')

					elif debug_log:
						logging.info(f'⚠️ Got KeyError in spx_api_handler(multi_parse()), returning: {error}')
						logging.info(f'⚠️ range: 0,{launch_count}, i:{i}, launch_json: {launch_json}')

				except Exception as error:
					logging.info(f'Other error in multi_parse: {error}')
					pass

				return

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
				for core, n in zip(cores, range(len(cores))):
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

					if n < len(cores) - 1:
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
	PARAMS = {'limit': 20, 'start': f'{now.year}-{now.month}-{now.day}'}
	API_URL = 'https://api.spacexdata.com'
	API_VERSION = 'v3'

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{construct_params(PARAMS)}'
	
	try: # perform the API call
		API_RESPONSE = requests.get(API_CALL)
	except Exception:
		if debug_log:
			logging.info(f'🛑 Exception in r/SpaceX API request')
			logging.info(f'⚠️ Trying again after 3 seconds...')

		time.sleep(3)
		spx_api_handler()

		if debug_log:
			logging.info(f'✅ Success!')
		
		return

	# parse all launches one-by-one in the returned json-file
	try:
		launch_json = json.loads(API_RESPONSE.text)
	except Exception as error:
		#if debug_log:
			#logging.info(f'Error reading launch_json in spx_api_handler: {error}')

		return
		

	multi_parse(launch_json, len(launch_json))

	# update stats
	update_statistics({'API_requests':1, 'db_updates':1, 'data':len(API_RESPONSE.content)})

	return


def spx_info_str_gen(launch_name, run_count):
	'''
	Gets the name of a launch from launches.db and attempts to find the corresponding launch name
	from spx-launches.db with diffing, then generate the SpaceX launch specific information string.
	'''

	# manual matches for certain launches
	if 'DM2' in launch_name:
		launch_name = 'cctcap demo mission 2'

	# open the database connection and check if the launch exists in the database
	# if not, update
	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir, 'spx-launches.db')):
		create_spx_database()
		spx_api_handler()

	# open connection
	conn = sqlite3.connect(os.path.join(launch_dir, 'spx-launches.db'))
	c = conn.cursor()

	# unix time for NET
	today_unix = time.mktime(datetime.datetime.today().timetuple())

	# manual launch name matching for cases where automatic parsing fails
	# MAKE SURE THE KEYS ARE IN lower_case!!!!
	manual_name_matches = {
		'starlink-9': 'starlink-9 & blacksky global 5-6'
	}

	if launch_name.lower() in manual_name_matches.keys():
		launch_name = manual_name_matches[launch_name.lower()]

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
		if len(close_matches) == 0:
			alt_matches = difflib.get_close_matches(launch_name, launch_names)
			if len(alt_matches) != 0:
				close_matches = alt_matches

		# no matches, use the stripped keys
		launch_name_stripped = launch_name.replace('(','').replace(')','').lower()
		if len(close_matches) == 0:
			close_matches = difflib.get_close_matches(launch_name_stripped, stripped_keys)
			if len(close_matches) == 1:
				diff_match = close_matches[0]

			elif len(close_matches) == 0:
				# parse manually
				manual_matches = []
				for key in stripped_keys:
					if launch_name_stripped in key:
						manual_matches.append(key)

				if len(manual_matches) == 1:
					diff_match = manual_matches[0]
				else:
					if debug_log:
						logging.info(f'🛑 Error finding {launch_name_stripped} from keys (tried manually)!\nStripped_keys: {stripped_keys}')
						logging.info(f'🛑 Manual try: match_count={len(manual_matches)}, matches={manual_matches}')
					
					return None, None

			elif len(close_matches) > 1:
				manual_matches = []
				for key in stripped_keys:
					if launch_name_stripped in key:
						manual_matches.append(key)

				if len(manual_matches) == 1:
					diff_match = manual_matches[0]

				else:
					smallest_net, net_index = close_matches[0][2], 0
					for row, i in zip(close_matches, range(len(close_matches))):
						if row[2] < smallest_net:
							smallest_net, net_index = row[2], i
				
					diff_match = close_matches[net_index]

		# only one diff match; use it
		elif len(close_matches) == 1:
			diff_match = close_matches[0]
		
		# if we have more than one diffed match, sort launches by NET
		elif len(close_matches) > 1:
			smallest_net, net_index = close_matches[0][2], 0
			for row, i in zip(close_matches, range(len(close_matches))):
				if row[2] < smallest_net:
					smallest_net, net_index = row[2], i

			diff_match = close_matches[net_index]

		else:
			if run_count == 0:
				if debug_log:
					logging.info(f'🛑 Error in spx_info_str_gen: unable to find launches \
						with a NET >= {today_unix}. Updating and trying again...')

				spx_api_handler()
				spx_info_str_gen(launch_name, 1)
			else:
				if debug_log:
					logging.info(f'🛑 Error in spx_info_str_gen: unable to find launches \
						with a NET >= {today_unix}. Tried once before, not trying again.')

			return None, None

	elif len(query_return) == 1:
		db_match = query_return[0]
		diff_match = None

	else:
		if debug_log:
			logging.info(f'⚠️ Error in spx_info_str_gen(): got more than one launch. \
				query: {launch_name}, return: {query_return}')

		return None, None

	# if we got a diff_match, pull the launch manually from the spx database
	if diff_match != None:
		c.execute('''SELECT * FROM launches WHERE launch_name = ?''', (diff_match,))
		query_return = c.fetchall()

		if len(query_return) == 1:
			db_match = query_return[0]
		else:
			# no match; check launch names that have parantheses
			close_matches = difflib.get_close_matches(diff_match, launch_names)
			if len(close_matches) >= 1:
				diff_match = close_matches[0]
				c.execute('''SELECT * FROM launches WHERE launch_name = ?''', (diff_match,))
				query_return = c.fetchall()
				if len(query_return) == 1:
					db_match = query_return[0]
				else:
					if debug_log:
						logging.info(f'🛑 [spx_info_str_gen] Found {len(query_return)} matches from db... Exiting')
					return None, None
			else:
				if debug_log:
					logging.info(f'🛑 [spx_info_str_gen] Found {len(query_return)} matches from db... Exiting')
				return None, None

	# same found in multi_parse
	# use to extract info from db
	# row stored in db_match
	# flight_num 0, launch_name 1, NET 2, orbit 3, vehicle 4, core_serials 5
	# core_reuses 6, landing_intents 7, fairing_reused 8, fairing_rec_attempt 9, fairing_ship 10

	# get the orbit
	destination_orbit = db_match[3]

	if 'ISS' in destination_orbit:
		destination_orbit = None

	# booster information
	if db_match[4] == 'FH': # a Falcon Heavy launch
		reuses = db_match[6].split(',')
		try:
			int(reuses[0])
			if int(reuses[0]) > 0:
				center_reuses = f"`♻️x{int(reuses[0])}`"
			else:
				center_reuses = f'✨ `New`'
		except:
			center_reuses = f'`♻️x?`'

		try:
			int(reuses[1])
			if int(reuses[1]) > 0:
				booster1_reuses = f"`♻️x{int(reuses[1])}`"
			else:
				booster1_reuses = f'✨ `New`'
		except:
			booster1_reuses = f'`♻️x?`'

		try:
			int(reuses[2])
			if int(reuses[2]) > 0:
				booster2_reuses = f"`♻️x{int(reuses[2])}`"
			else:
				booster2_reuses = f'✨ `New`'
		except:
			booster2_reuses = f'`♻️x?`'

		# pull serials from db, construct serial strings
		serials = db_match[5].split(',')
		core_serial = f"{serials[0]} {center_reuses}"
		booster_serials = f"`{serials[1]}` {booster1_reuses} + `{serials[2]}` {booster2_reuses}"

		landing_intents = db_match[7].split(',')
		if landing_intents[0] != 'expend':
			center_recovery = f"{landing_intents[0]}"
		else:
			center_recovery = f"*No recovery* `godspeed,` `{serials[0]}` 💫"

		if landing_intents[1] != 'expend':
			booster1_recovery= f"{landing_intents[1]}"
		else:
			booster1_recovery = f"*No recovery* `godspeed,` `{serials[1]}` 💫"

		if landing_intents[2] != 'expend':
			booster2_recovery = f"{landing_intents[2]}"
		else:
			booster2_recovery = f"*No recovery* `godspeed,` `{serials[2]}` 💫"

	
	else: # single-stick
		core_serial = db_match[5]

		# recovery
		landing_intents = db_match[7]

		if 'OCISLY' in landing_intents:
			landing_intents = 'OCISLY (Atlantic Ocean)'
		elif 'JRTI' in landing_intents:
			landing_intents = 'JRTI (Atlantic Ocean)'
		elif 'ASLOG' in landing_intents:
			landing_intents = 'ASLOG (Pacific Ocean)'
		elif 'LZ-1' in landing_intents:
			landing_intents = 'LZ-1 (RTLS)'

		landing_intents = ' '.join("`{}`".format(word) for word in landing_intents.split(' '))

		if landing_intents != 'expend':
			if 'None' in landing_intents:
				recovery_str = '*Recovery* `Unknown`'
			else:
				recovery_str = f"*Recovery* {landing_intents}"
		else:
			recovery_str = f'*No recovery* `godspeed,` `{core_serial}` 💫'

	# construct the Falcon-specific information message
	if db_match[4] == 'FH':
		header = f'*Falcon Heavy configuration*\n*Center core* {core_serial}\n*Boosters* {booster_serials}'
		if landing_intents[1] == 'expend' and landing_intents[2] == 'expend':
			rec_str = f'*Recovery operations*\n*Center core* {center_recovery}'
			boost_str = f'*Boosters* No recovery – godspeed, `{serials[1]}` & `{serials[2]}'
			spx_info = f'{header}\n\n{rec_str}\n{boost_str}'
		
		else:
			rec_str = f'*Recovery operations*\n*Center core* {center_recovery}'
			boost_str = f'*Boosters* {booster1_recovery} `&` {booster2_recovery}'
			spx_info = f'{header}\n\n{rec_str}\n{boost_str}'

		if core_serial == 'Unknown':
			spx_info = f'ℹ️ No FH configuration information available yet'

	# not a FH? Then it's _probably_ a F9
	elif db_match[4] == 'F9':
		reuses = db_match[6]
		try:
			reuses = int(reuses)
			if reuses < 10:
				reuse_count = {
					0: 'first',
					1: 'second',
					2: 'third',
					3: 'fourth',
					4: 'fifth',
					5: 'sixth',
					6: 'seventh',
					7: 'eighth',
					8: 'ninth',
					9: 'tenth'
				}[reuses]

			else:
				try:
					if reuses in {11, 12, 13}:
						suffix = 'th'
					else:
						suffix = {1: 'st', 2: 'nd', 3: 'rd'}[int(str(reuses)[-1])]
				except:
					suffix = 'th'

				reuse_count = f'{reuses}{suffix}'
				
			reuses = '(' + reuse_count + ' flight ✨)' if reuses == 0 else '(' + reuse_count + ' flight ♻️)'
			reuses = ' '.join("`{}`".format(word) for word in reuses.split(' '))

		except:
			reuses = f'`♻️x?`'

		spx_info = f'*Booster information* 🚀\n*Core* `{core_serial}` {reuses}\n{recovery_str}'
		if core_serial == 'Unknown':
			spx_info = f'🚀 No booster information available yet'

	else:
		if debug_log:
			logging.info(f'🛑 Error in spx_info_str_gen: vehicle not found ({db_match[4]})')
		
		return None, None

	# check if there is fairing recovery & orbit information available
	if db_match[8] != '0' and db_match[8] != '1':
		try:
			if 'Dragon' in db_match[8]: # check if it's a Dragon flight
				dragon_info = db_match[8].split('/')
				dragon_serial = 'Unknown' if dragon_info[1] == 'None' else dragon_info[1]
				dragon_reused = '♻️ `Reused`' if dragon_info[2] == 'True' else ' '.join("`{}`".format(word) for word in '(first flight ✨)'.split(' '))
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

				# force text for DM-2
				if db_match[1] == 'cctcap demo mission 2':
					crew_str = '👨‍🚀👨‍🚀 Hurley & Behnken'

				cap_type = ' '.join("`{}`".format(word) for word in dragon_info[0].split(' '))
				fairing_info = f'*Dragon information* 🐉\n*Type* {cap_type}\n*Serial* `{dragon_serial}` {dragon_reused}\n*Crew* `{crew_str}`'
				spx_info = spx_info + '\n\n' + fairing_info

		except:
			pass

	''' UNCOMMENT TO ADD FAIRING INFORMATION BACK
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

				status_str = '♻️ `Reused`' if db_match[8] == 1 else '✨ `New`'
				fairing_info = f"*Fairing information*\n*Status* {status_str}\n*Recovery* `{rec_str}`"
				spx_info = spx_info + '\n\n' + fairing_info

		except Exception as e:
			if debug_log:
				logging.info(f'{e}')
			pass
	'''

	return spx_info, destination_orbit


# handles API requests from launch_update_check()
def get_launch_updates(launch_ID):
	# handle notification sending; done in a separate function so we can retry more easily and handle exceptions
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
		
		except telepot.exception.BotWasBlockedError:
			if debug_log:
				logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat)} – cleaning notify database...')

			clean_notify_database(chat)
			return True

		except telepot.exception.TelegramError as error:
			# Bad Request: chat not found
			if error.error_code == 400 and 'not found' in error.description:
				if debug_log:
					logging.info(f'⚠️ chat {anonymize_id(chat)} not found – cleaning notify database...')
					logging.info(f'Error: {error}')

				clean_notify_database(chat)
				return True

			elif error.error_code == 403:
				if 'user is deactivated' in error.description:
					if debug_log:
						logging.info(f'⚠️ user {anonymize_id(chat)} was deactivated – cleaning notify database...')
						logging.info(f'Error: {error}')

					clean_notify_database(chat)
					return True

				elif 'bot was kicked from the supergroup chat' in error.description:
					if debug_log:
						logging.info(f'⚠️ bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database...')
						logging.info(f'Error: {error}')

					clean_notify_database(chat)
					return True

				elif 'Forbidden: bot is not a member of the supergroup chat' in error.description:
					if debug_log:
						logging.info(f'⚠️ bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database...')
						logging.info(f'Error: {error}')

					clean_notify_database(chat)
					return True

				else:
					if debug_log:
						logging.info(f'⚠️ unhandled 403 telepot.exception.TelegramError in send_postpone_notification: {error}')

			# Rate-limited by Telegram (https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this)
			elif error.error_code == 429:
				if debug_log:
					logging.info(f'🚧 Rate-limited (429) - sleeping for 5 seconds and continuing. Error: {error}')

				time.sleep(5)
				return False

			# Some error code we don't know how to handle
			else:
				if debug_log:
					logging.info(f'⚠️ unhandled telepot.exception.TelegramError in send_notification: {error}')

				return False

		except Exception as caught_exception:
			return caught_exception


	def construct_params(PARAMS):
		param_url, i = '', 0
		if PARAMS is not None:
			for key, val in PARAMS.items():
				if i == 0:
					param_url += f'?{key}={val}'
				else:
					param_url += f'&{key}={val}'
				i += 1

		return param_url


	def multi_parse(json, launch_count):
		# check if db exists
		launch_dir = 'data/launch'
		if not os.path.isfile(os.path.join(launch_dir, 'launches.db')):
			create_launch_database()

		# open connection
		conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
		c = conn.cursor()

		# launch, id, keywords, countrycode, NET, T-, notify24hour, notify12hour, notify60min, notify5min, success, launched, hold
		for i in range(0, launch_count):
			# json of flight i
			launch_json = json['launches'][i]

			# extract stuff
			launch_name = launch_json['name'].split('|')[1]
			launch_id = launch_json['id']
			status = launch_json['status']

			# extract: lsp_name, vehicle, pad, info
			try:
				lsp_name = launch_json['lsp']['name']
				lsp_short = launch_json['lsp']['abbrev']
				vehicle = launch_json['rocket']['name']
				location_name = launch_json['location']['pads'][0]['name']
			except Exception as e:
				if debug_log:
					logging.info(f'⚠️ Error in multi_parse (3334): {e}')
				pass

			# NEW (2020): probability of launch + tbdtime/tbddate
			tbd_date = launch_json['tbddate']
			tbd_time = launch_json['tbdtime']
			launch_prob = launch_json['probability']
			
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
				try:
					pad_loc = location_name.split(', ')[1]
					pad = f'{pad}, {pad_loc}'
				except:
					pass
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
			success = {0:0, 1:0, 2:0, 3:1, 4:-1, 5:0, 6:0}[status]
			lsp = launch_json['lsp']['id']
			countrycode = launch_json['lsp']['countryCode']

			if success in {1, -1}:
				launched, holding = 1, -1

			elif success == 2:
				launched, holding = 0, 1

			elif success == 0:
				launched, holding = 0, 0

			today_unix = time.mktime(datetime.datetime.today().timetuple())
			if launch_json['netstamp'] != 0:
				# construct datetime from netstamp
				net_unix = launch_json['netstamp']
				net_stamp = datetime.datetime.fromtimestamp(net_unix)

				if today_unix <= net_unix:
					Tminus = abs(datetime.datetime.today() - net_stamp).seconds
				else:
					Tminus = 0

			else:
				# use the ISO date, which is effectively a NET date, while the above netstamp is the instantenious launch time
				# 20200122T165900Z
				if launch_json['isonet'] != 0:
					# convert to datetime object
					utc_dt = datetime.datetime.strptime(launch_json['isonet'], '%Y%m%dT%H%M%S%fZ')
					
					# convert UTC datetime to seconds since the Epoch
					net_unix = (utc_dt - datetime.datetime(1970, 1, 1)).total_seconds()
					net_stamp = datetime.datetime.fromtimestamp(net_unix)

					if today_unix <= net_unix:
						Tminus = abs(datetime.datetime.today() - net_stamp).seconds
					else:
						Tminus = 0
				else:
					net_unix, Tminus = -1, -1

			# update if launch ID found, insert if id not found
			# launch, id, keywords, lsp_name, vehicle, pad, info, countrycode, NET, Tminus
			# notify24h, notify12h, notify60min, notify5min, notifylaunch, success, launched, hold
			# NEW: tbd_date tbd_time launch_prob
			try: # launch not found, insert as new
				c.execute('''INSERT INTO launches
					(launch, id, keywords, lsp_name, lsp_short, vehicle, pad, info, countrycode, NET, Tminus,
					notify24h, notify12h, notify60min, notify5min, notifylaunch, success, launched, hold, vid, tbd_date, tbd_time, launch_prob)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, ?, ?, ?, ?, ?, ?, ?)''',
					(launch_name, launch_id, lsp, lsp_name, lsp_short, vehicle, pad, mission_text, countrycode, net_unix, Tminus, success, launched, holding, vid_url,
						tbd_date, tbd_time, launch_prob))
			
			except: # launch found
				# Launch is already found; check if the new NET matches the old NET.
				c.execute('SELECT NET, notify24h, notify12h, notify60min, notify5min, launched FROM launches WHERE id = ?',(launch_id,))
				old_info = c.fetchall()[0]
				old_NET = old_info[0]

				# new net doesn't match old NET; decide what to do with the notification flags, if they have been set
				new_NET = int(net_unix)

				if old_NET != new_NET:
					notification_statuses = {
					'24h': old_info[1],
					'12h': old_info[2],
					'1h': old_info[3],
					'5m': old_info[4]
					}

					net_diff = new_NET - old_NET

					#if net_diff < 0:
					#	if debug_log:
					#		if net_diff <- 1:
					#			logging.info(f'🕑 NET for launch {launch_id} moved left. Old NET: {old_NET}, new NET: {new_NET}, diff: {net_diff}')

					# at least 1 notification has already been sent
					if 1 in notification_statuses.values() and net_diff >= 5*60 and launched != 1:
						disabled_statuses = set()
						for key, status in notification_statuses.items():
							if key == '24h' and status == 1:
								if net_diff > 3600*24:
									notification_statuses['24h'] = 0
									disabled_statuses.add('24h')

							elif key == '12h' and status == 1:
								if net_diff >= 3600*12:
									notification_statuses['12h'] = 0
									disabled_statuses.add('12h')

							elif key == '1h' and status == 1:
								if net_diff >= 3600:
									notification_statuses['1h'] = 0
									disabled_statuses.add('1h')

							elif key == '5m' and status == 1:
								if net_diff >= 3600*(5/60):
									notification_statuses['5m'] = 0
									disabled_statuses.add('5m')

						# construct the eta string
						net_stamp = datetime.datetime.fromtimestamp(new_NET)
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

							day_suff = f'day' if t_d == 1 else f'days'
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

						# notify users with a message
						launch_unix = datetime.datetime.utcfromtimestamp(new_NET)
						if launch_unix.minute < 10:
							launch_time = f'{launch_unix.hour}:0{launch_unix.minute}'
						else:
							launch_time = f'{launch_unix.hour}:{launch_unix.minute}'

						# lift-off date
						ymd_split = f'{launch_unix.year}-{launch_unix.month}-{launch_unix.day}'.split('-')
						try:
							suffix = {1: 'st', 2: 'nd', 3: 'rd'}[int(str(ymd_split[2])[-1])]
						except:
							suffix = 'th'

						month_map = {
							1: 'January', 2: 'February', 3: 'March', 4: 'April',
							5: 'May', 6: 'June', 7: 'July', 8: 'August',
							9: 'Septemper', 10: 'October', 11: 'November', 12: 'December'}

						date_str = f'{month_map[int(ymd_split[1])]} {ymd_split[2]}{suffix}'

						# time-delta for postpone_str; net_diff is the time in seconds
						if net_diff > 3600*24:
							days = math.floor(net_diff/(3600*24))
							hours = math.floor((net_diff - days*3600*24)/3600)
							d_suff = 'day' if days == 1 else 'days'
							h_suff = 'hour' if hours == 1 else 'hours'

							if hours == 0:
								postpone_str = f'{days} {d_suff}'
							else:	
								postpone_str = f'{days} {d_suff} and {hours} {h_suff}'

						elif net_diff == 3600*24:
							postpone_str = '24 hours'

						elif net_diff < 3600*24:
							hours = math.floor(net_diff/3600)
							mins = math.floor((net_diff-hours*3600)/60)
							h_suff = 'hour' if hours == 1 else 'hours'
							m_suff = 'minute' if mins == 1 else 'minutes'
							
							if hours == 0:
								postpone_str = f'{mins} {m_suff}'
							else:
								if mins == 0:
									postpone_str = f'{hours} {h_suff}'
								else:
									postpone_str = f'{hours} {h_suff} and {mins} {m_suff}'

						# construct message
						msg_text = f'📢 *{launch_name}* has been postponed by {postpone_str}. '
						msg_text += f'*{lsp_name}* is now targeting lift-off on *{date_str}* at *{launch_time} UTC*.\n\n'
						msg_text += f'⏱ {eta_str} until next launch attempt.\n\n'
						msg_text = reconstruct_message_for_markdown(msg_text)
						msg_text += f'ℹ️ _You will be re\-notified of this launch\. For detailed info\, use \/next\@{BOT_USERNAME}\. '
						msg_text += 'To disable\, mute this launch with the button below\._'

						if lsp not in LSP_IDs.keys():
							notify_list = get_notify_list(lsp_name, launch_id, None)
						else:
							notify_list = get_notify_list(LSP_IDs[lsp][0], launch_id, None)

						active_chats, muted_chats = set(), set()
						for chat in notify_list:
							if load_mute_status(chat, launch_id, lsp) == 0:
								active_chats.add(chat)
							else:
								muted_chats.add(chat)

						# send the notifications
						global msg_identifiers
						msg_identifiers = []
						for chat in active_chats:
							ret = send_postpone_notification(chat, msg_text, launch_id, lsp)

							if ret != True and debug_log:
								logging.info(f'🛑 Error sending notification to chat={anonymize_id(chat)}! Exception: {ret}')

							tries = 1
							while ret != True:
								time.sleep(2)
								ret = send_postpone_notification(chat, msg_text, launch_id, lsp)
								tries += 1
								
								if ret == True:
									if debug_log:
										logging.info(f'✅ Notification sent successfully to chat={anonymize_id(chat)}! Took {tries} tries.')

								elif ret != True and tries > 5:
									if debug_log:
										logging.info(f'⚠️ Tried to send notification to {anonymize_id(chat)} {tries} times – passing.')
										
									ret = True

						if debug_log:
							logging.info(f'📢 Notified {len(active_chats)} chats about the postpone ({postpone_str})'
										 f' of launch {launch_id} by {lsp_name}')
							logging.info(f'🔕 Did NOT notify {len(muted_chats)} chats about the postpone due to mute'
										 f' status.')

						# update stats with sent notifications
						update_statistics({'notifications':len(active_chats)})

						# remove old notifs if possible
						remove_previous_notification(launch_id, lsp_short if len(lsp_name) > len('Virgin Orbit') else lsp_name)

						# convert identifiers to string, store
						msg_identifiers = ','.join(msg_identifiers)
						store_identifiers(launch_id, msg_identifiers)

						if debug_log:
							logging.info(f'Storing identifiers (send_postpone_notification)... strlen: {len(msg_identifiers)}')

						if debug_log:
							if len(disabled_statuses) > 0:
								disabled_notif_str = ', '.join(disabled_statuses)
								logging.info(f'🚩 {disabled_notif_str} flags set to 0 for {launch_id} | lsp={lsp_short}, lname={launch_name}, net_diff={net_diff}')

					c.execute('''UPDATE launches
						SET NET = ?, Tminus = ?, success = ?, launched = ?, hold = ?, info = ?, pad = ?,
						vid = ?, notify24h = ?, notify12h = ?, notify60min = ?, notify5min = ?, tbd_date = ?, tbd_time = ?, launch_prob = ?
						WHERE id = ?''', (
							net_unix, Tminus, success, launched, holding, mission_text, pad, vid_url,
							notification_statuses['24h'], notification_statuses['12h'], notification_statuses['1h'], notification_statuses['5m'],
							tbd_date, tbd_time, launch_prob, launch_id))

				else:
					c.execute('''UPDATE launches
						SET NET = ?, Tminus = ?, success = ?, launched = ?, hold = ?, info = ?, pad = ?, vid = ?, tbd_date = ?, tbd_time = ?, launch_prob = ?
						WHERE id = ?''', (net_unix, Tminus, success, launched, holding, mission_text, pad, vid_url, tbd_date, tbd_time, launch_prob, launch_id))

		conn.commit()
		conn.close()
		return
	
	# datetime, so we can only get launches starting today
	now = datetime.datetime.now()
	today_call = f'{now.year}-{now.month}-{now.day}'

	# what we're throwing at the API
	API_REQUEST = f'launch'
	PARAMS = {'mode': 'verbose', 'limit': 250, 'startdate': today_call}
	API_URL = 'https://launchlibrary.net'
	API_VERSION = '1.4'

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{construct_params(PARAMS)}' #&{fields}
	
	# perform the API call
	headers = {'user-agent': f'telegram-{BOT_USERNAME}/{VERSION}'}

	try:
		API_RESPONSE = requests.get(API_CALL, headers=headers)
	except Exception:
		if debug_log:
			logging.info(f'🛑 Error in LL API request')
			logging.info(f'⚠️ Trying again after 3 seconds...')

		time.sleep(3)
		get_launch_updates(None)

		if debug_log:
			logging.info(f'✅ Success!')
		
		return

	# pull json, dump for later inspection
	try:
		launch_json = json.loads(API_RESPONSE.text)
	except Exception as error:
		with open(os.path.join('data', 'json-parsing-error.txt'), 'w') as error_file:
			error_file.write(f'Error: {error}')
			error_file.write(API_RESPONSE.text)
			
		return
	
	#with open(os.path.join('data', 'launch', 'launch-json.json'), 'w') as json_data:
	#	json.dump(launch_json, json_data, indent=4)

	# if we got nothing in return from the API
	if 'launches' not in launch_json:
		if debug_log:
			logging.info(f'🛑 Error in LL API request (2)')
			logging.info(f'⚠️ Trying again after 3 seconds...')

		time.sleep(3)
		get_launch_updates(None)
		return

	if len(launch_json['launches']) == 0:
		if debug_log:
			if API_RESPONSE.status_code == 404:
				logging.info('⚠️ No launches found!')
			else:
				logging.info(f'⚠️ Failed request with status code {API_RESPONSE.status_code}')
		
		return

	# we got something, parse all of it
	if len(launch_json['launches']) >= 1:
		multi_parse(launch_json, len(launch_json['launches']))

	update_statistics({'API_requests':1, 'db_updates':1, 'data':len(API_RESPONSE.content)})


# MarkdownV2 requires some special handling, so parse the link here
def reconstruct_link_for_markdown(link):
	link_reconstruct, char_set = '', {')', '\\'}
	for char in link:
		if char in char_set:
			link_reconstruct += f'\\{char}'
		else:
			link_reconstruct += char

	return link_reconstruct


# Same as above, but for the message text
def reconstruct_message_for_markdown(message):
	message_reconstruct = ''
	char_set = {'[', ']', '(', ')', '~', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!'}
	for char in message:
		if char in char_set:
			message_reconstruct += f'\\{char}'
		else:
			message_reconstruct += char

	return message_reconstruct


def get_notify_list(lsp, launch_id, notif_class):
	# pull all with matching keyword (LSP ID), matching country code notification, or an "all" marker (and no exclusion for this ID/country)
	# Establish connection
	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir,'notifications.db')):
		create_notify_database()

	conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	c = conn.cursor()

	# pull all where keyword = LSP or "All"
	c.execute('SELECT * FROM notify WHERE keyword == ? OR keyword == ?',(lsp, 'All'))
	query_return = c.fetchall()

	# parse for possible mutes
	parsed_query_return = set()
	muted_chats = set()
	for row in query_return:
		append = True
		if row[2] != None:
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

	if notif_class is not None:
		# parse for chats which have possibly disabled this notification type
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


# load mute status for chat and launch
def load_mute_status(chat, launch_id, keywords):
	launch_dir = 'data/launch'
	conn = sqlite3.connect(os.path.join(launch_dir,'notifications.db'))
	c = conn.cursor()
	c.execute("SELECT muted_launches FROM notify WHERE chat = ? AND keyword = ?", (chat, keywords))
	query_return = c.fetchall()

	if len(query_return) == 0:
		mute_status = 0
	else:
		if query_return[0][0] == None:
			mute_status = 0
		elif str(launch_id) in query_return[0][0].split(','):
			mute_status = 1
		else:
			mute_status = 0

	return mute_status


# removes all notification settings for a chat from the notify database
def clean_notify_database(chat):
	conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
	c = conn.cursor()
	
	c.execute("DELETE FROM notify WHERE chat = ?", (chat,))
	conn.commit()
	conn.close()


def remove_previous_notification(launch_id, keyword):
	''' Before storing the new identifiers, remove the old notification if possible. '''
	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir, 'sent-notifications.db')):
		return

	conn = sqlite3.connect(os.path.join(launch_dir, 'sent-notifications.db'))
	c = conn.cursor()

	c.execute("SELECT msg_identifiers FROM identifiers WHERE id = ?", (launch_id,))
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
				if debug_log:
					logging.info(f'⚠️ Unable to delete previous notification. Unique ID: {message_identifier}.'
								 f'Got error: {error}')
		else:
			muted_count += 1
			if debug_log:
				logging.info(f'🔍 Not removing previous notification due to mute status for chat={anonymize_id(chat_id)}')

	if debug_log:
		logging.info(f'✅ Successfully removed {success_count} previously sent notifications! {muted_count} avoided due to mute status.')


# store identifiers for sent messages so they can be removed later
def store_identifiers(launch_id, msg_identifiers):
	launch_dir = 'data/launch'
	conn = sqlite3.connect(os.path.join(launch_dir, 'sent-notifications.db'))
	c = conn.cursor()

	try:
		c.execute("CREATE TABLE identifiers (id INTEGER, msg_identifiers TEXT, PRIMARY KEY (id))")
		c.execute("CREATE INDEX id_identifiers ON notify (id, identifiers)")
		if debug_log:
			logging.info(f'✨ sent-notifications.db created!')
	except sqlite3.OperationalError:
		pass

	try:
		c.execute('''INSERT INTO identifiers (id, msg_identifiers) VALUES (?, ?)''',(launch_id, msg_identifiers))
	except:
		c.execute('''UPDATE identifiers SET msg_identifiers = ? WHERE id = ?''', (msg_identifiers, launch_id))

	conn.commit()
	conn.close()


# gets a request to send a notification about launch X from launch_update_check()
def notification_handler(launch_row, notif_class, NET_slip):
	# handle notification sending; done in a separate function so we can retry more easily and handle exceptions
	def send_notification(chat, notification, launch_id, keywords, vid_link, notif_class):
		# send early notifications silently
		if notif_class not in {'1h', '5m'}:
			silent = True
		else:
			silent = False

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
						InlineKeyboardButton(text=mute_key, callback_data=f'mute/{keywords}/{launch_id}/{mute_press}')
				]])

			if silent == True:
				sent_msg = bot.sendMessage(chat, notification, parse_mode='MarkdownV2',
										   reply_markup=keyboard, disable_notification=True)
			else:
				sent_msg = bot.sendMessage(chat, notification, parse_mode='MarkdownV2',
										   reply_markup=keyboard, disable_notification=False)

			# sent message is stored in sent_msg; store in db so we can edit messages
			msg_identifier = f"{sent_msg['chat']['id']}:{sent_msg['message_id']}"
			msg_identifiers.append(f'{msg_identifier}')
			return True
		
		except telepot.exception.BotWasBlockedError:
			if debug_log:
				logging.info(f'⚠️ Bot was blocked by {anonymize_id(chat)} – cleaning notify database...')

			clean_notify_database(chat)
			return True

		except telepot.exception.TelegramError as error:
			# Bad Request: chat not found
			if error.error_code == 400 and 'not found' in error.description:
				if debug_log:
					logging.info(f'⚠️ chat {anonymize_id(chat)} not found – cleaning notify database...')
					logging.info(f'Error: {error}')

				clean_notify_database(chat)
				return True

			elif error.error_code == 403:
				if 'user is deactivated' in error.description:
					if debug_log:
						logging.info(f'⚠️ user {anonymize_id(chat)} was deactivated – cleaning notify database...')
						logging.info(f'Error: {error}')

					clean_notify_database(chat)
					return True

				elif 'bot was kicked from the supergroup chat' in error.description:
					if debug_log:
						logging.info(f'⚠️ bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database...')
						logging.info(f'Error: {error}')

					clean_notify_database(chat)
					return True

				elif 'Forbidden: bot is not a member of the supergroup chat' in error.description:
					if debug_log:
						logging.info(f'⚠️ bot was kicked from supergroup {anonymize_id(chat)} – cleaning notify database...')
						logging.info(f'Error: {error}')

					clean_notify_database(chat)
					return True

				else:
					if debug_log:
						logging.info(f'⚠️ unhandled 403 telepot.exception.TelegramError in send_notification: {error}')

			# Rate limited by Telegram (https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this)
			elif error.error_code == 429:
				if debug_log:
					logging.info(f'🚧 Rate-limited (429) - sleeping for 5 seconds and continuing. Error: {error}')

				time.sleep(5)
				return False

			# Something else
			else:
				if debug_log:
					logging.info(f'⚠️ unhandled telepot.exception.TelegramError in send_notification: {error}')

				return False

	launch_id = launch_row[1]
	keywords = int(launch_row[2])

	# check if LSP ID in keywords is in our custom list, so we can get the short name and the flag
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

	# parse the input so we can generate the message later
	launch_name = query_return[0][0]
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
		elif 'Starlink' in launch_name and '8' not in launch_name:
			info = "60 satellites for the Starlink satellite constellation, SpaceX's project for providing global, high-speed internet access."
		
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
	lsp_name = lsp_short if len(lsp) > len('Virgin Orbit') else lsp

	# if it's a SpaceX launch, pull get the info string
	if lsp_name == 'SpaceX':
		spx_info_str, spx_orbit_info = spx_info_str_gen(launch_name, 0)
		if spx_info_str != None:
			spx_str = True
		else:
			spx_str = False

	# do some string magic to reduce the space width of monospaced text in the telegram message
	lsp_str = ' '.join("`{}`".format(word) for word in lsp_name.split(' '))
	vehicle_name = ' '.join("`{}`".format(word) for word in vehicle.split(' '))
	pad_name = ' '.join("`{}`".format(word) for word in pad.split(' '))

	if 'DM2' in launch_name:
		launch_name = 'SpX-DM2'
		if time_format == 'minutes':
			info_text += ' Godspeed Behnken & Hurley.'

	# construct the "base" message
	message_header = f'🚀 *{launch_name}* is launching in *{t_minus} {time_format}*\n'
	message_header += f'*Launch provider* {lsp_str} {lsp_flag}\n*Vehicle* {vehicle_name}\n*Pad* {pad_name}'

	# if it's a SpaceX launch, append the orbit to the header
	if lsp_name == 'SpaceX':
		if spx_orbit_info != '' and spx_orbit_info != None:
			orbit_map = {
			'VLEO': 'Very low-Earth orbit', 'SO': 'Sub-orbital', 'LEO': 'Low-Earth orbit',
			'SSO': 'Sun-synchronous (SSO)', 'MEO': 'Medium-Earth orbit', 'GEO': 'Geostationary (direct)',
			'GTO': 'Geostationary (transfer)', 'ISS': 'International Space Station'
			}

			if spx_orbit_info in orbit_map.keys():
				spx_orbit_info = ' '.join("`{}`".format(word) for word in orbit_map[spx_orbit_info].split(' '))
			else:
				spx_orbit_info = f'`{spx_orbit_info}`'

			if 'ISS' not in spx_orbit_info:
				message_header += f'\n*Orbit* {spx_orbit_info}'


	# launch probability
	launch_prob = query_return[0][22]
	if launch_prob != -1 and launch_prob != None:
		if launch_prob >= 80:
			prob_str = f'☀️ *{launch_prob} %* probability of launch'
		elif launch_prob >= 60:
			prob_str = f'🌤 *{launch_prob} %* probability of launch'
		elif launch_prob >= 40:
			prob_str = f'🌥 *{launch_prob} %* probability of launch'
		elif launch_prob >= 20:
			prob_str = f'☁️ *{launch_prob} %* probability of launch'
		else:
			prob_str = f'🌪 *{launch_prob} %* probability of launch'

		prob_str += '\n'
		message_footer = prob_str
	else:
		message_footer = ''

	# add the footer
	message_footer += f'*🕓 The launch is scheduled* for LAUNCHTIMEHERE\n'
	message_footer += f'*🔕 To disable* use /notify@{BOT_USERNAME}'
	launch_str = message_header + '\n\n' + info_text + '\n\n' + message_footer

	# if NOT a SpaceX launch and we're close to launch, add the video URL
	if lsp_name != 'SpaceX':
		# a different kind of message for 60m and 5m messages, which contain the video url (if one is available)
		if notif_class in {'1h', '5m'} and launch_row[19] != '': # if we're close to launch, add the video URL
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
		if notif_class in {'24h', '12h'}:
			if spx_str:
				launch_str = message_header + '\n\n' + spx_info_str + '\n\n' + info_text + '\n\n' + message_footer

		# we're close to the launch, send the video URL
		elif notif_class in {'1h', '5m'} and launch_row[19] != '':
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
		if notif_class not in {'1h', '5m'}:
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

		if ret == True:
			success = True
		else:
			success = False
			if debug_log:
				logging.info(f'🛑 Error sending notification to chat={anonymize_id(chat)}! Exception: {ret}')


		tries = 1
		while ret != True:
			time.sleep(2)
			ret = send_notification(chat, chat_launch_str, launch_id, cmd_keyword, launch_row[19], notif_class)
			tries += 1
			
			if ret == True:
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
					logging.info(f'⚠️ Error getting number of chat members for chat={anonymize_id(chat)}. Error: {error}')

	# log end time
	end_time = timer()

	# update stats for sent notifications
	conn.close()
	update_statistics({'notifications':len(notify_list)})

	# set notification as sent; if 12 hour sent but 24 hour not sent, disable "higher" ones as well
	conn.close()
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
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
			logging.info(f'''⚠️ Error disabling notification in notification_handler().
			t_minus={t_minus}, launch_id={launch_id}. Notifications sent: {len(notify_list)}.
			Exception: {e}. Disabling all further notifications.''')

		c.execute(f'UPDATE launches SET notify24h = 1, notify12h = 1, notify60min = 1, notify5min = 1, notifylaunch = 1 WHERE id = ?', (launch_id,))

	conn.commit()
	conn.close()

	# remove previous notification
	remove_previous_notification(launch_id, cmd_keyword)

	# store msg_identifiers
	msg_identifiers = ','.join(msg_identifiers)
	store_identifiers(launch_id, msg_identifiers)


# updates our stats with the given input
def update_statistics(stats_update):
	# check if the db exists
	if not os.path.isfile(os.path.join('data', 'statistics.db')):
		create_stats_database()

	# Establish connection
	stats_conn = sqlite3.connect(os.path.join('data', 'statistics.db'))
	stats_cursor = stats_conn.cursor()

	# Update stats with the provided data
	for stat, val in stats_update.items():
		stats_cursor.execute(f"UPDATE stats SET {stat} = {stat} + {val}")

	# experimental sliding 24-hour window for command frequency
	if 'commands' in stats_update.keys():
		today = datetime.datetime.utcnow()
		date = f'{today.year}-{today.month}-{today.day}'
		slot = f'{today.hour}:0{today.minute}' if today.minute < 10 else f'{today.hour}:{today.minute}'

		stats_cursor.execute("CREATE TABLE IF NOT EXISTS command_frequency (day TEXT, time_slot TEXT, commands INTEGER, PRIMARY KEY (day, time_slot))")

		try:
			stats_cursor.execute("INSERT INTO command_frequency (day, time_slot, commands) VALUES (?, ?, ?)", (date, slot, stats_update['commands']))
		except:
			stats_cursor.execute("UPDATE command_frequency SET commands = commands + ? WHERE day = ? AND time_slot = ?", (stats_update['commands'], date, slot))

	stats_conn.commit()
	stats_conn.close()


# prints our stats
def statistics(chat, mode):
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
			commands = query_return[0][3]
			data = query_return[0][4]

		else:
			commands = notifs = api_reqs = data = 0

	except sqlite3.OperationalError:
		commands = notifs = api_reqs = data = 0

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
	load_avg_str = f'Load {load_avgs[0]:.2f} {load_avgs[1]:.2f} {load_avgs[2]:.2f}'
	
	if updays > 0:
		up_str = f'Uptime {updays} days, {uphours} h {upmins} min'
	else:
		up_str = f'Uptime {uphours} hours {upmins} min'

	# format data to MB or GB
	if data / 10**9 >= 1:
		data, data_size_class = data/10**9, 'GB'
	else:
		data, data_size_class = data/10**6, 'MB'

	# get database sizes
	try:
		db_sizes = os.path.getsize(os.path.join('data','launch','launches.db'))
		db_sizes += os.path.getsize(os.path.join('data','launch','spx-launches.db'))
		db_sizes += os.path.getsize(os.path.join('data','launch','notifications.db'))
		db_sizes += os.path.getsize(os.path.join('data','launch','sent-notifications.db'))
		db_sizes += os.path.getsize(os.path.join('data','bot-settings.json'))
		db_sizes += os.path.getsize(os.path.join('data','statistics.db'))
		db_sizes += os.path.getsize(os.path.join('data','log.log'))
	except:
		db_sizes = 0.00

	if db_sizes / 10**9 >= 1:
		db_sizes, db_size_class = db_sizes/10**9, 'GB'
	else:
		db_sizes, db_size_class = db_sizes/10**6, 'MB'

	# connect to notifications db
	conn = sqlite3.connect(os.path.join('data/launch', 'notifications.db'))
	c = conn.cursor()

	# pull all rows with enabled = 1
	c.execute('SELECT chat FROM notify WHERE enabled = 1')
	query_return = c.fetchall()

	reply_str = f'''
	📊 *LaunchBot global statistics*
	Notifications delivered: {notifs}
	Notification recipients: {len(set(row[0] for row in query_return))}
	Commands parsed: {commands}

	🛰 *Network statistics*
	Data transferred: {data:.2f} {data_size_class}
	API requests made: {api_reqs}

	💾 *Database statistics*
	Storage used: {db_sizes:.2f} {db_size_class}

	🎛 *Server information*
	{up_str}
	{load_avg_str}
	LaunchBot version *{VERSION}* 🚀
	'''

	if mode == 'refresh':
		return inspect.cleandoc(reply_str)

	# add a keyboard for refreshing
	keyboard = InlineKeyboardMarkup(
		inline_keyboard=[[InlineKeyboardButton(
			text='🔄 Refresh statistics', callback_data=f'stats/refresh')]])

	bot.sendMessage(chat, inspect.cleandoc(reply_str), reply_markup=keyboard, parse_mode='Markdown')


# creates the spx database
def create_spx_database():
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
			logging.info(f'🛑 Error in create_spx_database: {e}')

	conn.commit()
	conn.close()


# creates a new notifications database, if one doesn't exist
def create_notify_database():
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
		c.execute("CREATE TABLE notify (chat TEXT, keyword TEXT, muted_launches TEXT, enabled INTEGER, PRIMARY KEY (chat, keyword))")
		c.execute("CREATE INDEX enabledchats ON notify (chat, enabled)")
	except sqlite3.OperationalError:
		pass

	conn.commit()
	conn.close()


# creates a launch database
def create_launch_database():
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
			tbd_date INTEGER, tbd_time INTEGER, launch_prob INTEGER,
			PRIMARY KEY (id))''')
		
		c.execute("CREATE INDEX keywordtminus ON launches (id, NET)")
	
	except sqlite3.OperationalError as e:
		if debug_log:
			logging.info(f'Error in create_launch_database: {e}')
		pass

	conn.commit()
	conn.close()


# creates a statistics database
def create_stats_database():
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


# if running for the first time
def first_run():
	print("Looks like you're running LaunchBot for the first time!")
	print("Let's start off by creating some folders.")
	time.sleep(2)
	
	# create /data and /chats
	if not os.path.isdir('data'):
		os.mkdir('data')
		print("Folders created!\n")

	time.sleep(1)

	print('To function, LaunchBot needs a bot API key;')
	print('to get one, send a message to @botfather on Telegram.')

	# create a settings file for the bot; we'll store the API keys here
	if not os.path.isfile('data' + '/bot-settings.json'):
		if not os.path.isdir('data'):
			os.mkdir('data')

		update_token(['botToken'])
		time.sleep(2)
		print('\n')


# update bot token
def update_token(update_tokens):
	# create /data and /chats
	if not os.path.isdir('data'):
		first_run()

	if not os.path.isfile('data' + '/bot-settings.json'):
		with open('data/bot-settings.json', 'w') as json_data:
			setting_map = {} # empty .json file
	else:
		with open('data' + '/bot-settings.json', 'r') as json_data:
				setting_map = json.load(json_data) # use old .json

	if 'botToken' in update_tokens:
		token_input = str(input('Enter the bot token for LaunchBot: '))
		while ':' not in token_input:
			print('Please try again – bot-tokens look like "123456789:ABHMeJViB0RHL..."')
			token_input = str(input('Enter the bot token for launchbot: '))

		setting_map['botToken'] = token_input

	with open('data' + '/bot-settings.json', 'w') as json_data:
		json.dump(setting_map, json_data, indent=4)

	time.sleep(2)
	print('Token update successful!\n')


def sigterm_handler(signal, frame):
	if debug_log:
		logging.info(f'✅ Got SIGTERM. Runtime: {datetime.datetime.now() - STARTUP_TIME}.')

	sys.exit(0)


if __name__ == '__main__':
	# some global vars for use in other functions
	global TOKEN, OWNER, VERSION, BOT_ID, BOT_USERNAME, STARTUP_TIME
	global bot, debug_log

	# current version
	VERSION = '0.5.24'

	# default start mode, log start time
	start = debug_log = debug_mode = False
	STARTUP_TIME = datetime.datetime.now()

	# list of args the program accepts
	start_args = ('start', '-start')
	debug_args = ('log', '-log', 'debug', '-debug')
	bot_token_args = ('newbottoken', '-newbottoken')

	if len(sys.argv) == 1:
		print('Give at least one of the following arguments:')
		print('\tlaunchbot.py [-start, -newBotToken, -log]\n')
		print('E.g.: python3 launchbot.py -start')
		print('\t-start starts the bot')
		print('\t-newBotToken changes the bot API token')
		print('\t-log stores some logs\n')
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
					debug_log = True
					if not os.path.isdir('data'):
						first_run()
					
					log = 'data/log.log'

					# disable logging for urllib and requests because jesus fuck they make a lot of spam
					logging.getLogger('requests').setLevel(logging.CRITICAL)
					logging.getLogger('urllib3').setLevel(logging.CRITICAL)
					logging.getLogger('schedule').setLevel(logging.CRITICAL)
					logging.getLogger('chardet.charsetprober').setLevel(logging.CRITICAL)
					logging.getLogger('telepot.exception.TelegramError').setLevel(logging.CRITICAL)

					# start log
					logging.basicConfig(filename=log,level=logging.DEBUG,format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')

				if arg in ('debug', '-debug'):
					debug_mode = True

		if len(update_tokens) != 0:
			update_token(update_tokens)

		if start is False:
			sys.exit('No start command given – exiting. To start the bot, include -start in startup options.')

	# if data folder isn't found, we haven't run before (or someone pressed the wrong button)
	if not os.path.isdir('data'):
		first_run()

	try:
		bot_settings_path = os.path.join('data','bot-settings.json')
		with open(bot_settings_path, 'r') as json_data:
			try:
				setting_map = json.load(json_data)
			except:
				os.remove(os.path.join('data','bot-settings.json'))
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
			'Mitsubishi Heavy Industries']
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
		print(f"| Don't close this window or set the computer to sleep. Quit: ctrl + c.")
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
		cursor.hide()
		
		try:
			while True:
				schedule.run_pending()
				for char in ('|', '/', '—', '\\'):
					sys.stdout.write('%s\r' % char)
					sys.stdout.flush()
					time.sleep(1)

		except KeyboardInterrupt:
			cursor.show()
			sys.exit(f'Program ending... Runtime: {datetime.datetime.now() - STARTUP_TIME}.')

	else:
		while True:
			schedule.run_pending()
			time.sleep(3)
