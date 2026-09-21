#!/usr/bin/env bash
set -euo pipefail

: "${PLUMTREE_REAL_CC:?set PLUMTREE_REAL_CC to the target compiler}"
: "${PLUMTREE_SQLCIPHER_INCLUDE:?set PLUMTREE_SQLCIPHER_INCLUDE to the target SQLCipher include directory}"
: "${SQLCIPHER_PREFIX:?set SQLCIPHER_PREFIX to the target SQLCipher prefix}"

library=${PLUMTREE_SQLCIPHER_LIBRARY:-sqlite3}
rewritten=()
for argument in "$@"; do
	case "$argument" in
		-L/usr/local/opt/sqlite/lib|-L/opt/homebrew/opt/sqlite/lib)
			argument="-L${SQLCIPHER_PREFIX}/lib"
			;;
		-I/usr/local/opt/sqlite/include|-I/opt/homebrew/opt/sqlite/include)
			argument="-I${PLUMTREE_SQLCIPHER_INCLUDE}"
			;;
		-lsqlite3)
			argument="-l${library}"
			;;
	esac
	rewritten+=("$argument")
done

exec "$PLUMTREE_REAL_CC" "${rewritten[@]}"
