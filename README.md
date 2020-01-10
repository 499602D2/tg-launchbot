# LaunchBot – a rocket launch information and notifications bot
A rocket launch info & notifications bot for Telegram. Reachable at `@rocketrybot` on Telegram.

APIs used: Launch Library for flights, r/SpaceX API for Falcon 9/Heavy booster and recovery information.

If you host the bot yourself, please change `headers = {'user-agent': 'telegram-launchbot/0.2'}` in `getLaunchUpdates()` to something else.
