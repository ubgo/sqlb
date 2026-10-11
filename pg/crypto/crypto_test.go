package crypto

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

var (
	users = sqlb.Table{Name: "users", Alias: "u"}
	hash  = sqlb.Col[string](users, "pw_hash")
	blob  = sqlb.Col[[]byte](users, "blob")
)

func TestRender(t *testing.T) {
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
		Exprs(Crypt(sqlb.Param("secret"), GenSalt("bf", 12)), Digest(sqlb.Param("x"), "sha256"),
			HMAC(sqlb.Param("p"), sqlb.Param("k"), "sha256"), PgpSymEncrypt(sqlb.Param("d"), sqlb.Param("k")),
			PgpSymDecrypt(blob, sqlb.Param("k")), GenRandomBytes(16)).
		From(users).Where(sqlb.EqExpr(hash, Crypt(sqlb.Param("secret"), hash))).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT crypt($1, gen_salt($2, $3)), digest($4, $5), hmac($6, $7, $8), pgp_sym_encrypt($9, $10), pgp_sym_decrypt("u"."blob", $11), gen_random_bytes($12) FROM "users" AS "u" WHERE ("u"."pw_hash" = crypt($13, "u"."pw_hash"))`
	if q.SQL != want {
		t.Fatalf("got\n%s\nwant\n%s", q.SQL, want)
	}
}

func TestGuard(t *testing.T) {
	for k, e := range map[string]sqlb.SQLer{
		"Crypt": Crypt(hash, hash), "GenSalt": GenSalt("bf", 10), "Digest": Digest(hash, "md5"),
		"HMAC": HMAC(hash, hash, "md5"), "PgpSymEncrypt": PgpSymEncrypt(hash, hash),
		"PgpSymDecrypt": PgpSymDecrypt(blob, hash), "GenRandomBytes": GenRandomBytes(8),
	} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(e).From(users).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: got %v", k, err)
		}
	}
}
