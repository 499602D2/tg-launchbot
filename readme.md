## LaunchBot rework in Go

### Goals of the rework
🌟 Massively improve code quality and project layout

🌟 Improve robustness and error recoverability of the backend

🌟 Intelligently dequeue messages to stay within API limits

🌟 Remove excessive complexity in storage and caching

🌟 Enable extending the bot to other platforms through modularity

🌟 Reuse proven Python code where possible with direct translation

🌟 Benefit from the performance-upside associated with Go

### To-do
- Error monitoring?

- LaunchLibrary2 API

- Telegram bot API
	- Add error handlers
		- Migrations!!!
	- Implement callbacks
	- Handle callbacks with sender (limits?)
	- Use a dual-limiter
		- Issues with sends?
		- Only add to queue _after_ user has suffered their rate-limit -> easy
	- Use preHandler as middleware

- Add database functions
	- Launch inserts
	- Stats updates
	- User functions
		- Hardest
			- /Notify
			- Chat migrations
		- Rethink

- Add commands
	- /notify
		- Replace /notify with /settings?
		- Database required
			- Use provider IDs (requires reverse mapping, trivial to do manually)
		- More:
			- dynamically generate lists for missing providers
			- Re-think how database inserts are done: try to simplify
	- /next
		- Easy with launch cache
	- /schedule
	- /stats
	- /feedback + response script
	- Admin functions (/debug)

- Database conversion script (Python fine)

### Must-haves before 3.0.0
- "Compress" messages to improve send-rates
	- Add "More info" button
- Remove manual time zone feature to reduce complexity
- Purge log-files when they become too large
	- Alternatively, be smarter about telebot's logging

### Nice-to-haves before 3.0.0
- Notify admin on any processing failure

### Future: 3.1 and onwards
- Handle window starts/ends
	- Instead of continuous postponements, notify of window start -> 5 min notification
- Support for general event types (event/upcoming endpoint)
	- Wrap launches in an Event{} type
	- https://ll.thespacedevs.com/2.2.0/
- Weekly summary messages
- Web-app based set-up screen, notification info..?
	- https://core.telegram.org/bots/webapps
	- Privacy implications
- Discord support
