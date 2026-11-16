package oracle

import (
	"testing"

	"bench-engine/internal/diff"
)

func TestParseInfoAppliesRotation(t *testing.T) {
	out := []byte("Pages:          3\n" +
		"Page    1 size: 612 x 792 pts (letter)\nPage    1 rot:  0\n" +
		"Page    2 size: 612 x 792 pts (letter)\nPage    2 rot:  90\n" +
		"Page    3 size: 612 x 792 pts (letter)\nPage    3 rot:  -90\n")
	in := parseInfo(out)
	want := []diff.PageSize{{W: 612, H: 792}, {W: 792, H: 612}, {W: 792, H: 612}}
	if in.Pages != 3 {
		t.Fatalf("pages = %d, want 3", in.Pages)
	}
	for i, w := range want {
		if in.Sizes[i] != w {
			t.Errorf("page %d = %v, want %v", i+1, in.Sizes[i], w)
		}
	}
}
