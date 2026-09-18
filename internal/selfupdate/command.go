package selfupdate

// Command is the `update` command surface shared by pt and plumtree. Every
// dependency is a field, so tests inject a fake release source and a
// temporary directory standing in for the install directory.
type Command struct {
	ExePath string
	Version string
	Source  Source
	Confirm func(string) bool
}
