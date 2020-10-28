import unittest
import logging
import sqlite3
import random
import os

from utils import time_delta_to_legible_eta, suffixed_readable_int
from api import construct_params
from notifications import create_notification_message, get_notify_list, toggle_launch_mute
from db import create_chats_db
from timezone import load_bulk_tz_offset
from config import load_config

class TestNotificationUtils(unittest.TestCase):
	'''
	Run tests for the API calls and associated functions.
	'''

	def test_notification_message_creation(self):
		'''
		Test create_notification_message

		launch: dict, notif_class: str, bot_username: str
		'''
		print('Testing notification message creation...')

		# db path
		db_path = 'launchbot'

		# Establish connection
		conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
		conn.row_factory = sqlite3.Row
		cursor = conn.cursor()

		# select all IDs in db
		cursor.execute('SELECT unique_id from launches WHERE lsp_name = ?', ('SpaceX',))
		query_return = cursor.fetchall()

		# run for all launches
		for row in query_return:
			cursor.execute('SELECT * FROM launches WHERE unique_id = ?', (row[0],))
			launch = [dict(row) for row in cursor.fetchall()][0]

			msg = create_notification_message(launch=launch, notif_class='notify_60min')
			print(msg + '\n------------------------\n\n')


	def test_get_notify_list(self):
		'''
		Test get_notify_list
		'''
		db_path = 'launchbot'
		lsp = 'Arianespace'
		launch_id = '56623c2d-7174-489c-b0ed-bf6f039b2412'
		notif_class = 'notify_24h'

		ret = get_notify_list(db_path, lsp, launch_id, notif_class)
		print(ret)

		'''
		A better test case
		1. generate an entry in chats db with a random chat ID
		2. add one random launch (provider) in enabled, some in disabled
		3. test pull for random launch ID
		# fire up connection to a testing db
		test_db = 'launchbot-tests'
		conn = sqlite3.connect(os.path.join(test_db, 'launchbot-data.db'))
		cursor = conn.cursor()

		# create a testing database
		create_chats_db(db_path=test_db, cursor=cursor)
		conn.commit()

		# generate fake chat IDs
		for i in range(0, 20):
			rand_id = random.randint(0, 10000)
			cursor.execute()
		'''


	def test_toggle_launch_mute(self):
		db_path = 'launchbot'
		chat = load_config(db_path)['owner']

		launch_id = 'c5a9ba01-d03f-4fd7-940a-8a10d535809a'
		toggle_launch_mute(db_path=db_path, chat=chat, launch_id=launch_id, toggle=1)
		#toggle_launch_mute(db_path=db_path, chat=chat, launch_id=launch_id, toggle=0)


class TestUtils(unittest.TestCase):
	def test_construct_params(self):
		'''
		Test construct_params
		'''
		print('Testing construct_params...')
		test_keyvals = {'one': 1, 'two': 2, 'three': 3}
		expected_params = '?one=1&two=2&three=3'

		self.assertEqual(construct_params(test_keyvals), expected_params)


	def test_pretty_eta(self):
			'''
			Test time_delta_to_legible_eta
			'''
			# test small deltas
			for i in range(0, 100):
				rand_delta = random.randint(0, 3600)
				time_delta_to_legible_eta(rand_delta, True)

			# test large deltas
			for i in range(0, 100):
				rand_delta = random.randint(0, 3600 * 24 * 2)
				time_delta_to_legible_eta(rand_delta, True)


			# test with 0 seconds
			self.assertEqual(time_delta_to_legible_eta(0, False), 'just now')


	def test_time_delta_to_legible_eta(self):
		'''
		Test time_delta_to_legible_eta with random times
		'''

		# without full accuracy, large values
		for i in range(10):
			print(
				time_delta_to_legible_eta(
					time_delta=random.uniform(0, 3600*24*30), full_accuracy=False))

		# without full accuracy, small values
		for i in range(10):
			print(
				time_delta_to_legible_eta(
					time_delta=random.uniform(0, 3600*24), full_accuracy=False))


		# with full accuracy, large values
		for i in range(10):
			print(
				time_delta_to_legible_eta(
					time_delta=random.uniform(0, 3600*24*30), full_accuracy=True))

		# with full accuracy, small values
		for i in range(10):
			print(
				time_delta_to_legible_eta(
					time_delta=random.uniform(0, 3600*24), full_accuracy=True))


		def test_suffixed_readable_int(self):
			for i in range(1000):
				rand_int = random.randint(0, 200)
				print(f'{rand_int} -> {suffixed_readable_int(rand_int)}')



class TestTimeZoneUtils(unittest.TestCase):
	def test_load_bulk_tz_offset(self):
		data_dir = 'launchbot'
		config = load_config(data_dir)

		chat_id_set = {config['owner']}
		ret = load_bulk_tz_offset(data_dir=data_dir, chat_id_set=chat_id_set)
		print(ret)


if __name__ == '__main__':
	# init log
	logging.basicConfig(level=logging.DEBUG,format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')

	unittest.main()
