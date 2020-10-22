import datetime

def reconstruct_link_for_markdown(link: str) -> str:
	''' Summary
	Telegram's MarkdownV2 requires some special handling, so
	parse the link here into a compatible format.
	'''
	link_reconstruct, char_set = '', {')', '\\'}
	for char in link:
		if char in char_set:
			link_reconstruct += f'\\{char}'
		else:
			link_reconstruct += char

	return link_reconstruct


def reconstruct_message_for_markdown(message: str) -> str:
	''' Summary
	Performs effectively the same functions as reconstruct_link, but
	is intended to be used for message body text.
	'''
	message_reconstruct = ''
	char_set = {'[', ']', '(', ')', '~', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!'}
	for char in message:
		if char in char_set:
			message_reconstruct += f'\\{char}'
		else:
			message_reconstruct += char

	return message_reconstruct


def shorten_monospaced_text(text: str) -> str:
	''' Summary
	Telegram has extremely wide spaces for the monospaced font. This function
	helps eliminate those wide spaces by replacing them with "regular" spaces.
	'''
	return ' '.join("`{}`".format(word) for word in text.split(' '))


def map_country_code_to_flag(country_code: str) -> str:
	''' Summary
	Maps a country code to a corresponding emoji flag: truly modern.
	'''
	flag_map = {
		'FRA': '🇪🇺', 'FR': '🇪🇺', 'USA': '🇺🇸', 'EU': '🇪🇺',
		'RUS': '🇷🇺', 'CHN': '🇨🇳', 'IND': '🇮🇳', 'JPN': '🇯🇵',
		'IRN': '🇮🇷', 'NZL': '🇳🇿', 'GUF': '🇬🇫', 'UNK': '🏳'
	}

	return flag_map[country_code] if country_code in flag_map.keys() else '🏳'


def timestamp_to_unix(timestamp: str) -> int:
	''' Summary
	Parses a LL2 timestamp from its format into a unix timestamp,
	i.e. seconds since the unix epoch. 
	'''
	# convert to a datetime object from the custom format, ex. 2020-10-18T12:25:00Z
	utc_dt = datetime.datetime.strptime(timestamp, '%Y-%m-%dT%H:%M:%S%fZ')

	# convert UTC datetime to integer seconds since the unix epoch, return
	return int((utc_dt - datetime.datetime(1970, 1, 1)).total_seconds())


def time_delta_to_legible_eta(time_delta: int) -> str:
	''' Summary
	This is a tiny helper function, used to convert integer time deltas
	(i.e. second deltas) to a legible ETA, where the largest unit of time
	is measured in days.
	'''
	# convert time delta to a semi-redable format: {days, hh:mm:ss}
	eta_str = "{}".format(str(datetime.timedelta(seconds=time_delta)))

	# parse into a "pretty" string. If ',' in string, it's more than 24 hours.
	if ',' in eta_str:
		day_str = eta_str.split(',')[0]
		hours = int(eta_str.split(',')[1].split(':')[0])

		pretty_eta = f'{day_str}{f", {hours} hour" if hours > 0 else ""}'
		if hours > 1:
			pretty_eta += 's'
	else:
		hhmmss_split = eta_str.split(':')
		hours, mins, secs = int(hhmmss_split[0]), int(hhmmss_split[1]), int(hhmmss_split[2])
		if hours > 0:
			pretty_eta = f'{hours} hour{"s" if hours > 1 else ""}'
			pretty_eta += f', {mins} minute{"s" if mins != 1 else ""}'
		else:
			pretty_eta = f'{mins} minute{"s" if mins > 1 else ""}'
			pretty_eta += f', {secs} second{"s" if secs != 1 else ""}'

	return pretty_eta
