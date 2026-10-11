# pg/crypto

The pgcrypto extension for `sqlb`. Needs `CREATE EXTENSION pgcrypto`.

| SQL | Helper |
|---|---|
| `crypt`, `gen_salt` | `Crypt`, `GenSalt` |
| `digest`, `hmac` | `Digest`, `HMAC` |
| `pgp_sym_encrypt`, `pgp_sym_decrypt` | `PgpSymEncrypt`, `PgpSymDecrypt` |
| `gen_random_bytes` | `GenRandomBytes` |

`gen_random_uuid` is core Postgres since 13 and lives in `pg` as `GenRandomUUID`. `encode(..., 'hex')` is core too: `pg.Encode`.

Hashing on the server puts the plain password on the wire and in `log_statement`. Hash in Go where you can; these helpers exist for schemas that already store pgcrypto hashes:

```go
ins.Row(sqlb.Set(UserEmail, email), sqlb.SetExpr(UserPass, crypto.Crypt(sqlb.Param(pw), crypto.GenSalt("bf", 12))))
sel.Where(sqlb.EqExpr(crypto.Crypt(sqlb.Param(pw), UserPass), UserPass))
```
