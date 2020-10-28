'''
Currently only includes the spammer class, but will later incorporate
some other anti-spam related features.
'''

from timeit import default_timer as timer


class Spammer:
	'''
	A tiny class for defining a spammer
	'''
	def __init__(self, uid):
		'''
		Initialises the spammer object
		'''
		self.id = uid
		self.offenses = 1
		self.spam_times = (timer())

	def get_offenses(self):
		'''
		Returns offenses for a spammer
		'''
		return self.offenses

	def add_offense(self):
		'''
		Adds an offense for the user
		'''
		self.offenses += 1
		self.spam_times = self.spam_times + (timer())

	def clear_offenses(self):
		'''
		Clears all offenses for a user
		'''
		self.offenses = 0
		self.spam_times = ()

	def offense_delta(self):
		'''
		Returns the time delta between two offenses
		'''
		return
