package structbind_test

import (
	"testing"

	"github.com/ubgo/sqlb/contrib/structbind"
)

// TestNamers pins every derivation, including the ones that look wrong until
// you try the alternative.
//
// The table is the specification. A run of capitals is one word and a trailing
// plural s belongs to the run before it, and both rules are here because the
// naive "underscore before every capital" gives h_t_t_p_status and ur_ls, which
// no schema has ever contained.
func TestNamers(t *testing.T) {
	for _, tc := range []struct {
		field, snake, camel, pascal, lower string
	}{
		{"CreatedAt", "created_at", "createdAt", "CreatedAt", "createdat"},
		{"Email", "email", "email", "Email", "email"},
		{"IsActive", "is_active", "isActive", "IsActive", "isactive"},

		// A capital run is one word.
		{"ID", "id", "id", "Id", "id"},
		{"UserID", "user_id", "userId", "UserId", "userid"},
		{"HTTPStatus", "http_status", "httpStatus", "HttpStatus", "httpstatus"},
		{"APIKey", "api_key", "apiKey", "ApiKey", "apikey"},
		{"HTTPSPort", "https_port", "httpsPort", "HttpsPort", "httpsport"},

		// A trailing plural s joins the run rather than starting a word.
		{"URLs", "urls", "urls", "Urls", "urls"},
		{"IDs", "ids", "ids", "Ids", "ids"},

		// Digits belong to the word they sit in.
		{"Address2", "address2", "address2", "Address2", "address2"},
		{"Line1Text", "line1_text", "line1Text", "Line1Text", "line1text"},

		// The shortest thing a field can be.
		{"X", "x", "x", "X", "x"},

		// Documented limitation: no initialism list, so OAuth splits. The
		// answer is a db tag. This pins the behaviour rather than pretending
		// it is right.
		{"OAuth2Token", "o_auth2_token", "oAuth2Token", "OAuth2Token", "oauth2token"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for _, c := range []struct{ name, got, want string }{
				{"Snake", structbind.Snake(tc.field), tc.snake},
				{"Camel", structbind.Camel(tc.field), tc.camel},
				{"Pascal", structbind.Pascal(tc.field), tc.pascal},
				{"Lower", structbind.Lower(tc.field), tc.lower},
				{"Exact", structbind.Exact(tc.field), tc.field},
			} {
				if c.got != c.want {
					t.Errorf("%s(%q) = %q, want %q", c.name, tc.field, c.got, c.want)
				}
			}
		})
	}
}

// TestNamers_EmptyInput pins that a namer does not panic on an empty name.
//
// No struct field is named "", so this is unreachable through New; it is
// asserted because Namer is an exported type a caller may compose with, and a
// panic inside a helper is a poor way to learn that.
func TestNamers_EmptyInput(t *testing.T) {
	for _, n := range []struct {
		name string
		fn   structbind.Namer
	}{
		{"Snake", structbind.Snake},
		{"Camel", structbind.Camel},
		{"Pascal", structbind.Pascal},
		{"Lower", structbind.Lower},
		{"Exact", structbind.Exact},
	} {
		if got := n.fn(""); got != "" {
			t.Errorf("%s(%q) = %q, want empty", n.name, "", got)
		}
	}
}

// TestSnake_IsIdempotentOnItsOwnOutput pins that a name already in snake_case
// survives unchanged.
//
// This is what makes the default safe on a struct whose fields were named after
// existing columns, and it is the property a naive splitter breaks by inserting
// an underscore on a second pass.
func TestSnake_IsIdempotentOnItsOwnOutput(t *testing.T) {
	for _, f := range []string{"CreatedAt", "HTTPStatus", "UserID", "URLs", "Email"} {
		once := structbind.Snake(f)
		if twice := structbind.Snake(once); twice != once {
			t.Errorf("Snake(Snake(%q)) = %q, want %q", f, twice, once)
		}
	}
}
