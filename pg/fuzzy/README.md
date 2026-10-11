# pg/fuzzy

The fuzzystrmatch extension for `sqlb`. Needs `CREATE EXTENSION fuzzystrmatch`.

| SQL | Helper |
|---|---|
| `dmetaphone`, `dmetaphone_alt`, `metaphone`, `soundex` | `Dmetaphone`, `DmetaphoneAlt`, `Metaphone`, `Soundex` |
| `levenshtein`, `levenshtein_less_equal` | `Levenshtein`, `LevenshteinLessEqual` |
| `difference` | `Difference` |

Phonetic matching compares codes: `sqlb.EqExpr(fuzzy.Dmetaphone(name), fuzzy.Dmetaphone(sqlb.Param("Katherine")))`. Edit distance with a threshold uses the `less_equal` form so the server stops early: `sqlb.LessEq(fuzzy.LevenshteinLessEqual(sqlb.Lower(name), sqlb.Param("jonathon"), 2), 2)`.
