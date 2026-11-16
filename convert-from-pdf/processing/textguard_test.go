package processing

import "testing"

func TestCharErrorRateBounds(t *testing.T) {
	if got := charErrorRate("abcd", "abcd"); got != 0 {
		t.Errorf("identical = %v, want 0", got)
	}
	if got := charErrorRate("abcd", "abxd"); got != 0.25 {
		t.Errorf("one substitution = %v, want 0.25", got)
	}
	if got := charErrorRate("यह फायरडॉक्स का हिंदी परीक्षण दस्तावेज़ है", "फा फा फा"); got <= textGuardMaxCER {
		t.Errorf("broken Devanagari CER %v must exceed %v", got, textGuardMaxCER)
	}
}
