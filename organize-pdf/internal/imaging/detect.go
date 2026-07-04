package imaging

import (
	"image"
	"math"
	"sort"
)

// Detection pipeline tuning constants.
const (
	detectMaxDim        = 500  // working resolution for detection
	gaussianSigma       = 1.4  // blur strength
	gradientPercentile  = 0.85 // edge-map threshold percentile
	houghThetaSteps     = 180  // 1° angular resolution
	houghRhoBin         = 2.0  // px per ρ bin
	minQuadAreaRatio    = 0.20 // accepted document must cover ≥20% of frame
	cornerAngleTolDeg   = 50.0 // rectangularity scoring span around 90°
	lineSeparationRatio = 0.25 // opposite sides must differ in ρ by ≥25% of dim
)

// DetectDocumentQuad locates the document quadrilateral in src. It never
// fails hard: whenever any stage is inconclusive it returns the full-image
// quad with confidence 0. Returned corners are normalized 0..1.
//
// Pipeline: downscale → grayscale → Gaussian blur → Sobel gradient →
// percentile threshold + dilation → Hough lines → best horizontal/vertical
// pairs → intersect → validate.
func DetectDocumentQuad(src image.Image) (Quad, float64) {
	small, _ := DownscaleTo(src, detectMaxDim)
	gray := Grayscale(small)
	w, h := gray.Bounds().Dx(), gray.Bounds().Dy()
	if w < 32 || h < 32 {
		return FullImageQuad(), 0
	}

	blurred := gaussianBlur(gray)
	mag := sobelMagnitude(blurred)
	edges := thresholdPercentile(mag, w, h, gradientPercentile)
	dilate3x3(edges, w, h)

	lines := houghLines(edges, w, h)
	hPair, vPair, ok := selectSidePairs(lines, w, h)
	if !ok {
		return FullImageQuad(), 0
	}

	pts, ok := intersectSides(hPair, vPair)
	if !ok {
		return FullImageQuad(), 0
	}

	quad := OrderCorners(pts).Normalize(w, h).Clamp01()
	if err := quad.Validate(); err != nil {
		return FullImageQuad(), 0
	}
	area := quad.Area()
	if area < minQuadAreaRatio {
		return FullImageQuad(), 0
	}

	confidence := scoreQuad(quad, area, hPair, vPair, w, h)
	return quad, confidence
}

// ---- blur ----

// gaussianBlur applies a 5-tap separable Gaussian (σ≈1.4) in two passes.
func gaussianBlur(g *image.Gray) *image.Gray {
	// Discrete 5-tap kernel for σ≈1.4: [1 4 6 4 1] normalized by 16 is σ≈1.0;
	// use [2 4 5 4 2]/17 as a slightly wider approximation.
	kernel := [5]int{2, 4, 5, 4, 2}
	const kSum = 17

	w, h := g.Bounds().Dx(), g.Bounds().Dy()
	tmp := image.NewGray(image.Rect(0, 0, w, h))
	out := image.NewGray(image.Rect(0, 0, w, h))

	clampX := func(x int) int {
		if x < 0 {
			return 0
		}
		if x > w-1 {
			return w - 1
		}
		return x
	}
	clampY := func(y int) int {
		if y < 0 {
			return 0
		}
		if y > h-1 {
			return h - 1
		}
		return y
	}

	// Horizontal pass.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum := 0
			for k := -2; k <= 2; k++ {
				sum += kernel[k+2] * int(g.Pix[y*g.Stride+clampX(x+k)])
			}
			tmp.Pix[y*tmp.Stride+x] = uint8(sum / kSum)
		}
	}
	// Vertical pass.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum := 0
			for k := -2; k <= 2; k++ {
				sum += kernel[k+2] * int(tmp.Pix[clampY(y+k)*tmp.Stride+x])
			}
			out.Pix[y*out.Stride+x] = uint8(sum / kSum)
		}
	}
	return out
}

// ---- gradient ----

// sobelMagnitude computes the 3×3 Sobel gradient magnitude per pixel.
func sobelMagnitude(g *image.Gray) []int {
	w, h := g.Bounds().Dx(), g.Bounds().Dy()
	mag := make([]int, w*h)
	at := func(x, y int) int {
		if x < 0 {
			x = 0
		} else if x > w-1 {
			x = w - 1
		}
		if y < 0 {
			y = 0
		} else if y > h-1 {
			y = h - 1
		}
		return int(g.Pix[y*g.Stride+x])
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gx := -at(x-1, y-1) - 2*at(x-1, y) - at(x-1, y+1) +
				at(x+1, y-1) + 2*at(x+1, y) + at(x+1, y+1)
			gy := -at(x-1, y-1) - 2*at(x, y-1) - at(x+1, y-1) +
				at(x-1, y+1) + 2*at(x, y+1) + at(x+1, y+1)
			if gx < 0 {
				gx = -gx
			}
			if gy < 0 {
				gy = -gy
			}
			mag[y*w+x] = gx + gy // L1 magnitude — cheaper, same ordering
		}
	}
	return mag
}

// thresholdPercentile binarizes the magnitude map at the given percentile
// using a 256-bin histogram over the clamped magnitude range.
func thresholdPercentile(mag []int, w, h int, percentile float64) []bool {
	maxVal := 1
	for _, v := range mag {
		if v > maxVal {
			maxVal = v
		}
	}
	var hist [256]int
	for _, v := range mag {
		bin := v * 255 / maxVal
		hist[bin]++
	}
	target := int(float64(w*h) * percentile)
	cum, threshBin := 0, 255
	for i := 0; i < 256; i++ {
		cum += hist[i]
		if cum >= target {
			threshBin = i
			break
		}
	}
	thresh := threshBin * maxVal / 255
	if thresh < 1 {
		thresh = 1
	}

	edges := make([]bool, w*h)
	for i, v := range mag {
		edges[i] = v >= thresh
	}
	return edges
}

// dilate3x3 performs one in-place-ish pass of 3×3 binary dilation.
func dilate3x3(edges []bool, w, h int) {
	src := make([]bool, len(edges))
	copy(src, edges)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if src[y*w+x] {
				continue
			}
			found := false
			for dy := -1; dy <= 1 && !found; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx >= 0 && nx < w && ny >= 0 && ny < h && src[ny*w+nx] {
						found = true
						break
					}
				}
			}
			edges[y*w+x] = found
		}
	}
}

// ---- Hough transform ----

type houghLine struct {
	rho   float64 // signed distance from origin
	theta float64 // radians, [0, π)
	votes int
}

// houghLines runs a standard ρ-θ Hough transform over the edge map and
// returns NMS-filtered peaks sorted by votes descending.
func houghLines(edges []bool, w, h int) []houghLine {
	diag := math.Hypot(float64(w), float64(h))
	nRho := int(2*diag/houghRhoBin) + 1
	nTheta := houghThetaSteps

	sinT := make([]float64, nTheta)
	cosT := make([]float64, nTheta)
	for t := 0; t < nTheta; t++ {
		angle := float64(t) * math.Pi / float64(nTheta)
		sinT[t] = math.Sin(angle)
		cosT[t] = math.Cos(angle)
	}

	acc := make([]int32, nRho*nTheta)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !edges[y*w+x] {
				continue
			}
			fx, fy := float64(x), float64(y)
			for t := 0; t < nTheta; t++ {
				rho := fx*cosT[t] + fy*sinT[t]
				rBin := int((rho + diag) / houghRhoBin)
				if rBin >= 0 && rBin < nRho {
					acc[rBin*nTheta+t]++
				}
			}
		}
	}

	minDim := w
	if h < minDim {
		minDim = h
	}
	minVotes := int32(float64(minDim) * 0.25)

	// Peak extraction with 3×3 neighborhood NMS in accumulator space.
	var peaks []houghLine
	for r := 0; r < nRho; r++ {
		for t := 0; t < nTheta; t++ {
			v := acc[r*nTheta+t]
			if v < minVotes {
				continue
			}
			isPeak := true
			for dr := -1; dr <= 1 && isPeak; dr++ {
				for dt := -1; dt <= 1; dt++ {
					if dr == 0 && dt == 0 {
						continue
					}
					nr, nt := r+dr, (t+dt+nTheta)%nTheta
					if nr < 0 || nr >= nRho {
						continue
					}
					nv := acc[nr*nTheta+nt]
					if nv > v || (nv == v && (dr < 0 || (dr == 0 && dt < 0))) {
						isPeak = false
						break
					}
				}
			}
			if isPeak {
				peaks = append(peaks, houghLine{
					rho:   float64(r)*houghRhoBin - diag,
					theta: float64(t) * math.Pi / float64(nTheta),
					votes: int(v),
				})
			}
		}
	}

	sort.Slice(peaks, func(i, j int) bool { return peaks[i].votes > peaks[j].votes })
	return peaks
}

// selectSidePairs buckets lines into near-horizontal and near-vertical and
// picks the two strongest per bucket that are far enough apart to be
// opposite document sides.
func selectSidePairs(lines []houghLine, w, h int) (hPair, vPair [2]houghLine, ok bool) {
	// θ≈90° (π/2) is horizontal (line normal points vertically); θ≈0 or π is vertical.
	const tolDeg = 35.0
	tol := tolDeg * math.Pi / 180

	var horiz, vert []houghLine
	for _, l := range lines {
		dTo90 := math.Abs(l.theta - math.Pi/2)
		if dTo90 < tol {
			horiz = append(horiz, l)
		} else if l.theta < tol || l.theta > math.Pi-tol {
			vert = append(vert, l)
		}
	}

	pick := func(bucket []houghLine, minSep float64) ([2]houghLine, bool) {
		for i := 0; i < len(bucket); i++ {
			for j := i + 1; j < len(bucket); j++ {
				// Compare ρ with theta-wraparound care: near-vertical lines can
				// have θ≈0 and θ≈π with opposite ρ signs describing nearby lines.
				ri, rj := bucket[i].rho, bucket[j].rho
				if math.Abs(bucket[i].theta-bucket[j].theta) > math.Pi/2 {
					rj = -rj
				}
				if math.Abs(ri-rj) >= minSep {
					return [2]houghLine{bucket[i], bucket[j]}, true
				}
			}
		}
		return [2]houghLine{}, false
	}

	hPair, hOK := pick(horiz, lineSeparationRatio*float64(h))
	vPair, vOK := pick(vert, lineSeparationRatio*float64(w))
	return hPair, vPair, hOK && vOK
}

// intersectSides intersects each horizontal side with each vertical side,
// yielding the four corner candidates.
func intersectSides(hPair, vPair [2]houghLine) ([4]Point, bool) {
	var pts [4]Point
	idx := 0
	for _, lh := range hPair {
		for _, lv := range vPair {
			p, ok := intersect(lh, lv)
			if !ok {
				return pts, false
			}
			pts[idx] = p
			idx++
		}
	}
	return pts, true
}

// intersect solves the 2×2 system for two ρ-θ lines.
func intersect(a, b houghLine) (Point, bool) {
	// x·cosθ + y·sinθ = ρ for each line.
	a1, b1, c1 := math.Cos(a.theta), math.Sin(a.theta), a.rho
	a2, b2, c2 := math.Cos(b.theta), math.Sin(b.theta), b.rho
	det := a1*b2 - a2*b1
	if math.Abs(det) < 1e-6 {
		return Point{}, false
	}
	return Point{
		X: (c1*b2 - c2*b1) / det,
		Y: (a1*c2 - a2*c1) / det,
	}, true
}

// scoreQuad computes a 0..1 confidence from Hough vote strength, area ratio,
// and rectangularity.
func scoreQuad(quad Quad, area float64, hPair, vPair [2]houghLine, w, h int) float64 {
	minDim := w
	if h < minDim {
		minDim = h
	}

	// Vote strength: mean votes normalized against the shorter dimension
	// (a full-width edge line would collect ≈minDim votes).
	totalVotes := hPair[0].votes + hPair[1].votes + vPair[0].votes + vPair[1].votes
	voteScore := float64(totalVotes) / 4 / float64(minDim)
	if voteScore > 1 {
		voteScore = 1
	}

	// Area: ramp 0.2 → 0.6 maps to 0 → 1.
	areaScore := (area - 0.2) / 0.4
	if areaScore < 0 {
		areaScore = 0
	}
	if areaScore > 1 {
		areaScore = 1
	}

	// Rectangularity: mean deviation of corner angles from 90°.
	angles := quad.cornerAngles()
	dev := 0.0
	for _, a := range angles {
		dev += math.Abs(a - 90)
	}
	dev /= 4
	rectScore := 1 - dev/cornerAngleTolDeg
	if rectScore < 0 {
		rectScore = 0
	}

	conf := 0.5*voteScore + 0.25*areaScore + 0.25*rectScore
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	return conf
}
