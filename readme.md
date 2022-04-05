## LaunchBot rework in Go

### Goals
1. Massively refactor the code and improve code quality
2. Improve the robustness of the backend
3. Remove excessive complexity in both storage and caching
3. Make extending the bot onto other platforms trivial (modularity)

### Before 3.0.0
- Compress messages to improve send-rates
	- Add "More info" button
- Remove manual time zones

### Nice-to-haves
- Notify on fail/success
- Weekly summary messages
