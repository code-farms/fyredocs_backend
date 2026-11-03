package imaging

import (
	"image/color"
	"testing"
)

func TestRemoveFingersOnlyAtTheBorder(t *testing.T) {
	t.Parallel()
	img := page(400, 500, paperGrey)
	ellipse(img, 0, 250, 45, 30, skin)   // a thumb on the left margin
	ellipse(img, 220, 200, 35, 35, skin) // a face photo inside the page
	n := RemoveFingers(img)
	if n != 1 {
		t.Fatalf("filled %d regions, want 1 (the border thumb)", n)
	}
	if r, g, b := rgbAt(img, 10, 250); abs(r-int(paperGrey.R)) > 6 || abs(g-int(paperGrey.G)) > 6 || abs(b-int(paperGrey.B)) > 6 {
		t.Errorf("thumb area → %d,%d,%d, want paper", r, g, b)
	}
	if r, g, b := rgbAt(img, 220, 200); r != int(skin.R) || g != int(skin.G) || b != int(skin.B) {
		t.Errorf("interior skin tone was modified: %d,%d,%d", r, g, b)
	}
}

func TestRemovePunchHolesAlignedMarginDiscsOnly(t *testing.T) {
	t.Parallel()
	const w, h = 600, 800
	img := page(w, h, paperGrey)
	hole := color.RGBA{45, 45, 50, 255}
	for _, y := range []float64{200, 400, 600} {
		disc(img, 32, y, 9, hole) // binder holes, 3% of width, left margin
	}
	disc(img, 570, 300, 9, hole) // a lone dot on the right margin — no partner
	// An "o" in the body text.
	disc(img, 300, 300, 8, ink)
	disc(img, 300, 300, 5, paperGrey)
	n := RemovePunchHoles(img)
	if n != 3 {
		t.Fatalf("filled %d, want the 3 aligned binder holes", n)
	}
	if v := lumaAt(img, 32, 400); v < 200 {
		t.Errorf("hole centre luma %d, want filled with paper", v)
	}
	if v := lumaAt(img, 570, 300); v > 80 {
		t.Errorf("lone margin dot was filled (luma %d)", v)
	}
	if v := lumaAt(img, 292, 300); v > 80 {
		t.Errorf("body-text 'o' was touched (luma %d)", v)
	}
}

func TestRemovePunchHolesIgnoresDarkPages(t *testing.T) {
	t.Parallel()
	img := page(300, 400, color.RGBA{40, 40, 40, 255})
	if n := RemovePunchHoles(img); n != 0 {
		t.Errorf("dark page: filled %d", n)
	}
}

func TestSuppressGlare(t *testing.T) {
	t.Parallel()
	dim := color.RGBA{196, 194, 188, 255} // paper as a phone photo exposes it
	img := page(400, 400, dim)
	disc(img, 200, 200, 35, color.RGBA{255, 255, 255, 255})
	img.SetRGBA(200, 200, ink) // a fragment of text that survived the glare
	if n := SuppressGlare(img); n != 1 {
		t.Fatalf("suppressed %d regions, want 1", n)
	}
	if v := lumaAt(img, 215, 200); v > 205 {
		t.Errorf("glare luma %d, want flattened to paper (~193)", v)
	}
	if v := lumaAt(img, 200, 200); v > 60 {
		t.Errorf("surviving ink was overwritten (luma %d)", v)
	}

	// A well-exposed white page with a small white mark is NOT glare.
	bright := page(400, 400, color.RGBA{250, 250, 250, 255})
	disc(bright, 200, 200, 30, color.RGBA{255, 255, 255, 255})
	if n := SuppressGlare(bright); n != 0 {
		t.Errorf("bright page: suppressed %d, want 0", n)
	}
}

func TestCleanupOrderAndNoOpOnCleanPages(t *testing.T) {
	t.Parallel()
	clean := page(300, 400, paperGrey)
	out, rep := Cleanup(clean, CleanupOptions{Fingers: true, Holes: true, Glare: true})
	if rep != (CleanupReport{}) {
		t.Errorf("clean page report %+v, want nothing found", rep)
	}
	if r, g, b := rgbAt(out, 150, 200); r != int(paperGrey.R) || g != int(paperGrey.G) || b != int(paperGrey.B) {
		t.Error("region passes must not change a clean page")
	}
	if !(CleanupOptions{Holes: true}).Any() || (CleanupOptions{}).Any() {
		t.Error("Any() wrong")
	}
	bg, _ := Cleanup(clean, CleanupOptions{Background: true, Shadows: true})
	if r, _, _ := rgbAt(bg, 150, 200); r != 255 {
		t.Errorf("Background must win over Shadows (got r=%d)", r)
	}
	if &out.Pix[0] == &clean.Pix[0] {
		t.Error("Cleanup must not alias its input")
	}
}

func TestAutoCleanStretchesLevels(t *testing.T) {
	t.Parallel()
	img := page(200, 200, color.RGBA{150, 150, 150, 255})
	ellipse(img, 100, 100, 60, 8, color.RGBA{110, 110, 110, 255})
	out := AutoClean(img)
	if v := lumaAt(out, 100, 100); v > 120 {
		t.Errorf("ink after auto %d, want darker contrast", v)
	}
	if v := lumaAt(out, 10, 10); v < 240 {
		t.Errorf("paper after auto %d, want near white", v)
	}
}
