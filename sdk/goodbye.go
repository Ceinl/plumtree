package sdk

import "github.com/Ceinl/plumtree/sdk/abi"

// SetGoodbye registers a message to display on the user's terminal after the
// SSH session ends (after the alt-screen is closed). Call it before Quit to
// show a "thanks for using" or similar message that persists in the terminal
// scrollback. An empty message or a message exceeding the size limit is silently
// ignored.
func SetGoodbye(msg string) { goodbyeSet(msg) }

// MaxGoodbyeLen is the maximum length of a goodbye message.
const MaxGoodbyeLen = abi.GoodbyeMaxLen
