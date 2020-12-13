import os
import time
import logging


import ujson as json


def first_run(data_dir: str):
	'''
	Show a quick introduction message during first run.

	Keyword arguments:
		data_dir (str): configuration file folder to create

	Returns:
		None
	'''
	print("Looks like you're running LaunchBot for the first time!")
	print("Let's start off by creating some folders.")
	time.sleep(2)

	# create directories
	if not os.path.isdir(data_dir):
		os.makedirs(data_dir)
		print("Folders created!\n")

	# fast things are scary, slow down
	time.sleep(1)


def store_config(config_json: dict, data_dir: str):
	'''
	Stores the configuration specified in config_json onto disk.

	Keyword arguments:
		config_json (dict): new config dictionary

	Returns:
		None
	'''
	with open(os.path.join(data_dir, 'bot-config.json'), 'w') as config_file:
		json.dump(config_json, config_file, indent=4)

	print('Updated config dumped!')


def create_config(data_dir: str):
	'''
	Runs the config file setup if file doesn't exist or is corrupted/missing data.

	Keyword arguments:
		data_dir (str): location where config file is created

	Returns:
		None
	'''
	if not os.path.isdir(data_dir):
		first_run(data_dir)

	with open(os.path.join(data_dir, 'bot-config.json'), 'w') as config_file:
		print('\nTo function, LaunchBot needs a bot API key;')
		print('to get one, send a message to @botfather on Telegram.')

		bot_token = input('Enter bot token: ')
		print()

		config = {
			'bot_token': bot_token,
			'owner': 0,
			'redis': {
				'host': 'localhost',
				'port': 6379,
				'db_num': 0
			},
			'local_api_server': {
				'enabled': False,
				'logged_out': False,
				'address': None
			}
		}

		json.dump(config, config_file, indent=4)


def load_config(data_dir: str) -> dict:
	'''
	Load variables from configuration file.

	Keyword arguments:
		data_dir (str): location of config file

	Returns:
		config (dict): configuration in json/dict format
	'''
	# if config file doesn't exist, create it
	if not os.path.isfile(os.path.join(data_dir, 'bot-config.json')):
		print('Config file not found: performing setup.\n')
		create_config(data_dir)

	with open(os.path.join(data_dir, 'bot-config.json'), 'r') as config_file:
		try:
			return json.load(config_file)
		except:
			print('JSONDecodeError: error loading configuration file. Running config setup...')

			create_config(data_dir)
			return load_config(data_dir)

	with open(os.path.join(data_dir, 'bot-config.json'), 'r') as config_file:
		return json.load(config_file)


def repair_config(data_dir: str) -> dict:
	'''
	Repairs a config file
	'''
	# check if all the server config keys exist
	config_keys = { 'bot_token', 'owner', 'redis', 'local_api_server' }

	# full config
	full_config = {
		'bot_token': 0,
		'owner': 0,
		'redis': {
			'host': 'localhost',
			'port': 6379,
			'db_num': 0
		},
		'local_api_server': {
			'enabled': False,
			'logged_out': False,
			'address': None
		}
	}

	# load config
	config = load_config(data_dir=data_dir)

	# we're just taking the difference of the two key sets here
	set_diff = config_keys.difference(set(config.keys()))
	if set_diff == set():
		return

	logging.info(f'Server configuration is missing keys {set_diff}: repairing...')
	for key, val in full_config.items():
		if key not in config:
			config[key] = val
			logging.info(f'\tAdded missing key-val pair: {key}')

	return config
