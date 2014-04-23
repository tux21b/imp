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

	"github.com/tux21b/imp/font"
)

type Imp struct {
	fontNormal *font.Font
	fontBold   *font.Font
	fontItalic *font.Font

	State      *State
	stateStack []*State

	Fonts []*font.Font
}

func (m *Imp) GetFontId(f *font.Font) string {
	for i := 0; i < len(m.Fonts); i++ {
		if m.Fonts[i] == f {
			return fmt.Sprintf("/F%d", i+1)
		}
	}
	m.Fonts = append(m.Fonts, f)
	return fmt.Sprintf("/F%d", len(m.Fonts))
}

type State struct {
	Imp  *Imp
	Font *font.Font
	Size float64
}

func (s *State) Clone() *State {
	cp := *s
	return &cp
}

func (m *Imp) SplitLines(tokens []Token, maxWidth float64) {
	pos := 0
	for pos < len(tokens) {
		width := 0.0
		breakPos := -1
		s := m.State.Clone()
		for i := pos; i < len(tokens); i++ {
			if w := GetWidth(s, tokens[i]); width+w > maxWidth {
				break
			} else {
				width += w
			}
			switch tokens[i].(type) {
			case LineBreak, ParagraphBreak:
				breakPos = i
				i = len(tokens)
			case CanBreak:
				breakPos = i
			}
		}
		if breakPos < 0 {
			return
		}
		for i := pos; i < breakPos; i++ {
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
	fontNormal, err := font.Open("fonts/SourceSansPro-Regular.otf")
	if err != nil {
		log.Fatalln(err)
	}
	fontBold, err := font.Open("fonts/SourceSansPro-Bold.otf")
	if err != nil {
		log.Fatalln(err)
	}
	fontItalic, err := font.Open("fonts/SourceSansPro-It.otf")
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
			Font: fontNormal,
			Size: 12,
		},
	}

	out, err := os.Create("output.pdf")
	if err != nil {
		log.Fatalln(err)
	}
	defer out.Close()

	w := NewPDFWriter(out)
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
			case "\\title":
				tokens[i] = SetFont{Font: fontBold, Size: 20}
			case "\\bold":
				tokens[i] = SetFont{Font: fontBold, Size: 12}
			case "\\normal":
				tokens[i] = SetFont{Font: fontNormal, Size: 12}
			case "\\italic":
				tokens[i] = SetFont{Font: fontItalic, Size: 12}
			}
		case Space:
			if strings.Count(string(tok), "\n") >= 2 {
				tokens[i] = ParagraphBreak{}
			} else {
				tokens[i] = CanBreak{NoBreak: tok}
			}
		case Text:
			parts := Hyphenate(string(tok))
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
	/*
		for i := range tokens {
			r, _ := utf8.DecodeRuneInString(tokens[i])
			if !unicode.IsSpace(r) && r != '\\' {
				tokens[i] = strings.Join(Hyphenate(tokens[i]), "-")
			}
		}
	*/

	imp.SplitLines(tokens, float64(pageB.Width.Computed))

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
	yPos := pageB.PaddingBottom.Computed + pageB.Height.Computed - float32(imp.CalcMaxAscent(tokens))
	fmt.Fprintf(buf, "BT /F1 %.4f Tf\n1.4 TL\n%.4f %.4f Td\n",
		imp.State.Size, pageB.PaddingLeft.Computed, yPos)

	inTJ := false
	wordSpacing := 0.0
	updateSpacing := -1
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
					wordSpacing = (float64(pageB.Width.Computed) - width) / float64(numSpaces)
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
			glyphs := imp.State.Font.StringToGlyphs(string(x))
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
			fmt.Fprintf(buf, "0 %.4f Td\n", -1.4*imp.State.Size)
			yPos += -1.4 * float32(imp.State.Size)
		case ParagraphBreak:
			if inTJ {
				buf.WriteString("] TJ\n")
				inTJ = false
			}
			fmt.Fprintf(buf, "0 %.4f Td\n", -1.4*imp.State.Size*1.8)
			yPos += -1.4 * float32(imp.State.Size) * 1.8
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
		}
	}
	if inTJ {
		buf.WriteString("] TJ\n")
	}
	buf.WriteString("ET\n")

	imgS := img.Bounds().Size()
	imgW := pageB.Width.Computed
	imgH := float32(imgS.Y) * imgW / float32(imgS.X)
	imgY := 0.5*(yPos-pageB.PaddingBottom.Computed-imgH) + pageB.PaddingBottom.Computed
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
		glyphs := s.Font.StringToGlyphs(string(t))
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

type SetFont struct {
	Font *font.Font
	Size int
}

var fullText = `\title Hello Imp!\normal\par

\normal This output was produced by \bold Imp\normal, a very early prototype
of a \italic modern typesetting system \normal written in Go. Imp is able
to output PDF files, has full Unicode support and supports modern font
formats like OpenType™ and TrueType™.

For example Imp currently works well with characters like € or ©, can deal
with other languages \italic „Umlaute sind blöd“ \normal and renders
ligatures like Th, ff, ffi, ffj automatically. A good input format with an
extensive macro system (similar to TeX or lout) is still missing. Feel
free to contribute!

Lorem \italic ipsum dolor \normal sit amet, consectetuer adipiscing elit. Aenean commodo
ligula eget dolor. Aenean massa. Cum sociis natoque penatibus et magnis dis
parturient montes, \bold nascetur ridiculus mus\normal. Donec quam felis,
ultricies nec, pellentesque eu, pretium quis, sem. Nulla consequat massa quis enim.

Donec pede \bold justo, fringilla \normal vel, aliquet nec, vulputate eget,
arcu. In enim justo, rhoncus ut, imperdiet a, venenatis vitae, justo. Nullam
dictum felis eu pede mollis pretium. Integer tincidunt. Cras dapibus. Vivamus
elementum semper nisi. Aenean vulputate eleifend tellus.

Aenean leo ligula, porttitor eu, consequat \italic vitae\normal, eleifend ac,
enim. Aliquam lorem ante, dapibus in, viverra quis, feugiat a, tellus.
Phasellus viverra nulla ut metus varius laoreet. Quisque rutrum. Aenean
imperdiet. Etiam ultricies nisi vel augue. Curabitur ullamcorper ultricies nisi.

BV BW BH F, P. Tä V.`
