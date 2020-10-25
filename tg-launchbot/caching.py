import pymemcache
import redis

'''
Basic caching logic

Cache...
	- /next calls: everything can be cached, but T- must be computed again for each instance: fast, easy
	- /schedule call, for both types

1. Generate cached strings on API update
2. Cache into a redis or memcached DB
3. Become speed
'''
