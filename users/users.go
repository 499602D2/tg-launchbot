package users

type User struct {
	Platform string // Discord=dg, Telegram=tb, Email=email
	Id       int64
	TimeZone string
}

/* Extends the User type by creating a list of users.
This can be userful for e.g. sending notifications to one platform. */
type UserList struct {
	Platform  string
	Users     *[]User
	UserCount int
}
