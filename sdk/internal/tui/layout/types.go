package layout

type UnitType int
type JustifyContent int
type AlignItems int
type Direction int

// Each enum gets its own const block so iota restarts at 0 for every type.
// Sharing one block does not reset iota, which would give these constants
// surprising, non-zero-based values (e.g. Column would be 11, not 0).
const (
	UnitPx UnitType = iota
	UnitPercent
	UnitGrow
)

const (
	JCenter JustifyContent = iota
	JLeft
	JRight
)

const (
	ACenter AlignItems = iota
	ATop
	ABottom
	ALeft
	ARight
)

const (
	Column Direction = iota
	Row
)

type Unit struct {
	Type  UnitType
	Value float64
}

func (u Unit) Resolve(total int) int {
	switch u.Type {
	case UnitPx:
		return int(u.Value)
	case UnitPercent:
		return int(float64(total) * u.Value / 100.0)
	case UnitGrow:
		return 0
	}
	return 0
}

type Padding struct {
	Top    Unit
	Bottom Unit
	Left   Unit
	Right  Unit
}
