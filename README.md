# LaunchBot – a rocket launch information and notifications bot for Telegram 🚀
A rocket launch info & notifications bot for Telegram. Reachable at `@rocketrybot` on Telegram.

APIs used: Launch Library for flights, r/SpaceX API for Falcon booster and recovery information.

**Basic instructions**

Install the Python3 dependencies with PIP, using the requirements.txt file found in the repository: `python3 -m pip install -R requirements.txt `

After the dependencies are installed, you can run the bot with `python3 launchbot.py -start`. If you need/want logging, add the `-log` flag. For debugging purposes and running with nohup, add `-debug`.


**Planned features**

- allow users to choose their own timezone
- allow users to choose which notifications (24h/12h/1h/5m) they want to receive

**Other TODO**

- code cleanup, especially API calls and the json parsing
- clean up code by not using a billion indices and variable names -> objects (i.e. launch.id instead of launch_id)

**Basic data structures**

The bot creates multiple files, located under `/data`:

SQLite: `statistics.db`, `launches.db`, & `spx-launches.db`

JSON: `bot_settings.json`
