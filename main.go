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

	State      State
	stateStack []State
}

type State struct {
	Font *font.Font
	Size float64
}

func (m *Imp) PushState() {
	m.stateStack = append(m.stateStack, m.State)
}

func (m *Imp) PopState() {
	if n := len(m.stateStack) - 1; n >= 0 {
		m.State, m.stateStack = m.stateStack[n], m.stateStack[:n]
	}
}

func (m *Imp) SplitLines(tokens []string, maxWidth float64) {
	pos := 0
	for pos < len(tokens) {
		width := 0.0
		breakPos := -1
		force := false
		m.PushState()
		for i := pos; i < len(tokens) && !((width >= maxWidth || force) && breakPos >= 0); i++ {
			if tokens[i] == " " {
				breakPos = i
				width += float64(m.State.Font.Scale(m.State.Font.HMetric(m.State.Font.Index(' ')).Width, 1000)) / 1000 * m.State.Size
			} else if strings.HasPrefix(tokens[i], "\\") {
				switch tokens[i] {
				case "\\par", "\\break":
					breakPos = i
					width = maxWidth
					force = true
				default:
					m.Apply(tokens[i])
				}
			} else {
				glyphs := m.State.Font.StringToGlyphs(tokens[i])
				for i := range glyphs {
					width += float64(m.State.Font.Scale(m.State.Font.HMetric(glyphs[i]).Width, 1000)) / 1000 * m.State.Size
					if i > 0 {
						kern := m.State.Font.Kerning(1000, glyphs[i-1], glyphs[i])
						if kern != 0 {
							width += float64(kern) / 1000 * m.State.Size
						}
					}
				}
			}
		}
		m.PopState()
		if breakPos < 0 {
			return
		}
		if (width >= maxWidth || force) && tokens[breakPos] == " " {
			tokens[breakPos] = "\\break"
		}
		pos = breakPos + 1
	}
}

func (m *Imp) CalcMaxAscent(line []string) float64 {
	ascent := 0.0
	m.PushState()
	for _, tok := range line {
		if strings.HasPrefix(tok, "\\") {
			if tok == "\\par" || tok == "\\break" {
				break
			} else {
				m.Apply(tok)
			}
		}
		a := float64(m.State.Font.Scale(m.State.Font.Ascender, 1000)) / 1000 * m.State.Size
		if a > ascent {
			ascent = a
		}
	}
	m.PopState()
	return ascent
}

func (m *Imp) Apply(cmd string) {
	switch cmd {
	case "\\bold":
		m.State.Size = 12
		m.State.Font = m.fontBold
	case "\\normal":
		m.State.Size = 12
		m.State.Font = m.fontNormal
	case "\\italic":
		m.State.Size = 12
		m.State.Font = m.fontItalic
	case "\\title":
		m.State.Size = 20
		m.State.Font = m.fontBold
	}
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

	fontNormal.StringToGlyphs("Affe")
	fontNormal.StringToGlyphs("ff")
	fontNormal.StringToGlyphs("ffx")
	fontNormal.StringToGlyphs("affil")

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
		State: State{
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
		info         = w.NextID()
		root         = w.NextID()
		pages        = w.NextID()
		page         = w.NextID()
		contents     = w.NextID()
		fontNormalId = w.NextID()
		fontBoldId   = w.NextID()
		fontItalicId = w.NextID()
		imgId        = w.NextID()
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
	imp.SplitLines(tokens, float64(pageB.Width.Computed))

	w.WriteObjectf(info, "<< /Title (Hallo Welt) >>")
	w.WriteObjectf(root, "<< /Type /Catalog /Pages %d 0 R >>", pages)

	w.WriteObjectf(pages, `<<
  /Type /Pages
  /MediaBox [0 0 %.4f %.4f]

  /Resources
  <<
    /Font << /F1 %d 0 R /F2 %d 0 R /F3 %d 0 R >>
    /ProcSet [/PDF /Text /ImageB /ImageC /ImageI]
    /XObject << /I1 %d 0 R >>
  >>
  /Kids [%d 0 R]
  /Count 1
>>`, pageB.TotalWidth(), pageB.TotalHeight(), fontNormalId, fontBoldId, fontItalicId, imgId, page)

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
			imp.PushState()
			for i, tok := range tokens[pos:] {
				if tok == "\\break" {
					wordSpacing = (float64(pageB.Width.Computed) - width) / float64(numSpaces)
					updateSpacing = pos + i + 1
					break
				} else if tok == "\\par" {
					updateSpacing = pos + i + 1
					break
				}
				if tok == " " {
					numSpaces++
				}
				if !strings.HasPrefix(tok, "\\") {
					glyphs := imp.State.Font.StringToGlyphs(tok)
					for j := range glyphs {
						if j > 0 {
							kern := imp.State.Font.Kerning(1000, glyphs[j-1], glyphs[j])
							width += float64(kern) / 1000 * imp.State.Size
						}
						width += float64(imp.State.Font.Scale(imp.State.Font.HMetric(glyphs[j]).Width, 1000)) / 1000 * imp.State.Size
					}
				} else {
					imp.Apply(tok)
				}
			}
			imp.PopState()
		}

		if strings.HasPrefix(token, "\\") {
			imp.Apply(token)
			if inTJ {
				buf.WriteString("] TJ\n")
				inTJ = false
			}
			switch token {
			case "\\par":
				fmt.Fprintf(buf, "0 %.4f Td\n", -1.4*imp.State.Size*1.8)
				yPos += -1.4 * float32(imp.State.Size) * 1.8
			case "\\break":
				fmt.Fprintf(buf, "0 %.4f Td\n", -1.4*imp.State.Size)
				yPos += -1.4 * float32(imp.State.Size)
			case "\\normal":
				fmt.Fprintf(buf, "/F1 %.4f Tf\n", imp.State.Size)
			case "\\bold":
				fmt.Fprintf(buf, "/F2 %.4f Tf\n", imp.State.Size)
			case "\\italic":
				fmt.Fprintf(buf, "/F3 %.4f Tf\n", imp.State.Size)
			case "\\title":
				fmt.Fprintf(buf, "/F2 %.4f Tf\n", imp.State.Size)
			}
			continue
		}
		if !inTJ {
			buf.WriteString("[")
			inTJ = true
		}
		buf.WriteString("<")
		glyphs := imp.State.Font.StringToGlyphs(token)
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
		if token == " " && wordSpacing > 0 {
			fmt.Fprintf(buf, "%d ", -int(wordSpacing/imp.State.Size*1000))
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

	w.WriteFontEmbedded(fontNormalId, fontNormal)
	w.WriteFontEmbedded(fontBoldId, fontBold)
	w.WriteFontEmbedded(fontItalicId, fontItalic)

	w.WriteImageJPEG(imgId, img)

	w.WriteFooter(root, info)
}

func Lex(input string) []string {
	var tokens []string
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
			if strings.Count(input[pos:end], "\n") >= 2 {
				tokens = append(tokens, `\par`)
			} else {
				tokens = append(tokens, " ")
			}
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
			tokens = append(tokens, input[pos:end])
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
			tokens = append(tokens, input[pos:end])
			pos = end
		}
	}
	return tokens
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
