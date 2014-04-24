// Copyright (c) 2014 by Christoph Hack <christoph@tux21b.org>
// All rights reserved. Distributed under the Simplified BSD License.

package pdf

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/ascii85"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"time"
	"unicode"

	"github.com/tux21b/imp/imp/otf"
)

type PDFWriter struct {
	w    *bufio.Writer
	pos  int
	err  error
	xref []int

	inTJ bool
}

func NewPDFWriter(out io.Writer) *PDFWriter {
	return &PDFWriter{w: bufio.NewWriter(out)}
}

func (w *PDFWriter) WriteString(s string) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	var n int
	n, w.err = w.w.WriteString(s)
	w.pos += n
	return n, w.err
}

func (w *PDFWriter) WriteStreamPlain(s string) error {
	if w.err != nil {
		return w.err
	}
	fmt.Fprintf(w, "<< /Length %d >>\n", len(s))
	w.WriteString("stream\n")
	w.WriteString(s)
	w.WriteString("\nendstream\n")
	return w.err
}

func (w *PDFWriter) WriteObjectStart(id int) int {
	if id <= 0 {
		id = w.NextID()
	}
	w.xref[id-1] = w.pos
	fmt.Fprintf(w, "%d 0 obj\n", id)
	return id
}

func (w *PDFWriter) WriteObjectEnd() {
	w.WriteString("endobj\n")
}

func (w *PDFWriter) WriteObjectf(id int, format string, args ...interface{}) int {
	id = w.WriteObjectStart(id)
	fmt.Fprintf(w, format, args...)
	w.WriteString("\n")
	w.WriteObjectEnd()
	return id
}

func (w *PDFWriter) NextID() int {
	w.xref = append(w.xref, 0)
	return len(w.xref)
}

func (w *PDFWriter) WriteHeader() {
	w.WriteString("%PDF-1.4\n")
	w.WriteString("%âãÏÓ\n")
}

func (w *PDFWriter) WriteFooter(root, info int) {
	startxref := w.pos
	fmt.Fprintf(w, "xref\n0 %d\n0000000000 65535 f \n", len(w.xref)+1)
	for _, pos := range w.xref {
		fmt.Fprintf(w, "%010d 00000 n \n", pos)
	}
	w.WriteString("trailer\n")

	h := md5.New()
	binary.Write(h, binary.BigEndian, time.Now().UnixNano())
	id := h.Sum(nil)

	fmt.Fprintf(w, `<<
  /Size %d
  /Info %d 0 R
  /Root %d 0 R
  /ID [<%x> <%x>]
>>
`, len(w.xref)+1, info, root, id, id)
	fmt.Fprintf(w, "startxref\n%d\n", startxref)
	w.WriteString("%%EOF\n")
	w.w.Flush()
}

func (w *PDFWriter) WriteFontEmbedded(id int, f *otf.Font) {
	var (
		fontBase       = id
		fontDescedant  = w.NextID()
		fontDescriptor = w.NextID()
		fontStream     = w.NextID()
		fontUnicode    = w.NextID()
	)

	name := encodeName(f.PostscriptName)
	cff := f.CFF()

	// base font object
	w.WriteObjectf(fontBase, `<<
  /Type /Font
  /Subtype /Type0
  /BaseFont %s
  /Encoding /Identity-H
  /ToUnicode %d 0 R
  /DescendantFonts [%d 0 R]
>>`, name, fontUnicode, fontDescedant)

	// font descedant
	widths := make([]int, f.NumGlyphs())
	for i := 0; i < len(widths); i++ {
		widths[i] = f.Scale(f.HMetric(otf.Index(i)).Width, 1000)
	}
	fontType := 2
	if cff != nil {
		fontType = 0
	}
	w.WriteObjectf(fontDescedant, `<<
  /Type /Font
  /Subtype /CIDFontType%d
  /BaseFont %s
  /CIDSystemInfo
  <<
    /Registry (Adobe)
    /Ordering (Identity)
    /Supplement 0
  >>
  /DW %d
  /W [0 %v]
  /FontDescriptor %d 0 R
>>`, fontType, name, widths[0], widths, fontDescriptor)

	// font descriptor
	fontFile := 2
	if cff != nil {
		fontFile = 3
	}
	flags := 0
	if f.ItalicAngle != 0 {
		flags |= 0x40 // italic
	}
	flags |= 0x20 // non-symbolic font
	w.WriteObjectf(fontDescriptor, `<<
  /Type /FontDescriptor
  /FontName %s
  /Ascent %d
  /Descent %d
  /CapHeight %d
  /FontBBox [%d %d %d %d]
  /ItalicAngle %.4f
  /Flags %d
  /StemV 0
  /FontFile%d %d 0 R
>>`, name, f.Scale(f.Ascender, 1000), f.Scale(f.Descender, 1000),
		f.Scale(f.CapHeight, 1000), f.Scale(f.XMin, 1000),
		f.Scale(f.YMin, 1000), f.Scale(f.XMax, 1000),
		f.Scale(f.YMax, 1000), f.ItalicAngle, flags, fontFile, fontStream)

	// font stream
	w.WriteObjectStart(fontStream)
	streamBuf := &bytes.Buffer{}
	enc := ascii85.NewEncoder(streamBuf)
	enc.Write(cff)
	enc.Close()
	fontStreamBytes := streamBuf.Bytes()

	if cff == nil {
		ttf := f.TTF()
		fmt.Fprintf(w, "<< /Length %d /Length1 %d >>\n", len(ttf), len(ttf))
		fmt.Fprintf(w, "stream\n%s\nendstream\n", ttf)
	} else {
		fmt.Fprintf(w, "<< /Length %d /Length1 %d /Filter /ASCII85Decode /Subtype /CIDFontType0C >>\n", len(fontStreamBytes), len(cff)) // CIDType0C or Type1C depending on the font
		fmt.Fprintf(w, "stream\n%s\nendstream\n", fontStreamBytes)
	}
	w.WriteObjectEnd()

	// to unicode mapping
	w.WriteObjectStart(fontUnicode)
	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (FontSpecific) /Ordering (%s) /Supplement 0 >> def
/CMapName /FontSpecific-%s def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
`, name[1:], name[1:])
	glyphs := make([]rune, f.NumGlyphs())
	for i := 0; i < math.MaxUint16; i++ {
		glyphs[f.Index(rune(i))] = rune(i)
	}
	total := 0
	for i := 0; i < len(glyphs); i++ {
		if glyphs[i] != 0 {
			total++
		}
	}
	section := 0
	inside := false
	for i := 0; i < len(glyphs); i++ {
		if glyphs[i] == 0 {
			continue
		}
		if section--; section < 0 {
			if section = total; section > 100 {
				section = 100
			}
			total -= section
			if inside {
				fmt.Fprintf(buf, "endbfchar\n")
			}
			fmt.Fprintf(buf, "%d beginbfchar\n", section)
			inside = true
		}
		fmt.Fprintf(buf, "<%04x> <%04x>\n", i, glyphs[i])
	}
	if inside {
		fmt.Fprintf(buf, "endbfchar\n")
	}
	fmt.Fprintf(buf, `endcmap
CMapName currentdict /CMap defineresource pop
end
end`)
	w.WriteStreamPlain(buf.String())
	w.WriteObjectEnd()
}

func (w *PDFWriter) WriteImageJPEG(id int, img image.Image) {
	w.WriteObjectStart(id)
	buf := &bytes.Buffer{}
	jpeg.Encode(buf, img, nil)
	s := img.Bounds().Size()
	fmt.Fprintf(w, `<<
  /Type /XObject
  /Subtype /Image
  /Width %d
  /Height %d
  /ColorSpace /DeviceRGB
  /BitsPerComponent 8
  /Interpolate true
  /Filter [/DCTDecode]
  /Length %d
>>
stream
%s
endstream
`, s.X, s.Y, buf.Len(), buf.Bytes())
	w.WriteObjectEnd()
}

func (w *PDFWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	var n int
	n, w.err = w.w.Write(p)
	w.pos += n
	return n, w.err
}

func u(v int) float32 {
	return float32(v) / 1000
}

func (w *PDFWriter) Pos() int {
	return w.pos
}

func mmToPt(v float32) float32 {
	return v * 72.0 / 25.4
}

func mm(v float32) int {
	return int(v * 72.0 / 25.4 * 1000)
}

func encodeName(s string) string {
	buf := &bytes.Buffer{}
	buf.WriteByte('/')
	for i, r := range s {
		if i == 0 && r == '/' {
			continue
		}
		if unicode.IsLetter(r) {
			buf.WriteRune(r)
		} else if r <= 0xff {
			fmt.Fprintf(buf, "#%02x", r)
		}
	}
	return buf.String()
}
