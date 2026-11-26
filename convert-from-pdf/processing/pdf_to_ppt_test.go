package processing

import "testing"

func TestPptMode(t *testing.T) {
	cases := []struct {
		opts map[string]interface{}
		want string
	}{
		{nil, pptModeEditable},
		{map[string]interface{}{}, pptModeEditable},
		{map[string]interface{}{"mode": "editable"}, pptModeEditable},
		{map[string]interface{}{"mode": "image"}, pptModeImage},
		{map[string]interface{}{"mode": " IMAGE "}, pptModeImage},
		{map[string]interface{}{"mode": "unknown"}, pptModeEditable},
		{map[string]interface{}{"mode": 3}, pptModeEditable},
	}
	for _, c := range cases {
		if got := pptMode(c.opts); got != c.want {
			t.Errorf("pptMode(%v) = %q, want %q", c.opts, got, c.want)
		}
	}
}

func TestHasIndicScript(t *testing.T) {
	for _, s := range []string{"यह फायरडॉक्स", "বাংলা", "தமிழ்", "mixed English हिंदी"} {
		if !hasIndicScript(s) {
			t.Errorf("hasIndicScript(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "Quarterly invoice 2026", "Café résumé", "日本語"} {
		if hasIndicScript(s) {
			t.Errorf("hasIndicScript(%q) = true, want false", s)
		}
	}
}
