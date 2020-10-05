# LaunchBot – a rocket launch information and notifications bot for Telegram 🚀
A rocket launch info & notifications bot for Telegram. Reachable at `@rocketrybot` on Telegram.

APIs used: Launch Library for flights, r/SpaceX API for extra data (orbits, recovery, booster)

**Basic instructions**

Install the Python3 dependencies with PIP, using the requirements.txt file found in the repository: `python3 -m pip install -R requirements.txt`

After the dependencies are installed, you can run the bot with `python3 launchbot.py -start`. If you need/want logging, add the `-log` flag. For debugging purposes and running with nohup, add `-debug`.


**Basic data structures**

The bot creates multiple files, located under `/data`:

SQLite: `statistics.db`, `launches.db`, `spx-launches.db`, `notifications.db`, `sent-notifications.db`, `preferences.db`

JSON: `bot_settings.json`

You can specify your Telegram user ID in bot_settings.json in the form `owner: your_user_id`. This disabled the logging of commands sent by the owner, as well as sends a notification of new feedback.

**Privacy**

The bot stores every interaction (i.e. command) sent to it if logging is enabled, alongside with a truncated, unsalted SHA-1 hash of the user ID. No text messages are stored, aside from text sent as a reply to a feedback request message. The bot's privacy settings forbid the bot from reading regular text, as in text messages which have not tagged the bot (@botusername) or are not a reply to a message sent by the bot (these are not logged, unless they're a reply to a feedback message.)

Only information stored by the bot is the chat ID, which can also be the user ID of a user in the case of a private chat. This is the only user information stored, which is used to deliver notifications. If no notifications are enabled, no information is stored, aside from an in-memory chat ID for managing spam, which is automatically cleared when the program quits and is thus never stored.

## **Bot roadmap**

### 0.1 / first implementation (November)

	- ✅ implemented uncached API requests
	
	- ✅ implemented the request of next launch via a direct API call

### 0.2 / basic features (December)

	- ✅ implement /next using DB calls
	
	- ✅ implement support for SpaceX core information

### 0.3 / user-facing features (January)
	
	- ✅ add "next" and "previous" button(s) to /next command
	
	- ✅ add a mute button to notifications
	
	- ✅ update /notify to be more user friendly
	
	- ✅ implement /feedback
	
	- ✅ improve notification handling with the hold flag -> moving NETs and info text regarding them
	
	- ✅ change launch database index from tminus to net

### 0.4 / basic improvements (February ->)

	- ✅ Notify users of a launch being postponed if a notification has already been sent
	
	- ✅ disable logging of text messages; how to do feedback? (log feedback messages in a global array?)
	
	- ✅ add tbd-field to launches, so schedule can only show certain launch dates (filter certain and uncertain with a button)
	
	- ✅ add location (i.e. state/country) below pad information (Florida, USA etc.)

### 0.5 / user-facing features
	
	- ✅ **(moved to 0.6)** allow users to disable postpone notifications on a per-launch basis
	
	- ✅ delete older notification messages when a new one is sent
	
	- add a "more info"/"less info" button
	
	- ✅ add probability of launch and launch location, separate from mission name etc. with \n\n
	
	- ✅ **(moved to 0.6)** handle notification send checks with schedule, instead of polling every 20-30 seconds (i.e. update schedule every time db is updated)
	
	- ✅ **(moved to 0.6)** unify spx-launch database and launch database into one file with separate tables
	
	- ✅ allow users to set their own notifications (i.e. 24h/12h/...)
	
	- ✅ allow users to set their own timezone
	
### 0.6 / major back-end improvements and changes
	
	- update to the LL2 API
	
	- perform API requests intelligently, as the monthly request quota is enough for only one request every 8 minutes
	
		- don't update API on startup, unless it has been sufficiently long since last check: store requests in memory + storage
		
		- use schedule to schedule requests: just before a launch is due, plus for when notifications are supposed to be sent
		
		- a raw update schedule of once every 15 - 60 minutes
		
	- check for launch notifications intelligently
		
		- on API update, check for updated launch times (notification send times) -> clear schedule queue -> schedule next checks for when a notification is supposed to be sent 
		
	- store LL2 and SpX API data in the same database
	
	- combine all separate database files into one file with multiple tables
	
	- use an in-memory DB to handle all responses
	
		- update in-mem DB on API call, push update to disk -> persistence
	
	- attempt to split the monolithic file into multiple separate files, starting with the API request functions
	
	- index launches by the new unique launch ID instead of launch name
	
	- enable the disabling of postpone notifications

	- improve json-parsing performance by using pooling
	
	
