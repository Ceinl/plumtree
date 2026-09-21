package main

// A cell fires once, then rests. Its trace is visual memory, not activity.
type cell struct {
	life, trace, ink uint8
}

type point struct{ x, y int }
type source struct {
	point
	ink uint8
}

type field struct {
	w, h    int
	cells   []cell
	next    []cell
	sources []source
	tick    int
	mode    int
}

var rules = [...]struct {
	name, note string
	rest       uint8
}{
	{"RIPPLE", "One signal is enough to begin.", 7},
	{"BRANCH", "Two signals meet. A new path opens.", 3},
	{"BLOOM", "A thought takes shape between its neighbours.", 5},
}

func newField(w, h int) field {
	f := field{w: w, h: h, cells: make([]cell, w*h), next: make([]cell, w*h)}
	for i, p := range []point{{w / 4, h / 2}, {w / 2, h / 3}, {3 * w / 4, 2 * h / 3}} {
		f.sources = append(f.sources, source{p, 1 << i})
	}
	f.pulse()
	return f
}

func (f *field) inside(p point) bool { return p.x >= 0 && p.x < f.w && p.y >= 0 && p.y < f.h }

// Resize preserves the overlapping field and removes sources outside it.
func (f *field) resize(w, h int) {
	cells := make([]cell, w*h)
	for y := 0; y < min(h, f.h); y++ {
		copy(cells[y*w:y*w+min(w, f.w)], f.cells[y*f.w:y*f.w+min(w, f.w)])
	}
	f.w, f.h, f.cells, f.next = w, h, cells, make([]cell, w*h)
	sources := f.sources[:0]
	for _, s := range f.sources {
		if f.inside(s.point) {
			sources = append(sources, s)
		}
	}
	f.sources = sources
}

func (f *field) toggle(p point) {
	if !f.inside(p) {
		return
	}
	for i, s := range f.sources {
		if s.point == p {
			f.sources = append(f.sources[:i], f.sources[i+1:]...)
			return
		}
	}
	if len(f.sources) < 24 {
		s := source{p, 1 << (len(f.sources) % 3)}
		f.sources = append(f.sources, s)
		f.ignite(s)
	}
}

// Each source emits a small asymmetric seed. Double buffering ensures every
// cell sees the same generation, independent of traversal order.
func (f *field) ignite(s source) {
	for _, d := range []point{{0, 0}, {1, 0}, {0, 1}} {
		p := point{s.x + d.x, s.y + d.y}
		if f.inside(p) {
			f.cells[p.y*f.w+p.x] = cell{life: 1, trace: 28, ink: s.ink}
		}
	}
}

func (f *field) pulse() {
	for _, s := range f.sources {
		f.ignite(s)
	}
}

func (f *field) step() {
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			i := y*f.w + x
			c := f.cells[i]
			if c.trace > 0 {
				c.trace--
			}
			switch {
			case c.life == 1:
				c.life = rules[f.mode].rest
			case c.life > 1:
				c.life--
				if c.life == 1 {
					c.life = 0
				}
			default:
				count, ink := 0, uint8(0)
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						p := point{x + dx, y + dy}
						if (dx == 0 && dy == 0) || !f.inside(p) {
							continue
						}
						n := f.cells[p.y*f.w+p.x]
						if n.life == 1 {
							count++
							ink |= n.ink
						}
					}
				}
				fire := count == 1
				if f.mode == 1 {
					fire = count == 2
				} else if f.mode == 2 {
					fire = count == 2 || count == 3
				}
				if fire {
					c = cell{life: 1, trace: 28, ink: ink}
				}
			}
			f.next[i] = c
		}
	}
	f.cells, f.next = f.next, f.cells
	f.tick++
	if f.tick%24 == 0 {
		f.pulse()
	}
}
