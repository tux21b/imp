// Copyright (c) 2014 by Christoph Hack <christoph@tux21b.org>
// All rights reserved. Distributed under the Simplified BSD License.

package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	"log"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tux21b/imp/imp/otf"
	"github.com/tux21b/imp/imp/pdf"
	"github.com/tux21b/imp/imp/text"
)

type Imp struct {
	fontNormal *otf.Font
	fontBold   *otf.Font
	fontItalic *otf.Font

	State      *State
	stateStack []*State

	Fonts []*otf.Font
}

func (m *Imp) GetFontId(f *otf.Font) string {
	for i := 0; i < len(m.Fonts); i++ {
		if m.Fonts[i] == f {
			return fmt.Sprintf("/F%d", i+1)
		}
	}
	m.Fonts = append(m.Fonts, f)
	return fmt.Sprintf("/F%d", len(m.Fonts))
}

type State struct {
	Imp        *Imp
	Font       *otf.Font
	Size       float64
	SmallCaps  bool
	Ligatures  bool
	LineHeight float64
	ParSkip    float64
	MaxWidth   float64
	YPos       float64
	ColStart   float64
}

func (s *State) StringToGlyphs(text string) []otf.Index {
	glyphs := s.Font.StringToGlyphs(text)
	if s.SmallCaps {
		glyphs = s.Font.SmallCaps(glyphs)
	}
	if s.Ligatures {
		glyphs = s.Font.Ligatures(glyphs)
	}
	return glyphs
}

func (s *State) Clone() *State {
	cp := *s
	return &cp
}

func (m *Imp) SplitLines(tokens []Token, maxWidth float64) {
	pos := 0
	state := m.State.Clone()
	for pos < len(tokens) {
		width := 0.0
		breakPos := -1
		s := state.Clone()
		for i := pos; i < len(tokens); i++ {
			if a, ok := tokens[i].(StateAction); ok {
				a(s)
			}
			if w := GetWidth(s, tokens[i]); width+w > s.MaxWidth {
				break
			} else {
				width += w
			}
			switch x := tokens[i].(type) {
			case LineBreak, ParagraphBreak:
				breakPos = i
				i = len(tokens)
			case CanBreak:
				if width+GetWidth(s, x.Before) < s.MaxWidth {
					breakPos = i
				}
			}
		}
		if breakPos < 0 {
			return
		}
		for i := pos; i < breakPos; i++ {
			if a, ok := tokens[i].(StateAction); ok {
				a(state)
			} else {
				GetWidth(state, tokens[i])
			}
			if tok, ok := tokens[i].(CanBreak); ok {
				tokens[i] = tok.NoBreak
			}
		}
		if tok, ok := tokens[breakPos].(CanBreak); ok {
			if tok.Before != nil {
				tokens = append(tokens[:breakPos], append([]Token{tok.Before}, tokens[breakPos:]...)...)
				breakPos++
			}
			tokens[breakPos] = LineBreak{}
		}
		pos = breakPos + 1
	}
}

func (m *Imp) CalcMaxAscent(line []Token) float64 {
	ascent := 0.0
	s := m.State.Clone()
	for _, tok := range line {
		GetWidth(s, tok)
		switch tok.(type) {
		case LineBreak, ParagraphBreak:
			return ascent
		case Text, Space:
			a := float64(s.Font.Scale(s.Font.Ascender, 1000)) / 1000 * s.Size
			if a > ascent {
				ascent = a
			}
		}
	}
	return ascent
}

func main() {
	fontNormal, err := otf.Open("fonts/SourceSansPro-Regular.otf")
	if err != nil {
		log.Fatalln(err)
	}
	fontBold, err := otf.Open("fonts/SourceSansPro-Bold.otf")
	if err != nil {
		log.Fatalln(err)
	}
	fontItalic, err := otf.Open("fonts/SourceSansPro-It.otf")
	if err != nil {
		log.Fatalln(err)
	}
	fontLight, err := otf.Open("fonts/SourceSansPro-Light.otf")
	if err != nil {
		log.Fatalln(err)
	}

	imgFile, err := os.Open("buddy.jpg")
	if err != nil {
		log.Fatalln(err)
	}
	defer imgFile.Close()
	img, _, err := image.Decode(imgFile)
	if err != nil {
		log.Fatalln(err)
	}

	imp := &Imp{
		fontNormal: fontNormal,
		fontBold:   fontBold,
		fontItalic: fontItalic,
		State: &State{
			Font:       fontNormal,
			Size:       12,
			Ligatures:  true,
			LineHeight: 1.4,
			ParSkip:    1.8,
			MaxWidth:   0.0,
		},
	}

	out, err := os.Create("output.pdf")
	if err != nil {
		log.Fatalln(err)
	}
	defer out.Close()

	w := pdf.NewPDFWriter(out)
	w.WriteHeader()

	var (
		info     = w.NextID()
		root     = w.NextID()
		pages    = w.NextID()
		page     = w.NextID()
		contents = w.NextID()
		imgId    = w.NextID()
	)

	pageB := &Box{
		Width:         MustParseLength("160mm"),
		Height:        MustParseLength("252mm"),
		PaddingTop:    MustParseLength("25mm"),
		PaddingRight:  MustParseLength("25mm"),
		PaddingBottom: MustParseLength("20mm"),
		PaddingLeft:   MustParseLength("25mm"),
	}

	tokens := Lex(fullText)
	for i := 0; i < len(tokens); i++ {
		switch tok := tokens[i].(type) {
		case Macro:
			switch tok {
			case "\\par":
				tokens[i] = ParagraphBreak{}
			case "\\break":
				tokens[i] = LineBreak{}
			case "\\bold":
				tokens[i] = SetFont{Font: fontBold}
			case "\\light":
				tokens[i] = SetFont{Font: fontLight}
			case "\\normal":
				tokens[i] = SetFont{Font: fontNormal}
			case "\\italic":
				tokens[i] = SetFont{Font: fontItalic}
			case "\\Large":
				tokens[i] = SetFont{Size: 24}
			case "\\large":
				tokens[i] = SetFont{Size: 14}
			case "\\normalsize":
				tokens[i] = SetFont{Size: 12}
			case "\\blue":
				tokens[i] = SetTextColor{1, .34, 0, .21}
			case "\\black":
				tokens[i] = SetTextColor{0, 0, 0, 1}
			case "\\smcpon":
				tokens[i] = StateAction(func(s *State) {
					s.SmallCaps = true
				})
			case "\\smcpoff":
				tokens[i] = StateAction(func(s *State) {
					s.SmallCaps = false
				})
			case "\\column":
				tokens[i] = StateAction(func(s *State) {
					s.ColStart = s.YPos
					s.MaxWidth = 0.48 * s.MaxWidth
				})
			case "\\nextcolumn":
				tokens[i] = ColBreak{}
			}
		case Space:
			if strings.Count(string(tok), "\n") >= 2 {
				tokens[i] = ParagraphBreak{}
			} else {
				tokens[i] = CanBreak{NoBreak: tok}
			}
		case Text:
			parts := text.Hyphenate(string(tok))
			repl := make([]Token, 0, 2*len(parts)-1)
			for j := range parts {
				if j > 0 {
					repl = append(repl, CanBreak{Before: Text("-")})
				}
				repl = append(repl, Text(parts[j]))
			}
			tokens = append(tokens[:i], append(repl, tokens[i+1:]...)...)
			i += len(repl) - 1
		}
	}

	imp.State.MaxWidth = float64(pageB.Width.Computed)

	imp.SplitLines(tokens, 0)

	w.WriteObjectf(info, "<< /Title (Hallo Welt) >>")
	w.WriteObjectf(root, "<< /Type /Catalog /Pages %d 0 R >>", pages)

	w.WriteObjectf(page, `<<
  /Type /Page
  /Parent %d 0 R
  /Contents %d 0 R
>>`, pages, contents)

	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, ".5 w .9 G %.4f %.4f %.4f %.4f re S\n",
		pageB.PaddingLeft.Computed,
		pageB.PaddingBottom.Computed,
		pageB.Width.Computed,
		pageB.Height.Computed)
	imp.State.YPos = float64(pageB.PaddingBottom.Computed+pageB.Height.Computed) - imp.CalcMaxAscent(tokens)
	fmt.Fprintf(buf, "BT /F1 %.4f Tf\n1.4 TL\n%.4f %.4f Td\n",
		imp.State.Size, pageB.PaddingLeft.Computed, imp.State.YPos)

	inTJ := false
	wordSpacing := 0.0
	updateSpacing := -1
	yMin := 0.0
	for pos, token := range tokens {
		if pos >= updateSpacing {
			width := 0.0
			numSpaces := 0
			wordSpacing = 0
			updateSpacing = len(tokens)
			s := imp.State.Clone()
			for i := pos; i < len(tokens); i++ {
				w := GetWidth(s, tokens[i])
				switch tokens[i].(type) {
				case LineBreak:
					wordSpacing = (s.MaxWidth - width) / float64(numSpaces)
					updateSpacing = i + 1
					i = len(tokens)
				case ParagraphBreak:
					updateSpacing = i + 1
					i = len(tokens)
				case Space:
					numSpaces++
				}
				width += w
			}
		}

		switch x := token.(type) {
		case Text:
			if !inTJ {
				buf.WriteString("[")
				inTJ = true
			}
			buf.WriteString("<")
			glyphs := imp.State.StringToGlyphs(string(x))
			for i := range glyphs {
				if i > 0 {
					kern := imp.State.Font.Kerning(1000, glyphs[i-1], glyphs[i])
					if kern != 0 {
						fmt.Fprintf(buf, "> %d <", -kern)
					}
				}
				fmt.Fprintf(buf, "%04x", glyphs[i])
			}
			buf.WriteString("> ")
		case Space:
			if !inTJ {
				buf.WriteString("[")
				inTJ = true
			}
			fmt.Fprintf(buf, "<%04x> ", imp.State.Font.Index(' '))
			if wordSpacing > 0 {
				fmt.Fprintf(buf, "%d ", -int(wordSpacing/imp.State.Size*1000))
			}
		case LineBreak:
			if inTJ {
				buf.WriteString("] TJ\n")
				inTJ = false
			}
			fmt.Fprintf(buf, "0 %.4f Td\n", -imp.State.LineHeight*imp.State.Size)
			imp.State.YPos += -imp.State.LineHeight * float64(imp.State.Size)
		case ParagraphBreak:
			if inTJ {
				buf.WriteString("] TJ\n")
				inTJ = false
			}
			fmt.Fprintf(buf, "0 %.4f Td\n", -imp.State.LineHeight*imp.State.Size*imp.State.ParSkip)
			imp.State.YPos += -imp.State.LineHeight * float64(imp.State.Size) * imp.State.ParSkip
		case ColBreak:
			if inTJ {
				buf.WriteString("] TJ\n")
				inTJ = false
			}
			yOff := imp.State.ColStart - imp.State.YPos
			xOff := float64(pageB.Width.Computed) - imp.State.MaxWidth
			fmt.Fprintf(buf, "%.4f %.4f Td\n", xOff, yOff)
			yMin = imp.State.YPos
			imp.State.YPos = imp.State.ColStart
		case SetFont:
			if inTJ {
				buf.WriteString("] TJ\n")
				inTJ = false
			}
			if x.Font != nil {
				imp.State.Font = x.Font
			}
			if x.Size != 0 {
				imp.State.Size = float64(x.Size)
			}
			id := imp.GetFontId(imp.State.Font)
			fmt.Fprintf(buf, "%s %.4f Tf\n", id, imp.State.Size)
		case SetTextColor:
			if inTJ {
				buf.WriteString("] TJ\n")
				inTJ = false
			}
			fmt.Fprintf(buf, "%.4f %.4f %.4f %.4f k\n", x.C, x.M, x.Y, x.K)
		case StateAction:
			x(imp.State)
		}
	}
	if inTJ {
		buf.WriteString("] TJ\n")
	}
	buf.WriteString("ET\n")

	if y := imp.State.YPos; y < yMin {
		yMin = y
	}

	imgS := img.Bounds().Size()
	imgW := float64(pageB.Width.Computed)
	imgH := float64(imgS.Y) * imgW / float64(imgS.X)
	imgY := 0.5*(yMin-float64(pageB.PaddingBottom.Computed)-imgH) + float64(pageB.PaddingBottom.Computed)
	fmt.Fprintf(buf, `q
1 0 0 1 %.4f %.4f cm
%.4f 0 0 %.4f 0 0 cm
/I1 Do
Q`, pageB.PaddingLeft.Computed, imgY, imgW, imgH)

	w.WriteObjectStart(contents)
	w.WriteStreamPlain(buf.String())
	w.WriteObjectEnd()

	fontBuf := &bytes.Buffer{}
	fontIds := make([]int, len(imp.Fonts))
	for i := range imp.Fonts {
		fontIds[i] = w.NextID()
		fmt.Fprintf(fontBuf, "/F%d %d 0 R ", i+1, fontIds[i])
	}
	w.WriteObjectf(pages, `<<
  /Type /Pages
  /MediaBox [0 0 %.4f %.4f]

  /Resources
  <<
    /Font << %s >>
    /ProcSet [/PDF /Text /ImageB /ImageC /ImageI]
    /XObject << /I1 %d 0 R >>
  >>
  /Kids [%d 0 R]
  /Count 1
>>`, pageB.TotalWidth(), pageB.TotalHeight(), fontBuf.String(), imgId, page)

	for i := range imp.Fonts {
		w.WriteFontEmbedded(fontIds[i], imp.Fonts[i])
	}
	w.WriteImageJPEG(imgId, img)

	w.WriteFooter(root, info)
}

func Lex(input string) []Token {
	var tokens []Token
	pos := 0
	for pos < len(input) {
		r, n := utf8.DecodeRuneInString(input[pos:])
		if unicode.IsSpace(r) {
			end := pos + n
			for end < len(input) {
				r, n := utf8.DecodeRuneInString(input[end:])
				if !unicode.IsSpace(r) {
					break
				}
				end += n
			}
			tokens = append(tokens, Space(input[pos:end]))
			pos = end
		} else if r == '\\' {
			end := pos + n
			for end < len(input) {
				r, n := utf8.DecodeRuneInString(input[end:])
				if !unicode.IsLetter(r) {
					break
				}
				end += n
			}
			tokens = append(tokens, Macro(input[pos:end]))
			pos = end
			for pos < len(input) {
				r, n := utf8.DecodeRuneInString(input[pos:])
				if !unicode.IsSpace(r) {
					break
				}
				pos += n
			}
		} else {
			end := pos + n
			for end < len(input) {
				r, n := utf8.DecodeRuneInString(input[end:])
				if unicode.IsSpace(r) || r == '\\' {
					break
				}
				end += n
			}
			tokens = append(tokens, Text(input[pos:end]))
			pos = end
		}
	}
	return tokens
}

func GetWidth(s *State, t Token) float64 {
	switch t := t.(type) {
	case Text:
		glyphs := s.StringToGlyphs(string(t))
		width := 0.0
		for i := range glyphs {
			width += float64(s.Font.Scale(s.Font.HMetric(glyphs[i]).Width, 1000)) / 1000 * s.Size
			if i > 0 {
				kern := s.Font.Kerning(1000, glyphs[i-1], glyphs[i])
				if kern != 0 {
					width += float64(kern) / 1000 * s.Size
				}
			}
		}
		return width
	case CanBreak:
		return GetWidth(s, t.NoBreak)
	case Space:
		return float64(s.Font.Scale(s.Font.HMetric(s.Font.Index(' ')).Width, 1000)) / 1000 * s.Size
	case SetFont:
		if t.Font != nil {
			s.Font = t.Font
		}
		if t.Size != 0 {
			s.Size = float64(t.Size)
		}
	case StateAction:
		t(s)
	}
	return 0
}

type Token interface {
	//	Execute(s *State, w *bytes.Buffer)
}

type LineBreak struct{}

type ParagraphBreak struct{}

type CanBreak struct {
	Before  Token
	NoBreak Token
	After   Token
}

type Text string

type Space string

type Macro string

type SetTextColor struct {
	C, M, Y, K float32
}

type ColBreak struct{}

type SetFont struct {
	Font *otf.Font
	Size int
}

type StateAction func(s *State)

var fullText = `\Large\bold\blue\smcpon Hello Imp!\smcpoff\normal\normalsize\black\par

\large\light This output was produced by \normal Imp\light, a very early prototype
of a \italic modern typesetting system \light written in Go. Imp is able
to output PDF files, has full Unicode support and supports modern font
formats like OpenType™ and TrueType™.\normal\normalsize\par\break

\column\blue\smcpon\bold OpenType™ Fonts\smcpoff\normal\black\par

You can use your favorite OpenType™ and TrueType™ fonts with Imp, including
special features like \italic kerning\normal, \italic ligatures \normal and
\italic small caps\normal. Adobe's excellent \bold Source Sans Pro \normal
font family is included by default.

\blue\smcpon\bold Unicode Support\smcpoff\normal\black\par

Imp comes with full Unicode support. You can simply type any character you
want and Imp will happily display it as long as your font contains a suitable
glyph for it.

\blue\smcpon\bold Extensive Markup\smcpoff\normal\black\par

Future versions of Imp should feature a simple markup language with an
extensive macro system similar to \italic TeX \normal or \italic lout\normal.
Defining such a language is however a very complex task and no
progress has been made so far.

\nextcolumn\blue\smcpon\bold Go Package\smcpoff\normal\black\par

Imp's main strength is typesetting generated content automatically in a
beautiful way. The Go package allows you to easily embed Imp in your own
application for server side PDF generation. Complex layouts can be achieved
by extended Imp with additional plug-ins written in Go.

\blue\smcpon\bold Open Source\smcpoff\normal\black\par

The whole project is available freely and licensed under the \italic
BSD (3 clause) license\normal. Development has just started and the
source code of the prototype still looks horrible. Sorry for that.

Anyway, feel free to grab the source from \bold GitHub \normal and join
the project today!

xxx a b c`
