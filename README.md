# LaunchBot – a rocket launch information and notifications bot for Telegram 🚀
A rocket launch info & notifications bot for Telegram. Reachable at `@rocketrybot` on Telegram.

APIs used: Launch Library for flights, r/SpaceX API for extra data (orbits, recovery, booster)

**Basic instructions**

Install the Python3 dependencies with PIP, using the requirements.txt file found in the repository: `python3 -m pip install -R requirements.txt`

After the dependencies are installed, you can run the bot with `python3 launchbot.py -start`. If you need/want logging, add the `-log` flag. For debugging purposes and running with nohup, add `-debug`.


**Basic data structures**

The bot creates multiple files, located under `/data`:

SQLite: `statistics.db`, `launches.db`, `spx-launches.db`, `notifications.db`

JSON: `bot_settings.json`

You can specify your Telegram user ID in bot_settings.json in the form `owner: your_user_id`. This disabled the logging of commands sent by the owner, as well as sends a notification of new feedback.

**Bot roadmap**

0.2 (December):
	- ✅ implement /next using DB calls
	- ✅ implement support for SpaceX core information

0.3 (January):
	- ✅ add "next" and "previous" button(s) to /next command
	- ✅ add a mute button to notifications
	- ✅ update /notify to be more user friendly
	- ✅ implement /feedback
	- ✅ improve notification handling with the hold flag -> moving NETs and info text regarding them
	- ✅ change launch database index from tminus to net

0.4.X (February)
	- ✅ Notify users of a launch being postponed if a notification has already been sent
	- ✅ disable logging of text messages; how to do feedback? (log feedback messages in a global array?)
	- add tbd-field to launches, so schedule can only show certain launch dates (filter certain and uncertain with a button)
	- add location (i.e. state/country) below pad information (Florida, USA etc.)

0.5 (Next feature release)
	- allow users to disable postpone notifications on a per-launch basis
	- delete older notification messages when a new one is sent
	- add a "more info"/"less info" button
	- add probability of launch and launch location, separate from mission name etc. with \n\n
	- handle notification send checks with schedule, instead of polling every 20-30 seconds (i.e. update schedule every time db is updated)
	- unify spx-launch database and launch database into one file with separate tables
	- allow users to set their own notifications (i.e. 24h/12h/...)
	- allow users to set their own timezone

Later versions
	- functionize more of the processes
		- move callbacks to a function, pass text + tuple + keyboard as args
