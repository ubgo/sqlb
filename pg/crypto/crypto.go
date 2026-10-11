// Package crypto is the pgcrypto extension: hashing, password hashing and
// symmetric encryption on the server. Needs CREATE EXTENSION pgcrypto.
//
// Hashing passwords on the server sends the plain password over the wire
// and into the statement log. Prefer hashing in Go and storing the hash;
// these helpers exist for schemas that already commit to pgcrypto.
package crypto

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

const construct = "crypto"

// Crypt renders crypt(password, salt). To verify: crypt(input, stored) = stored.
func Crypt(password, salt sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Func[string](construct+".Crypt", "crypt", password, salt)
}

// GenSalt renders gen_salt(type, rounds); type is "bf", "md5", "xdes" or
// "des" and rounds is the bcrypt cost.
func GenSalt(typ string, rounds int) sqlb.Expr[string] {
	return pg.Func[string](construct+".GenSalt", "gen_salt", sqlb.Param(typ), sqlb.Param(rounds))
}

// Digest renders digest(data, algo); algo is "sha256", "sha1", "md5".
func Digest(data sqlb.Expr[string], algo string) sqlb.Expr[[]byte] {
	return pg.Func[[]byte](construct+".Digest", "digest", data, sqlb.Param(algo))
}

// HMAC renders hmac(data, key, algo).
func HMAC(data, key sqlb.Expr[string], algo string) sqlb.Expr[[]byte] {
	return pg.Func[[]byte](construct+".HMAC", "hmac", data, key, sqlb.Param(algo))
}

// PgpSymEncrypt renders pgp_sym_encrypt(data, key). The key travels to the
// server on every call, so the server and its logs see it.
func PgpSymEncrypt(data, key sqlb.Expr[string]) sqlb.Expr[[]byte] {
	return pg.Func[[]byte](construct+".PgpSymEncrypt", "pgp_sym_encrypt", data, key)
}

// PgpSymDecrypt renders pgp_sym_decrypt(data, key), the reverse of
// PgpSymEncrypt. It returns text, so the ciphertext column is []byte and the
// result is a string.
func PgpSymDecrypt(data sqlb.Expr[[]byte], key sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Func[string](construct+".PgpSymDecrypt", "pgp_sym_decrypt", data, key)
}

// GenRandomBytes renders gen_random_bytes(n).
func GenRandomBytes(n int) sqlb.Expr[[]byte] {
	return pg.Func[[]byte](construct+".GenRandomBytes", "gen_random_bytes", sqlb.Param(n))
}
