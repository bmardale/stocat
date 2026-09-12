package splash

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"unicode/utf8"
)

const cat = `
    /\_/\
   ( o.o )
    > ^ <
   /     \   )
  (  | |  ) (
   \_|_|_/_/
`

const title = `
███████╗████████╗ ██████╗  ██████╗ █████╗ ████████╗
██╔════╝╚══██╔══╝██╔═══██╗██╔════╝██╔══██╗╚══██╔══╝
███████╗   ██║   ██║   ██║██║     ███████║   ██║
╚════██║   ██║   ██║   ██║██║     ██╔══██║   ██║
███████║   ██║   ╚██████╔╝╚██████╗██║  ██║   ██║
╚══════╝   ╚═╝    ╚═════╝  ╚═════╝╚═╝  ╚═╝   ╚═╝
`

const (
	indent = 2
	gap    = 3
)

type profile int

const (
	noColor profile = iota
	ansi16
	ansi256
	trueColor
)

type color struct {
	r, g, b uint8
	basic   int
}

var (
	amber  = color{r: 0xff, g: 0xb8, b: 0x6c, basic: 33}
	green  = color{r: 0x50, g: 0xfa, b: 0x7b, basic: 92}
	pink   = color{r: 0xff, g: 0x79, b: 0xc6, basic: 95}
	purple = color{r: 0xbd, g: 0x93, b: 0xf9, basic: 94}
	cyan   = color{r: 0x8b, g: 0xe9, b: 0xfd, basic: 96}
	slate  = color{r: 0x62, g: 0x72, b: 0xa4, basic: 90}
)

// Print writes the banner to w. It adds color only when w is a terminal that supports color.
func Print(w io.Writer, version string) error {
	_, err := io.WriteString(w, render(detect(w, os.Getenv), version))
	return err
}

func detect(w io.Writer, getenv func(string) string) profile {
	f, ok := w.(*os.File)
	if !ok {
		return noColor
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return noColor
	}
	return profileFromEnv(getenv)
}

func profileFromEnv(getenv func(string) string) profile {
	term := getenv("TERM")
	switch {
	case getenv("NO_COLOR") != "", term == "dumb":
		return noColor
	case getenv("COLORTERM") == "truecolor", getenv("COLORTERM") == "24bit":
		return trueColor
	case strings.Contains(term, "256color"):
		return ansi256
	default:
		return ansi16
	}
}

func render(p profile, version string) string {
	catRows := lines(cat)
	titleRows := lines(title)
	catWidth := maxWidth(catRows)
	titleWidth := maxWidth(titleRows)

	var b strings.Builder
	pen := pen{b: &b, profile: p}
	pad := strings.Repeat(" ", indent)

	b.WriteByte('\n')
	for i := range max(len(catRows), len(titleRows)) {
		b.WriteString(pad)
		row := rowAt(catRows, i)
		for _, r := range row {
			pen.write(r, catColor(r))
		}
		b.WriteString(strings.Repeat(" ", catWidth-utf8.RuneCountInString(row)+gap))
		for col, r := range []rune(rowAt(titleRows, i)) {
			pen.write(r, titleColor(r, float64(col)/float64(titleWidth-1)))
		}
		pen.reset()
		b.WriteByte('\n')
	}

	b.WriteString(strings.Repeat(" ", max(0, indent+catWidth+gap+titleWidth-utf8.RuneCountInString(version))))
	for _, r := range version {
		pen.write(r, slate)
	}
	pen.reset()
	b.WriteString("\n\n")

	return b.String()
}

func catColor(r rune) color {
	switch r {
	case 'o':
		return green
	case '.', '^':
		return pink
	default:
		return amber
	}
}

func titleColor(r rune, t float64) color {
	c := gradient(t)
	if r == '█' {
		return c
	}
	return color{r: c.r / 2, g: c.g / 2, b: c.b / 2, basic: slate.basic}
}

func gradient(t float64) color {
	stops := [...]color{pink, purple, cyan}
	pos := t * float64(len(stops)-1)
	i := int(pos)
	if i >= len(stops)-1 {
		return stops[len(stops)-1]
	}
	from, to := stops[i], stops[i+1]
	f := pos - float64(i)
	c := color{
		r:     mix(from.r, to.r, f),
		g:     mix(from.g, to.g, f),
		b:     mix(from.b, to.b, f),
		basic: from.basic,
	}
	if f >= 0.5 {
		c.basic = to.basic
	}
	return c
}

func mix(from, to uint8, f float64) uint8 {
	return uint8(math.Round(float64(from) + (float64(to)-float64(from))*f))
}

func (c color) sgr(p profile) string {
	switch p {
	case trueColor:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.r, c.g, c.b)
	case ansi256:
		return fmt.Sprintf("\x1b[38;5;%dm", 16+36*cubeLevel(c.r)+6*cubeLevel(c.g)+cubeLevel(c.b))
	case ansi16:
		return fmt.Sprintf("\x1b[%dm", c.basic)
	default:
		return ""
	}
}

// cubeLevel maps a channel to the nearest xterm color cube level: 0, 95, 135, 175, 215, or 255.
func cubeLevel(v uint8) int {
	switch {
	case v < 48:
		return 0
	case v < 115:
		return 1
	default:
		return (int(v) - 35) / 40
	}
}

type pen struct {
	b       *strings.Builder
	profile profile
	current string
}

func (p *pen) write(r rune, c color) {
	if r != ' ' {
		if sgr := c.sgr(p.profile); sgr != p.current {
			p.b.WriteString(sgr)
			p.current = sgr
		}
	}
	p.b.WriteRune(r)
}

func (p *pen) reset() {
	if p.current != "" {
		p.b.WriteString("\x1b[0m")
		p.current = ""
	}
}

func lines(s string) []string {
	rows := strings.Split(strings.Trim(s, "\n"), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(row, " ")
	}
	return rows
}

func maxWidth(rows []string) int {
	width := 0
	for _, row := range rows {
		width = max(width, utf8.RuneCountInString(row))
	}
	return width
}

func rowAt(rows []string, i int) string {
	if i < len(rows) {
		return rows[i]
	}
	return ""
}
