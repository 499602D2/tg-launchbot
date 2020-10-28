# LaunchBot – a rocket launch information and notifications bot for Telegram 🚀
LaunchBot keeps you up to date with what's going up, around the clock, since 2019. Reachable as [@rocketrybot](https://t.me/rocketrybot) on Telegram.

LaunchBot uses the LaunchLibrary2 API to fetch launch information on intelligently scheduled intervals (due to a quite strict API call-count limit introduced with LL2). The bot provides multiple forms of information: notifications, information about upcoming flights, and a simple flight schedule showing the upcoming flights at a glance. 

✨ **Other features include...**
- user-configurable notifications on...
	- per-provider basis
	- per-country basis
- user-choosable notification times from 4 different options
- mutable launches
- notifications of launches being postponed
- a quick, easily digestible message format
- direct links to launch webcasts
- automatically cleared notification messages
- neat statistics about the bot
- direct feedback to the developer via the bot
- smart spam management
- simple information refresh with Telegram's message buttons,

and tons of other things!

**📃 Basic instructions**

Install the Python3 dependencies with pip, using the requirements.txt file found in the repository: `python3 -m pip install -R requirements.txt`.

After the dependencies are installed, you can run the bot with `python3 launchbot.py -start`. For debugging purposes and running in the background with nohup, add `-debug`: this prevents the flooding of the `nohup.out` file.

**🖥 Data structures**

The bot creates the following supporting files under `../launchbot/`:

SQLite: `launchbot-data.db`: houses all data the bot needs to operate, including launch caching, statistics, etc.

JSON: `bot-settings.json`: used to configure the bot by setting the Telegram bot API key, alongside with some other configuration information.

You can specify your personal account's Telegram user ID in bot_settings.json in the form `owner: your_user_id`. This disabled the logging of commands sent by you, and sends a notification for new feedback.

**🔒 Privacy**

The bot stores every interaction (i.e. command) sent to it if logging is enabled, alongside with a truncated, unsalted SHA-1 hash of the user ID. No text messages are stored, aside from text sent as a reply to a feedback request message. The bot's privacy settings forbid the bot from reading regular text, as in text messages which have not tagged the bot (@botusername) or are not a reply to a message sent by the bot (these are not logged, unless they're a reply to a feedback message.)

Only information stored by the bot is the chat ID, which can also be the user ID of a user in the case of a private chat. This is the only user information stored, which is used to deliver notifications. If no notifications are enabled, no information is stored, aside from an in-memory chat ID for managing spam, which is automatically cleared when the program quits and is thus never stored.

Please note, that the above only applies on a per-bot basis. The creator of the bot chooses whether to configure the bot to be able to read all text messages, not just ones directed at the bot. Telegram bots are, by nature, extremely privacy invasive: don't add unknown bots to group chats, unless it's hosted by you or someone you trust.

## **Bot roadmap**

### 1.0 / first implementation (November 2019)

	- ✅ implemented uncached API requests
	
	- ✅ implemented the request of next launch via a direct API call

### 1.2 / basic features (December 2019)

	- ✅ implement /next using DB calls
	
	- ✅ implement support for SpaceX core information

### 1.3 / user-facing features (January 2020)
	
	- ✅ add "next" and "previous" button(s) to /next command
	
	- ✅ add a mute button to notifications
	
	- ✅ update /notify to be more user friendly
	
	- ✅ implement /feedback
	
	- ✅ improve notification handling with the hold flag -> moving NETs and info text regarding them
	
	- ✅ change launch database index from tminus to net

### 1.4 / basic improvements (February 2020 ->)

	- ✅ Notify users of a launch being postponed if a notification has already been sent
	
	- ✅ disable logging of text messages; how to do feedback? (log feedback messages in a global array?)
	
	- ✅ add tbd-field to launches, so schedule can only show certain launch dates (filter certain and uncertain with a button)
	
	- ✅ add location (i.e. state/country) below pad information (Florida, USA etc.)

### 1.5 / user-facing features
	
	- ✅ delete older notification messages when a new one is sent
	
	- add a "more info"/"less info" button to /next and notification messages
	
	- ✅ add probability of launch and launch location, separate from mission name etc. with \n\n
	
	- ✅ allow users to set their own notifications (i.e. 24h/12h/...)
	
	- ✅ allow users to set their own timezone
	
### 1.6 / major back-end improvements and changes (October 2020)
	
	- ✅ upgrade to the LL2 API (LL1 closes at the end of October)
	
	- ✅ perform API requests intelligently, as the monthly request quota is enough for only one request every 8 minutes
	
		- ✅ don't update API on startup, unless it has been sufficiently long since last check: store requests in memory + storage
		
		- ✅ use schedule to schedule requests: just before a launch is due, plus for when notifications are supposed to be sent
		
		- ✅ a raw update schedule of once every 15 - 60 minutes
		
	- ✅ check for launch notifications intelligently
		
		- ✅ on API update, check for updated launch times (notification send times) -> clear schedule queue -> schedule next checks for when a notification is supposed to be sent
		
	- ✅ store LL2 and SpX API data in the same database

	- add "show changelog" button under /help

		- load from a changelog.txt file?
	
		- or, replace /help with /info?
	
	- ✅ combine all separate database files into one file with multiple tables
	
	- ✅ attempt to split the monolithic file into multiple separate files, starting with the API request functions
	
	- ✅ index launches by the new unique launch ID instead of launch name

	- ✅ fully integrate new API and notifications systems with LaunchBot 1.5

	- complete pre_handler(), so we can update time zone information and get feedback

	- re-add statistics to all needed places

	- improve json-parsing performance by using pooling

	- open-source LaunchBot ✨
	
### 1.7 more backend changes

	- ✅ update from telepot Telegram API wrapper to python-telegram-bot
	
	- enable the disabling of postpone notifications

		- globally or on a per-launch basis
	
	- use an in-memory DB, like redis or memcached, to handle all responses
	
		- update in-mem DB on API call, push update to disk -> persistence
