// Package discord provides Discord gateway bot and message sending.
// This is a stub defining the interface; full implementation is separate.
package discord

// StartGateway connects to the Discord gateway in a background goroutine.
func StartGateway(debug bool) {
	// TODO: implement
}

// StopGateway disconnects from the Discord gateway and cleans up.
func StopGateway() {
	// TODO: implement
}

// SendMessageToUser sends a DM to a Discord user by their snowflake ID.
func SendMessageToUser(token, userID, text string) error {
	// TODO: implement
	return nil
}

// Standalone runs the Discord bot as a standalone process (blocking).
func Standalone() {
	// TODO: implement
}
