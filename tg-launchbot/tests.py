import unittest
import logging
import sqlite3
import random
import os

from utils import time_delta_to_legible_eta
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

		# three SpX launches
		test_id_tuple = (
			'47c91a03-2e98-42c9-8751-d8fb36c89c99',
			'0ede12be-ac6d-4571-9d0c-b2a85b5cf280',
			'56623c2d-7174-489c-b0ed-bf6f039b2412')

		test_id = test_id_tuple[0]

		# select all IDs in db
		cursor.execute('SELECT unique_id from launches')
		query_return = cursor.fetchall()

		# run for all launches
		for row in query_return:
			cursor.execute('SELECT * FROM launches WHERE unique_id = ?', (row[0],))
			launch = [dict(row) for row in cursor.fetchall()][0]

			msg = create_notification_message(
				launch=launch, notif_class='notify_60min', bot_username='rocketrybot')

			# print(msg + '\n\n ------------------------')


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


if __name__ == '__main__':
	# init log
	logging.basicConfig(level=logging.DEBUG,format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')

	unittest.main()
