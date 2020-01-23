# LaunchBot – a rocket launch information and notifications bot
A rocket launch info & notifications bot for Telegram. Reachable at `@rocketrybot` on Telegram.

APIs used: Launch Library for flights, r/SpaceX API for Falcon 9/Heavy booster and recovery information.

**Basic instructions**

Install the Python3 dependencies with PIP, using the requirements.txt file found in the repository: `python3 -m pip install -R requirements.txt `

After the dependencies are installed, you can run the bot with `python3 launchbot.py -start`. If you need/want logging, add the `-log` flag. For debugging purposes and running with nohup, add `-debug`.


**Features**

TBW

**Basic data structure**

The bot creates multiple files, located under `/data`:

SQLite: `statistics.db`, `launches.db`, & `spx-launches.db`

JSON: `bot_settings.json`
