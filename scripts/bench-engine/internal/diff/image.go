// Package diff holds the differential checks of the engine benchmark harness:
// scores that compare a tool's output with a reference (an oracle's output or
// the input) so an engine change is judged by what it produces, not only by how
// fast it runs (docs/developer/architecture/next-generation-document-engine/
// 16-benchmarking-and-testing.md §5.6).
//
// The image metrics are implemented here in pure Go on image.Gray rather than
// by calling an image library: both are short, well-specified algorithms, and
// the harness must not add a cgo or runtime dependency to the test image. Both
// sides are rasterised by the same pdftoppm, so a score measures the PDF, not
// the rasteriser.
package diff

import (
	"errors"
	"image"
	"math"
	"math/bits"
	"sort"

	"golang.org/x/image/draw"
)

// ErrSizeMismatch is returned when two rasters differ in size. Pages rendered
// at the same DPI from equal page boxes have equal sizes, so a mismatch is a
// finding in itself (the page box changed), not something to resample away.
var ErrSizeMismatch = errors.New("diff: images differ in size")

const (
	// ssimWindow is the side of the square window SSIM is computed over.
	ssimWindow = 8
	// ssimStride is the step between windows; half the window keeps the cost
	// low while every pixel still falls in at least one window.
	ssimStride = 4
)

// SSIM returns the mean structural similarity of two equal-size greyscale
// images: 1.0 for identical images, lower as structure diverges. Windows are
// ssimWindow×ssimWindow at stride ssimStride with the standard constants
// (K1 = 0.01, K2 = 0.03, L = 255). Images smaller than one window are compared
// as a single window.
func SSIM(a, b *image.Gray) (float64, error) {
	ra, rb := a.Bounds(), b.Bounds()
	if ra.Dx() != rb.Dx() || ra.Dy() != rb.Dy() {
		return 0, ErrSizeMismatch
	}
	w, h := ra.Dx(), ra.Dy()
	if w == 0 || h == 0 {
		return 0, errors.New("diff: empty image")
	}
	win := ssimWindow
	if w < win || h < win {
		win = min(w, h)
	}
	const c1 = (0.01 * 255) * (0.01 * 255)
	const c2 = (0.03 * 255) * (0.03 * 255)
	var sum float64
	var n int
	for y := 0; y+win <= h; y += ssimStride {
		for x := 0; x+win <= w; x += ssimStride {
			var ma, mb float64
			for dy := 0; dy < win; dy++ {
				for dx := 0; dx < win; dx++ {
					ma += float64(a.GrayAt(ra.Min.X+x+dx, ra.Min.Y+y+dy).Y)
					mb += float64(b.GrayAt(rb.Min.X+x+dx, rb.Min.Y+y+dy).Y)
				}
			}
			k := float64(win * win)
			ma /= k
			mb /= k
			var va, vb, cov float64
			for dy := 0; dy < win; dy++ {
				for dx := 0; dx < win; dx++ {
					pa := float64(a.GrayAt(ra.Min.X+x+dx, ra.Min.Y+y+dy).Y) - ma
					pb := float64(b.GrayAt(rb.Min.X+x+dx, rb.Min.Y+y+dy).Y) - mb
					va += pa * pa
					vb += pb * pb
					cov += pa * pb
				}
			}
			va /= k - 1
			vb /= k - 1
			cov /= k - 1
			sum += ((2*ma*mb + c1) * (2*cov + c2)) / ((ma*ma + mb*mb + c1) * (va + vb + c2))
			n++
			if win < ssimWindow {
				break
			}
		}
		if win < ssimWindow {
			break
		}
	}
	return sum / float64(n), nil
}

// PHash returns the 64-bit DCT perceptual hash of img: the image is scaled to
// 32×32 grey, a 2D DCT-II is taken, and each of the 8×8 lowest frequencies
// (the DC term excluded from the median) becomes a bit set when it is above the
// median. Similar images give hashes a small Hamming distance apart.
func PHash(img image.Image) uint64 {
	const n = 32
	small := image.NewGray(image.Rect(0, 0, n, n))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)
	var px [n][n]float64
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			px[y][x] = float64(small.GrayAt(x, y).Y)
		}
	}
	var coef [8][8]float64
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			var s float64
			for y := 0; y < n; y++ {
				for x := 0; x < n; x++ {
					s += px[y][x] *
						math.Cos(float64((2*x+1)*u)*math.Pi/(2*n)) *
						math.Cos(float64((2*y+1)*v)*math.Pi/(2*n))
				}
			}
			coef[v][u] = s
		}
	}
	vals := make([]float64, 0, 63)
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			if u == 0 && v == 0 {
				continue
			}
			vals = append(vals, coef[v][u])
		}
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	var h uint64
	for i, c := range vals {
		if c > median {
			h |= 1 << uint(i)
		}
	}
	return h
}

// Hamming returns the number of differing bits between two hashes.
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }
