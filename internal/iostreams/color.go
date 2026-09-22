package iostreams

// Palette writes ANSI attributes, or nothing at all when color is off. Styles
// are a table rather than one method per code, so adding a style is data.
type Palette struct{ enabled bool }

// Style names one visual attribute.
type Style string

// The styles dbmap uses. Anything a command needs is listed here.
const (
	Bold   Style = "bold"
	Red    Style = "red"
	Green  Style = "green"
	Yellow Style = "yellow"
	Gray   Style = "gray"
)

var codes = map[Style]string{
	Bold:   "\x1b[1m",
	Red:    "\x1b[31m",
	Green:  "\x1b[32m",
	Yellow: "\x1b[33m",
	Gray:   "\x1b[90m",
}

const reset = "\x1b[0m"

// Enabled reports whether this palette emits escape sequences.
func (p *Palette) Enabled() bool { return p.enabled }

// Apply wraps text in a style, or returns it untouched when color is off or the
// style is unknown.
func (p *Palette) Apply(s Style, text string) string {
	code, ok := codes[s]
	if !p.enabled || !ok {
		return text
	}
	return code + text + reset
}
