package imaging

import (
	"image"
	"math"
)

// Region detection (fingers, holes, glare) runs on a reduced copy and the
// fill is applied at full resolution through the scaled mask: labelling a
// 12 MP mask would cost ~50 MB of labels for no accuracy the fill needs.
const cleanupWorkMaxDim = 1024

// CleanupOptions selects classical clean-up passes for one scanned page.
type CleanupOptions struct {
	Shadows    bool // divide out uneven lighting (deep shadow removal)
	Background bool // paper → pure white after normalization (implies Shadows)
	Glare      bool // flatten blown-out specular highlights to paper tone
	Fingers    bool // fill skin-coloured regions touching the page border
	Holes      bool // fill punch holes in the left/right margins
	Auto       bool // normalization + levels stretch (the server-side "auto")
}

// Any reports whether at least one pass is enabled.
func (o CleanupOptions) Any() bool {
	return o.Shadows || o.Background || o.Glare || o.Fingers || o.Holes || o.Auto
}

// CleanupReport counts what each pass changed — the worker turns a non-zero
// Glare count into a user-facing "re-shoot at an angle" warning, because
// text under a blown-out highlight is unrecoverable.
type CleanupReport struct {
	Fingers int
	Holes   int
	Glare   int
}

// Cleanup runs the enabled passes in a fixed order: region fills first
// (fingers, holes, glare — they need the original colours to detect), then
// one tone pass (background ⊃ auto ⊃ shadows). Returns a new image.
func Cleanup(src image.Image, o CleanupOptions) (*image.RGBA, CleanupReport) {
	img := toRGBA(src)
	var rep CleanupReport
	if o.Fingers {
		rep.Fingers = RemoveFingers(img)
	}
	if o.Holes {
		rep.Holes = RemovePunchHoles(img)
	}
	if o.Glare {
		rep.Glare = SuppressGlare(img)
	}
	switch {
	case o.Background:
		img = CleanBackground(img)
	case o.Auto:
		img = AutoClean(img)
	case o.Shadows:
		img = RemoveShadows(img)
	}
	return img, rep
}

// RemoveShadows normalizes illumination (see EstimateBackground).
func RemoveShadows(src image.Image) *image.RGBA {
	return NormalizeIllumination(src, EstimateBackground(src))
}

// CleanBackground normalizes illumination, then pushes near-paper pixels to
// pure white with a soft ramp (no hard halo at ink edges). Ink — anything
// clearly darker than paper in any channel — keeps its colour. This is
// DOCUMENT background removal, not object segmentation.
func CleanBackground(src image.Image) *image.RGBA {
	out := RemoveShadows(src)
	const lo, hi = 200, 228 // ramp on the darkest channel
	for i := 0; i+3 < len(out.Pix); i += 4 {
		m := min(out.Pix[i], out.Pix[i+1], out.Pix[i+2])
		if m <= lo {
			continue
		}
		t := 1.0
		if m < hi {
			t = float64(m-lo) / float64(hi-lo)
		}
		for c := 0; c < 3; c++ {
			v := float64(out.Pix[i+c])
			out.Pix[i+c] = uint8(v + (255-v)*t)
		}
	}
	return out
}

// AutoClean is the server-side "auto": normalization plus a luma levels
// stretch (1st → 99th percentile), for pages built without on-device Skia.
func AutoClean(src image.Image) *image.RGBA {
	out := RemoveShadows(src)
	lo := float64(lumaPercentile(out, 0.01))
	hi := float64(lumaPercentile(out, 0.99))
	if hi-lo < 32 {
		return out // nearly flat page: stretching would amplify noise
	}
	scale := 255 / (hi - lo)
	for i := 0; i+3 < len(out.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			v := (float64(out.Pix[i+c]) - lo) * scale
			out.Pix[i+c] = uint8(math.Max(0, math.Min(255, v)))
		}
	}
	return out
}

// workMask is a reduced-resolution view used for region detection.
type workMask struct {
	w, h  int
	scale float64 // work px per full px
	small *image.RGBA
}

// newWorkMask downscales img for detection.
func newWorkMask(img *image.RGBA) workMask {
	small, scale := DownscaleTo(img, cleanupWorkMaxDim)
	b := small.Bounds()
	return workMask{w: b.Dx(), h: b.Dy(), scale: scale, small: small}
}

// at returns the work-resolution RGB at (x, y).
func (m workMask) at(x, y int) (uint8, uint8, uint8) {
	i := (y*m.w + x) * 4
	return m.small.Pix[i], m.small.Pix[i+1], m.small.Pix[i+2]
}

// paperColorAround is the median colour of the brighter half of the pixels
// in a ring around comp (bbox grown by 25%), excluding the region itself.
// "Brighter half" skips ink in the ring, so the fill is paper, never text.
func (m workMask) paperColorAround(labels []int32, comp Component) (uint8, uint8, uint8) {
	gx := max(2, comp.Width()/4)
	gy := max(2, comp.Height()/4)
	var rs, gs, bs, ls []uint8
	for y := max(0, comp.MinY-gy); y <= min(m.h-1, comp.MaxY+gy); y++ {
		for x := max(0, comp.MinX-gx); x <= min(m.w-1, comp.MaxX+gx); x++ {
			if labels[y*m.w+x] != 0 {
				continue
			}
			r, g, b := m.at(x, y)
			rs, gs, bs, ls = append(rs, r), append(gs, g), append(bs, b), append(ls, luma(r, g, b))
		}
	}
	if len(ls) == 0 {
		return 255, 255, 255
	}
	cut := medianUint8(append([]uint8(nil), ls...))
	var br, bg, bb []uint8
	for i := range ls {
		if ls[i] >= cut {
			br, bg, bb = append(br, rs[i]), append(bg, gs[i]), append(bb, bs[i])
		}
	}
	return medianUint8(br), medianUint8(bg), medianUint8(bb)
}

// fillRegions paints the selected components' pixels in the full-resolution
// image with their fill colours. Pixels next to a region at work resolution
// (a 1-px ring) get a 50% blend, so the fill has no hard edge. With
// featherBrighterOnly (glare), the ring blends only pixels BRIGHTER than the
// fill: a fragment of ink inside a highlight is data and must not be faded.
func fillRegions(img *image.RGBA, m workMask, labels []int32, fills map[int32][3]uint8, featherBrighterOnly bool) {
	if len(fills) == 0 {
		return
	}
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	for y := 0; y < H; y++ {
		sy := min(m.h-1, int(float64(y)*m.scale))
		for x := 0; x < W; x++ {
			sx := min(m.w-1, int(float64(x)*m.scale))
			l := labels[sy*m.w+sx]
			alpha := 1.0
			if _, ok := fills[l]; !ok {
				l = 0
				for _, q := range [4][2]int{{sx - 1, sy}, {sx + 1, sy}, {sx, sy - 1}, {sx, sy + 1}} {
					if q[0] < 0 || q[1] < 0 || q[0] >= m.w || q[1] >= m.h {
						continue
					}
					if n := labels[q[1]*m.w+q[0]]; n != 0 {
						if _, ok := fills[n]; ok {
							l, alpha = n, 0.5
							break
						}
					}
				}
				if l == 0 {
					continue
				}
			}
			col := fills[l]
			i := (y*W + x) * 4
			if alpha < 1 && featherBrighterOnly && luma(img.Pix[i], img.Pix[i+1], img.Pix[i+2]) <= luma(col[0], col[1], col[2]) {
				continue
			}
			for c := 0; c < 3; c++ {
				v := float64(img.Pix[i+c])
				img.Pix[i+c] = uint8(v + (float64(col[c])-v)*alpha)
			}
		}
	}
}

// isSkin is the classic Chai & Ngan YCbCr skin range.
func isSkin(r, g, b uint8) bool {
	R, G, B := float64(r), float64(g), float64(b)
	cb := 128 - 0.168736*R - 0.331264*G + 0.5*B
	cr := 128 + 0.5*R - 0.418688*G - 0.081312*B
	return cb >= 77 && cb <= 127 && cr >= 133 && cr <= 173
}

// RemoveFingers fills skin-coloured regions that TOUCH THE PAGE BORDER and
// cover 0.2–15% of it — fingers holding the page's margin. Skin tones in the
// page's interior (a photo of a face) are never touched. Mutates img;
// returns how many regions were filled.
func RemoveFingers(img *image.RGBA) int {
	m := newWorkMask(img)
	mask := make([]bool, m.w*m.h)
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			mask[y*m.w+x] = isSkin(m.at(x, y))
		}
	}
	total := m.w * m.h
	labels, comps := LabelComponents(mask, m.w, m.h, total/500)
	fills := map[int32][3]uint8{}
	for _, c := range comps {
		if !c.TouchesBorder || c.Area > total*15/100 {
			continue
		}
		r, g, b := m.paperColorAround(labels, c)
		fills[c.Label] = [3]uint8{r, g, b}
	}
	fillRegions(img, m, labels, fills, false)
	return len(fills)
}

// RemovePunchHoles fills disc-shaped regions in the left/right 12% margins
// that differ from the paper, are 2–6% of the page width across, roughly
// round (fill ratio ≈ π/4, near-square bbox), and line up vertically with at
// least one other such disc — the signature of binder holes. A lone dot, an
// "o" (a ring, low fill ratio) or body text never qualifies. A hole showing a
// paper-white table is indistinguishable from paper and is left as is.
func RemovePunchHoles(img *image.RGBA) int {
	m := newWorkMask(img)
	paper := lumaPercentile(m.small, 0.75)
	if paper < 80 {
		return 0 // no recognisable paper (a dark page) — don't guess
	}
	margin := m.w * 12 / 100
	mask := make([]bool, m.w*m.h)
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			if x > margin && x < m.w-1-margin {
				continue
			}
			r, g, b := m.at(x, y)
			l := luma(r, g, b)
			chroma := int(max(r, g, b)) - int(min(r, g, b))
			mask[y*m.w+x] = int(l) < int(paper)*70/100 || chroma > 60
		}
	}
	labels, comps := LabelComponents(mask, m.w, m.h, 4)
	minD, maxD := float64(m.w)*0.02, float64(m.w)*0.06
	var cands []Component
	for _, c := range comps {
		wd, ht := float64(c.Width()), float64(c.Height())
		aspect := wd / ht
		if wd < minD || wd > maxD || aspect < 0.75 || aspect > 1.33 {
			continue
		}
		if fr := c.FillRatio(); fr < 0.62 || fr > 0.92 {
			continue
		}
		cands = append(cands, c)
	}
	fills := map[int32][3]uint8{}
	tol := float64(m.w) * 0.02
	for i, a := range cands {
		for j, b := range cands {
			if i != j && math.Abs(a.CentroidX()-b.CentroidX()) <= tol {
				r, g, bl := m.paperColorAround(labels, a)
				fills[a.Label] = [3]uint8{r, g, bl}
				break
			}
		}
	}
	fillRegions(img, m, labels, fills, false)
	return len(fills)
}

// SuppressGlare flattens specular highlights: blown-out (luma ≥ 250),
// colourless regions of 0.05–10% of the page whose surrounding paper is
// clearly darker (ring median < 235) — so an already-white, well-exposed
// page is never mistaken for one big highlight. Only the saturated pixels
// are replaced, with the surrounding paper tone; any ink that survived the
// glare stays. Text the glare fully blew out cannot be recovered.
func SuppressGlare(img *image.RGBA) int {
	m := newWorkMask(img)
	mask := make([]bool, m.w*m.h)
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			r, g, b := m.at(x, y)
			mask[y*m.w+x] = luma(r, g, b) >= 250 && int(max(r, g, b))-int(min(r, g, b)) <= 14
		}
	}
	total := m.w * m.h
	labels, comps := LabelComponents(mask, m.w, m.h, max(4, total/2000))
	fills := map[int32][3]uint8{}
	for _, c := range comps {
		if c.Area > total/10 {
			continue
		}
		r, g, b := m.paperColorAround(labels, c)
		if luma(r, g, b) >= 235 {
			continue
		}
		fills[c.Label] = [3]uint8{r, g, b}
	}
	fillRegions(img, m, labels, fills, true)
	return len(fills)
}
