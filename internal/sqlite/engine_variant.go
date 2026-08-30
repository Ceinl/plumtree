//go:build !sqlcipher

package sqlite

import "database/sql/driver"

const (
	compiledSQLCipher = false
	engineVariant     = "sqlite-development"
)

func wrapKeyedDriver(base driver.Driver, _ []byte) driver.Driver {
	return base
}
