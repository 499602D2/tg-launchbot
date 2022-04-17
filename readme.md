## LaunchBot rework in Go

### Goals of the rework
🌟 Massively improve code quality and project layout
🌟 Improve robustness and error recoverability of the backend
🌟 Intelligently dequeue messages to stay within API limits
🌟 Remove excessive complexity in storage and caching
🌟 Enable extending the bot to other platforms through modularity
🌟 Reuse proven Python code where possible with direct translation

### Must-haves before 3.0.0
- "Compress" messages to improve send-rates
	- Add "More info" button
- Remove manual time zone feature to reduce complexity

### Nice-to-haves before 3.0.0
- Notify admin on any processing failure

### Future: 3.1 and onwards
- Weekly summary messages
- Web-app based set-up screen, notification info..?
	- https://core.telegram.org/bots/webapps
	- Privacy implications
- Discord support
