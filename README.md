# LaunchBot – a rocket launch information and notifications bot for Telegram 🚀
LaunchBot keeps you up to date with what's going up, around the clock, since 2019. Over 350,000 notifications delivered to thousands of chats and groups!

Reachable as [@rocketrybot](https://t.me/rocketrybot) on Telegram.

![preview](notification.png)

## Features
LaunchBot uses the LaunchLibrary2 API to fetch launch information on scheduled intervals. The bot provides multiple forms of information: launch notifications, information about upcoming flights, and a simple flight schedule displaying upcoming flights at a glance.

Other features include...
- user-configurable notifications on a per-provider and per-country basis
- user-choosable notification times from 4 different options
- mutable launches
- notifications of launches being postponed
- direct links to launch webcasts
- automatically cleared notification messages
- simple information refresh with Telegram's message buttons

## Basic instructions
Clone the repository and install the Python3 dependencies with pip, using the requirements.txt file found in the repository: `python3 -m pip install -R requirements.txt`.

LaunchBot also requires a running redis server instance. Redis is used to reduce disk accesses: redis is an in-memory (caching) database, compared to the sqlite database sitting on the disk. This should help the longevity of cheap flash storage media, like SD-cards, while also improving latency.

To install redis, follow the instructions at [redis.io](https://redis.io/download). On Linux-based systems, `redis-server` can be installed through most package managers. On macOS, `redis-server` can be found in the Homebrew package repository.

LaunchBot expects a running redis instance to be found at `127.0.0.1:6379`, which is also the default location. You should be fine running `redis-server` with the default configuration. However you might want to add the `--daemonize` flag to run the instance in the background: `redis-server --daemonize`.

After the dependencies are installed and you have a redis-server instance running, you can run the bot with `python3 launchbot.py -start`. Once you have set up the bot, you can run the bot in the background with `nohup` – in this case, it's advisable to add the `-debug` flag to prevent the flooding of the `nohup.out` file: `nohup python3 launchbot.py -start -debug &`.

## Data
The bot creates the following supporting files under `../data`:

SQLite: `launchbot-data.db`: houses all data the bot needs to operate, including launch information, statistics, chat preferences, and notification lists.

Redis: used to cache various responses and statistics.

json: `bot-config.json`: used to configure the bot by setting the Telegram bot API key, alongside with some other configuration information, such as the redis server and telegram bot API server settings.

You can specify your personal account's Telegram user ID in `bot-config.json` in the form `owner: "your_user_id"`. This disables the logging of commands sent by you.

## Privacy

The bot stores every interaction (i.e. command) sent to it if logging is enabled, alongside with a truncated, unsalted SHA-1 hash of the user ID. No text messages are stored, aside from text sent as a reply to a feedback request message. The bot's privacy settings forbid the bot from reading regular text, as in text messages which have not tagged the bot (@botusername) or are not a reply to a message sent by the bot (these are not logged, unless they're a reply to a feedback message.)

Only information stored by the bot is the chat ID, which can also be the user ID of a user in the case of a private chat. This is the only user information stored, which is used to deliver notifications. If no notifications are enabled, no information is stored, aside from an in-memory chat ID for managing spam, which is automatically cleared when the program quits and is thus never stored.

Please note, that the above only applies on a per-bot basis. The creator of the bot chooses whether to configure the bot to be able to read all text messages, not just ones directed at the bot. Telegram bots are, by nature, extremely privacy invasive: don't add unknown bots to group chats, unless it's hosted by you or someone you trust.

## Roadmap and historical changelog

<details>
  <summary>View changelog/roadmap</summary>
  	
	## 1.0 / first implementation (November 2019)

	- ✅ implemented uncached API requests
	
	- ✅ implemented the request of next launch via a direct API call

	
	## 1.2 / basic features (December 2019)

	- ✅ implement /next using DB calls
	
	- ✅ implement support for SpaceX core information

	
	## 1.3 / user-facing features (January 2020)
	
	- ✅ add "next" and "previous" button(s) to /next command
	
	- ✅ add a mute button to notifications
	
	- ✅ update /notify to be more user friendly
	
	- ✅ implement /feedback
	
	- ✅ improve notification handling with the hold flag -> moving NETs and info text regarding them
	
	- ✅ change launch database index from tminus to net

	
	## 1.4 / basic improvements (February 2020 ->)

	- ✅ Notify users of a launch being postponed if a notification has already been sent
	
	- ✅ disable logging of text messages; how to do feedback? (log feedback messages in a global array?)
	
	- ✅ add tbd-field to launches, so schedule can only show certain launch dates (filter certain and uncertain with a button)
	
	- ✅ add location (i.e. state/country) below pad information (Florida, USA etc.)

	
	## 1.5 / user-facing features
	
	- ✅ delete older notification messages when a new one is sent
	
	- add a "more info"/"less info" button to /next and notification messages
	
	- ✅ add probability of launch and launch location, separate from mission name etc. with \n\n
	
	- ✅ allow users to set their own notifications (i.e. 24h/12h/...)
	
	- ✅ allow users to set their own timezone
	
	
	## 1.6 / major back-end changes (October 2020)
	
	- ✅ upgrade to the LL2 API (LL1 closes at the end of October)
	
	- ✅ update from telepot Telegram API wrapper to python-telegram-bot
	
	- ✅ perform API requests intelligently, as the monthly request quota is enough for only one request every 8 minutes
	
		- ✅ don't update API on startup, unless it has been sufficiently long since last check: store requests in memory + storage
		
		- ✅ use schedule to schedule requests: just before a launch is due, plus for when notifications are supposed to be sent
		
		- ✅ a raw update schedule of once every 15 - 60 minutes
		
	- ✅ check for launch notifications intelligently
		
		- ✅ on API update, check for updated launch times (notification send times) -> clear schedule queue -> schedule next checks for when a notification is supposed to be sent
		
	- ✅ store LL2 and SpX API data in the same database
	
	- ✅ combine all separate database files into one file with multiple tables
	
	- ✅ attempt to split the monolithic file into multiple separate files, starting with the API request functions
	
	- ✅ index launches by the new unique launch ID instead of launch name

	- ✅ fully integrate new API and notifications systems with LaunchBot 1.5

	- ✅ complete pre_handler(), so we can update time zone information and get feedback

	- ✅ re-add statistics to all needed places

	- add "show changelog" button under /statistics or /help

		- load from a changelog.txt file?

	- ✅ open-source LaunchBot ✨
	
	
	## 1.7 / performance optimizations
	
	- send notifications for launches entering into the middle of notification windows
	
		- e.g. if a launch suddenly pops up with T-9 hours until launch, currently the 12 hour notification is skipped (as expected)
		
		- this could cause launches to be missed, however: so, in this case, a notification with "launching in 9 hours" would be sent

	- identify bottlenecks in processing by benchmarking and timing functions

		- the largest bottleneck is usually Telegram's API

	- 🚧 improve json-parsing performance by using multiprocessing

	- write stats to redis only, schedule disk writes to SQLite every ≈30 misn

		- easy to do e.g. every API update
	
	- 🚧 use in-memory caching, like redis or memcached, to handle all responses

		- reduce disk writes and reads: SD cards have terrible latency, LPDDR4 on RasPi is pretty snappy
	
		- update cache on API call

		- key:vals for all chats: simple, fast, easy

	- enable the disabling of postpone notifications

		- globally or on a per-launch basis
</details>
