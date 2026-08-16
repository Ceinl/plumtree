//go:build sqlcipher && !cgo

package sqlite

const (
	compiledSQLCipher = false
	engineVariant     = "sqlcipher-requires-cgo"
)
