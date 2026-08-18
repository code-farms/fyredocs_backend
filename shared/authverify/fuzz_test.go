// Fuzz target for the scope-claim decoder: it parses attacker-influenced JWT
// payload bytes (the token body is unauthenticated until the signature check,
// and this decoder also runs on gateway-relayed header values), so it must
// never panic and every accepted value must re-marshal cleanly.
package authverify

import (
	"encoding/json"
	"testing"
)

func FuzzScopeListUnmarshalJSON(f *testing.F) {
	for _, seed := range []string{
		`"read write"`, `["read","write"]`, `null`, `""`, `[]`,
		`123`, `{"x":1}`, `["a",1]`, `"  padded  scopes "`, `[""]`,
		"\"\x00\"", `["\ud800"]`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var s ScopeList
		if err := s.UnmarshalJSON(data); err != nil {
			return
		}
		if _, err := json.Marshal(s); err != nil {
			t.Errorf("accepted scopes %#v cannot re-marshal: %v", s, err)
		}
	})
}
