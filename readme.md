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
- [ ] Verify no maps are used where the read is expected to be ordered
- [ ] LaunchLibrary2 API
	- [x] `/launch/upcoming`
	- [ ] `/lsp`

- [ ] Telegram bot API
	- [ ] Add error handlers
		- [x] Catch-all type handlers
	- [ ] Implement callbacks
		- [ ] Notifications
			- [ ] Mute
			- [x] Expand description
		- [x] Commands
	- [x] Use a dual-limiter

- [ ] Add database functions
	- [x] Create database, auto-migrations
	- [x] Launch inserts
	- [ ] Stats updates
	- [ ] User functions
		- [ ] Statistics
		- [x] Time zone  
		- [x] Notification updates
		- [x] Chat migrations

- [ ] Caching
	- [x] Launches
	- [x] Active users
		- [ ] Regularly clean cache (once a day, e.g.)
			- gochron

- [ ] Add commands
	- [x] /notify
	- [x] /next
	- [x] /schedule
	- [x] /stats
	- [ ] /feedback + response script
	- [ ] Admin functions (/debug)

- [ ] Database migration from v2 to v3

### Must-haves before 3.0.0
- [x] "Compress" messages to improve send-rates
	- [x] Add "More info" button
		- [x] Implement for description
		- [ ] Implement for reuse information
- [x] Remove manual time zone feature to reduce complexity
- [ ] Purge log-files when they become too large
	- Also, be smarter about telebot's logging

### Nice-to-haves before 3.0.0
- [x] Notify admin on any processing failure
	- [x] Telegram
- [x] Allow postpone notifications to be disabled

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
