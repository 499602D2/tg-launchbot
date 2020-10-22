import unittest
import logging
import sqlite3
import os

from api import construct_params
from notifications import create_notification_message

class TestLaunchBotFunctions(unittest.TestCase):
	'''
	Run tests for the API calls and associated functions.
	'''
	def test_construct_params(self):
		'''
		Test construct_params
		'''
		print('Testing construct_params...')
		test_keyvals = {'one': 1, 'two': 2, 'three': 3}
		expected_params = '?one=1&two=2&three=3'
		
		self.assertEqual(construct_params(test_keyvals), expected_params)

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

		test_id_tuple = (
			'47c91a03-2e98-42c9-8751-d8fb36c89c99',
			'0ede12be-ac6d-4571-9d0c-b2a85b5cf280',
			'56623c2d-7174-489c-b0ed-bf6f039b2412')

		test_id = test_id_tuple[0]

		cursor.execute('SELECT * FROM launches WHERE unique_id = ?', (test_id,))
		launch = [dict(row) for row in cursor.fetchall()][0]

		msg = create_notification_message(
			launch=launch, notif_class='notify_24h', bot_username='rocketrybot')

		print(msg)


if __name__ == '__main__':
	# init log
	logging.basicConfig(level=logging.DEBUG,format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')

	unittest.main()
