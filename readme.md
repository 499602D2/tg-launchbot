# LaunchBot – a rocket launch information and notifications bot for Telegram 🚀
LaunchBot keeps you up to date with what's going up, around the clock, since 2019. Over 900'000 notifications delivered to thousands of chats and groups!

Reachable as [@rocketrybot](https://t.me/rocketrybot) on Telegram.

![preview](notification.png)

## Features
LaunchBot uses the LaunchLibrary2 API to fetch launch information on scheduled intervals. The bot provides multiple forms of information: launch notifications, information about upcoming flights, and a simple flight schedule displaying upcoming flights at a glance.

Other features include...
- user-configurable notifications on a per-provider and per-country basis
- user-configurable notification times from 4 different options
- notifications of launches being postponed
- muteable launches
- direct links to launch webcasts
- automatically cleared notification messages
- simple information refresh with Telegram's message buttons
- spam management for groups (removes requests the bot won't respond to)

## Basic instructions
1. Clone the repository and install all dependencies with `go get all`

2. `cd` into `/cmd` with `cd cmd`

3. Build the program with `./build.sh`. This may require you to allow executing the script: this can be done with `chmod +x build.sh`

4. `cd` back into the main folder with `cd ..`

Now, you can run the program: to start, open a new terminal window, and run `./launchbot`. The bot will ask you for a Telegram bot API key: you can get one from BotFather on Telegram.

If you would like to view the logs as they come in, instead of saving them to a dedicated log-file, add the `--debug` CLI flag: `./launchbot --debug`.

## Data
SQLite: `data/launchbot.db`: houses all data the bot needs to operate, including launch information, statistics, chat preferences, etc.

JSON: `data/config.json`: used to configure the bot by setting the Telegram bot API key, alongside with some other configuration information.

You can specify your personal account's Telegram user ID in `config.json` in the form `owner: 12345`. This disables the logging of commands sent by you.

## Privacy

In order to operate, LaunchBot must save a chat ID. This may or may not be your user ID, depending on whether the chat is a one-on-one or a group chat. The chat ID is used to deliver notifications, manage spam, and keep statistics. Users can optionally store their time zone as a time zone database entry (e.g. Europe/Berlin), which can be removed at any time.

When LaunchBot is added to a new group, it looks up the number of users the group has. Apart from the chat ID, this is the only extra information saved, and is only used to get an idea of the reach of the bot.

The above only applies on a per-bot-instance basis. The creator of the bot chooses whether to configure the bot to be able to read all text messages, not just ones directed at the bot. Telegram bots are, by nature, extremely privacy invasive: don't add unknown bots to group chats, unless it's hosted by you or someone you trust.

## Dependencies

TODO

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
	
	- [ ] add a "more info"/"less info" button to /next and notification messages
	
	- ✅ add probability of launch and launch location, separate from mission name etc. with \n\n
	
	- ✅ allow users to set their own notifications (i.e. 24h/12h/...)
	
	- ✅ allow users to set their own timezone
	
	
	## 2.0 / major back-end changes (October 2020)
	
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

	- ✅ open-source LaunchBot ✨


	## 2.1 (December 2020 to February 2022)

	- ✅ Postpone notification fixes

	- ✅ Local bot API server support

	- ✅ Attempt to reduce rate-limits caused by sending stuff too fast at Telegram's API

	- ✅ Various edge-case and bug fixes
	
	
	## 3.0 / LaunchBot rework in Go (May 2022)

	- ✅ Improved code quality and project layout

	- ✅ Improved robustness and error recoverability of the backend

	- ✅ Dequeue messages properly to stay within API limits

	- ✅ Smart spam management for commands and callbacks, which reduces rate-limiting

	- ✅ Remove excessive complexity in storage and caching

	- ✅ Modularize most functions so that adding e.g. Discord functionality is easier

	- ✅ Reuse proven Python logic where possible with direct translation

	- ✅ Improve performance with better caching and Go's performance upside

	- ✅ Dance around API limits by sending incomplete messages, where the rest of the message can be later expanded

	- ✅ Add some group-specific settings, e.g. command permissions

	## 3.1 and onwards

	- [ ] Inline queries (should be trivial to do)

	- [ ] Handle window starts/ends

		- Instead of continuous postponements, notify of window start -> 5 min notification

	- [ ] Support for general event types (event/upcoming endpoint)

		- Wrap launches in an Event{} type

	- [ ] "Featured launches" subscription option, for interesting one-off launches

	- [ ] Weekly summary messages

	- [ ] Web-app based set-up screen, notification info?

		- https://core.telegram.org/bots/webapps

		- Privacy implications

	- [ ] Discord support


</details>