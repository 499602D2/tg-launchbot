import unittest

from api import construct_params

class TestAPIFunctions(unittest.TestCase):
	'''
	Run tests for the API calls and associated functions.
	'''
	def test_construct_params(self):
		'''
		Test construct_params
		'''
		test_keyvals = {'one': 1, 'two': 2, 'three': 3}
		expected_params = '?one=1&two=2&three=3'
		
		self.assertEqual(construct_params(test_keyvals), expected_params)

if __name__ == '__main__':
	unittest.main()
