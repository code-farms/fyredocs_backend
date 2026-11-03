package imaging

// Component is one 4-connected region of a binary mask.
type Component struct {
	Label                  int32
	Area                   int
	MinX, MinY, MaxX, MaxY int
	// SumX/SumY give the centroid as Sum/Area.
	SumX, SumY    int
	TouchesBorder bool
}

// Width is the component's bounding-box width.
func (c Component) Width() int { return c.MaxX - c.MinX + 1 }

// Height is the component's bounding-box height.
func (c Component) Height() int { return c.MaxY - c.MinY + 1 }

// CentroidX is the x of the component's centre of mass.
func (c Component) CentroidX() float64 { return float64(c.SumX) / float64(c.Area) }

// CentroidY is the y of the component's centre of mass.
func (c Component) CentroidY() float64 { return float64(c.SumY) / float64(c.Area) }

// FillRatio is area / bounding-box area: ≈π/4 for a disc, 1 for a filled
// rectangle, low for rings and strokes (an "o", a line of text).
func (c Component) FillRatio() float64 {
	return float64(c.Area) / float64(c.Width()*c.Height())
}

// LabelComponents labels the 4-connected true regions of mask (row-major,
// w×h) and returns the label map (0 = background, i+1 = comps[i]) and the
// components with area ≥ minArea. Smaller regions are labelled 0. Iterative
// flood fill with an explicit stack — no recursion depth limit on big masks.
func LabelComponents(mask []bool, w, h, minArea int) ([]int32, []Component) {
	labels := make([]int32, w*h)
	var comps []Component
	stack := make([]int, 0, 1024)
	var next int32 = 1
	for start := range mask {
		if !mask[start] || labels[start] != 0 {
			continue
		}
		c := Component{Label: next, MinX: w, MinY: h, MaxX: -1, MaxY: -1}
		members := make([]int, 0, 64)
		stack = append(stack[:0], start)
		labels[start] = next
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			members = append(members, p)
			x, y := p%w, p/w
			c.Area++
			c.SumX += x
			c.SumY += y
			c.MinX, c.MaxX = min(c.MinX, x), max(c.MaxX, x)
			c.MinY, c.MaxY = min(c.MinY, y), max(c.MaxY, y)
			if x == 0 || y == 0 || x == w-1 || y == h-1 {
				c.TouchesBorder = true
			}
			for _, q := range [4]int{p - 1, p + 1, p - w, p + w} {
				if q < 0 || q >= w*h || (q == p-1 && x == 0) || (q == p+1 && x == w-1) {
					continue
				}
				if mask[q] && labels[q] == 0 {
					labels[q] = next
					stack = append(stack, q)
				}
			}
		}
		if c.Area < minArea {
			for _, p := range members {
				labels[p] = -1 // visited, too small
			}
			continue
		}
		comps = append(comps, c)
		next++
	}
	for i, l := range labels {
		if l < 0 {
			labels[i] = 0
		}
	}
	return labels, comps
}
