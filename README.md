# LaunchBot – a rocket launch information and notifications bot
A rocket launch info & notifications bot for Telegram. Reachable at `@rocketrybot` on Telegram.

APIs used: Launch Library for flights, r/SpaceX API for Falcon 9/Heavy booster and recovery information.

If you host the bot yourself, please change `headers = {'user-agent': 'telegram-launchbot/0.2'}` in `getLaunchUpdates()` to something else.

**Basic instructions**

Install the Python3 dependencies with PIP, using the requirements.txt file found in the repository: `python3 -m pip install -R requirements.txt `

After the dependencies are installed, you can run the bot with `python3 launchbot.py -start`. If you need/want logging, add the `-log` flag. For debugging purposes and running with nohup, add `-debug`.

**Basic data structure**

The bot creates multiple files and SQLite databases:

SQLite: `statistics.db`, `launches.db`, & `spx-launches.db`
