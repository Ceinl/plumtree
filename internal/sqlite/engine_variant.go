//go:build !sqlcipher

package sqlite

const (
	compiledSQLCipher = false
	engineVariant     = "sqlite-development"
)
