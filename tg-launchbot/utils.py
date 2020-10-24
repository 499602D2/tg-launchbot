'''
utils.py includes some common, tiny helper functions used throughout the program.
Including these separately in each file would be ugly, so they're here in their
own little place.

Classes:
	None

Functions:
	anonymize_id(chat: str) -> str
	reconstruct_link_for_markdown(link: str) -> str
	reconstruct_message_for_markdown(message: str) -> str
	short_monospaced_text(text: str) -> str
	map_country_code_to_flag(country_code: str) -> str
	timestamp_to_unix(timestamp: str) -> int
	time_delta_to_legible_eta(time_delta: int) -> str

Misc variables:
	None
'''


import datetime

from hashlib import sha1


def anonymize_id(chat: str) -> str:
	'''
	For pseudo-anonymizing chat IDs, a truncated, unsalted SHA-1 hash
	is returned for use in logging.

	Keyword arguments:
		chat (str): chat ID to anonymize

	Returns:
		chat (str): the anonymized chat ID
	'''
	return sha1(str(chat).encode('utf-8')).hexdigest()[0:6]


def reconstruct_link_for_markdown(link: str) -> str:
	'''
	Telegram's MarkdownV2 requires some special handling, so
	parse the link here into a compatible format.

	Keyword arguments:
		link (str): link to reconstruct for Markdown

	Returns:
		link_reconstruct (str): the reconstructed link
	'''
	link_reconstruct, char_set = '', {')', '\\'}
	for char in link:
		if char in char_set:
			link_reconstruct += f'\\{char}'
		else:
			link_reconstruct += char

	return link_reconstruct


def reconstruct_message_for_markdown(message: str) -> str:
	'''
	Performs effectively the same functions as reconstruct_link, but
	is intended to be used for message body text.

	Keyword arguments:
		message (str): message to escape for Markdown

	Returns:
		message_reconstruct (str): the escaped message
	'''
	message_reconstruct = ''
	char_set = {'[', ']', '(', ')', '~', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!'}
	for char in message:
		if char in char_set:
			message_reconstruct += f'\\{char}'
		else:
			message_reconstruct += char

	return message_reconstruct


def short_monospaced_text(text: str) -> str:
	'''
	Telegram has extremely wide spaces for the monospaced font. This function
	helps eliminate those wide spaces by replacing them with "regular" spaces.

	Keyword arguments:
		text (str): text to monospace in a shortened format

	Returns:
		text (str): monospaced text
	'''
	return ' '.join("`{}`".format(word) for word in text.split(' '))


def map_country_code_to_flag(country_code: str) -> str:
	'''
	Maps a country code to a corresponding emoji flag: truly modern.
	The functions returns a blank, white flag if the country code
	doesn't exist in the flag_map dictionary.

	Keyword arguments:
		country_code (str): country code to return the flag for

	Returns:
		emoji_flag (str): the flag for the country_code
	'''
	flag_map = {
		'FRA': '🇪🇺', 'FR': '🇪🇺', 'USA': '🇺🇸', 'EU': '🇪🇺',
		'RUS': '🇷🇺', 'CHN': '🇨🇳', 'IND': '🇮🇳', 'JPN': '🇯🇵',
		'IRN': '🇮🇷', 'NZL': '🇳🇿', 'GUF': '🇬🇫', 'UNK': '🏳'
	}

	return flag_map[country_code] if country_code in flag_map.keys() else '🏳'


def timestamp_to_unix(timestamp: str) -> int:
	'''
	Parses a LL2 timestamp from its format into a unix timestamp,
	i.e. seconds since the unix epoch. 

	Keyword arguments:
		timestamp (str): timestamp in the format used by the LL2 API

	Returns:
		unix_timestamp (int): unix timestamp corresponding to the above timestamp
	'''
	# convert to a datetime object from the custom format, ex. 2020-10-18T12:25:00Z
	utc_dt = datetime.datetime.strptime(timestamp, '%Y-%m-%dT%H:%M:%S%fZ')

	# convert UTC datetime to integer seconds since the unix epoch, return
	return int((utc_dt - datetime.datetime(1970, 1, 1)).total_seconds())


def time_delta_to_legible_eta(time_delta: int) -> str:
	'''
	This is a tiny helper function, used to convert integer time deltas
	(i.e. second deltas) to a legible ETA, where the largest unit of time
	is measured in days.

	Keyword arguments:
		time_delta (int): time delta in seconds to convert

	Returns:
		pretty_eta (str): the prettily formatted, readable ETA string
	'''
	# convert time delta to a semi-redable format: {days, hh:mm:ss}
	eta_str = "{}".format(str(datetime.timedelta(seconds=time_delta)))

	# parse into a "pretty" string. If ',' in string, it's more than 24 hours.
	if ',' in eta_str:
		day_str = eta_str.split(',')[0]
		hours = int(eta_str.split(',')[1].split(':')[0])
		mins = int(eta_str.split(',')[1].split(':')[1])

		if hours > 0:
			pretty_eta = f'{day_str}{f", {hours} hour" if hours > 0 else ""}'
			if hours > 1:
				pretty_eta += 's'
		else:
			pretty_eta = f'{day_str}{f", {mins} minute" if mins > 0 else ""}'
			if mins > 1:
				pretty_eta += 's'
	else:
		# split eta_string into hours, minutes, and seconds -> convert to integers
		hhmmss_split = eta_str.split(':')
		hours, mins, secs = int(hhmmss_split[0]), int(hhmmss_split[1]), int(hhmmss_split[2])

		if hours > 0:
			pretty_eta = f'{hours} hour{"s" if hours > 1 else ""}'
			pretty_eta += f', {mins} minute{"s" if mins != 1 else ""}'
		else:
			if mins > 0:
				pretty_eta = f'{mins} minute{"s" if mins > 1 else ""}'
				pretty_eta += f', {secs} second{"s" if secs != 1 else ""}'
			else:
				if secs > 0:
					pretty_eta = f'{secs} second{"s" if secs != 1 else ""}'
				else:
					pretta_eta = 'just now'

	return pretty_eta
