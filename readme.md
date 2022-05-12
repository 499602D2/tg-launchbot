## LaunchBot rework in Go

### Goals of the rework
🌟 **Massively improve code quality** and project layout

🌟 **Improve robustness** and error recoverability of the backend

🌟 **Intelligently dequeue messages** to stay within API limits

🌟 **Remove excessive complexity** in storage and caching

🌟 **Enable extending the bot** to other platforms through modularity

🌟 **Reuse proven Python logic** where possible with direct translation

🌟 **Improve performance** by simply switching to Go

### To-do before 3.0.0
- [ ] Architecture overview diagram in readme
- [x] LL2 API `/launch/upcoming` handler

- [ ] Telegram bot API
	- [ ] Add error handlers
		- [x] Catch-all type handlers
		- [x] Chat migrations
		- [ ] Odd edge-case handlers (check launchbot.py)
	- [ ] Implement callbacks
		- [ ] Notifications
			- [ ] Mute
				- [ ] Only allow admins to mute a launch
			- [x] Expand description
		- [x] Commands
	- [x] Use a dual-limiter

- [ ] Add database functions
	- [x] Create database, auto-migrations
	- [x] Launch inserts
	- [x] Stats updates
	- [x] User functions
		- [x] Statistics
		- [x] Time zone  
		- [x] Notification updates
		- [x] Chat migrations

- [ ] Caching
	- [x] Launches
	- [x] Active users
		- [ ] Regularly clean cache (once a day, e.g.)
			- Easy to do with user.Flush()

- [ ] Add commands
	- [x] /settings
		- [ ] Remove the Subscription settings -menu: add a direct button to notification time settings?
	- [x] /next
	- [x] /schedule
	- [x] /stats
	- [ ] /feedback + response script
	- [ ] Admin functions (/debug)

- [ ] Notifications
	- [x] Scheduling
	- [ ] Pre-send API update (just compare NETs)
		- [ ] Postpone notifications
	- [ ] Recipient list on notification send
		- [ ] Check for mute status
	- [ ] Mute notifications
	- [x] Sending

- [ ] Other, backend
	- [ ] Update stats wherever needed
	- [ ] Regularly dump statistics to disk, especially on ctrl+c

- [ ] Database migration from v2 to v3
	- [ ] Acceptable level of data lost?
		- [ ] Manually map launch provider names to IDs

### Must-haves before 3.0.0
- [x] "Compress" messages to improve send-rates
	- [x] Add "More info" button
		- [x] Implement for description
		- [ ] Implement for reuse information
- [x] Remove manual time zone feature to reduce complexity
- [ ] Purge log-files when they become too large
	- Also, be smarter about telebot's logging (raise an issue?)

### Nice-to-haves before 3.0.0
- [x] Notify admin on any processing failure
	- [x] Telegram
- [x] Allow postpone notifications to be disabled
- [ ] Allow chats to flip a setting to enable everyone to send commands (callbacks only by the initial sender?)

### Future: 3.1 and onwards
- [ ] Handle window starts/ends
	- Instead of continuous postponements, notify of window start -> 5 min notification
- [ ] Support for general event types (event/upcoming endpoint)
	- Wrap launches in an Event{} type
	- https://ll.thespacedevs.com/2.2.0/
- [ ] Weekly summary messages
- [ ] Web-app based set-up screen, notification info..?
	- https://core.telegram.org/bots/webapps
	- Privacy implications
- [ ] Discord support
