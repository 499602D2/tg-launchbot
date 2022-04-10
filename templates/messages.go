/*
messages.go includes various message templates, that can be used by throwing
the appropriate functions some output.
*/
package templates

func HelpMessage() *string {
	msg := "Pong"
	return &msg
}
