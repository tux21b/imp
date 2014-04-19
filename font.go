// Copyright (c) 2014 by Christoph Hack <christoph@tux21b.org>
// All rights reserved. Distributed under the Simplified BSD License.

// This source is based on a file from the freetype-go implementation, which
// was originally developed by the Freetype Go authors. See:
// https://code.google.com/p/freetype-go/source/browse/freetype/truetype/truetype.go

package main

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

// An Index is a Font's index of a rune.
type Index uint16

type FontError string

func (e FontError) Error() string {
	return string(e)
}

// An HMetric holds the horizontal metrics of a single glyph.
type HMetric struct {
	Width, Left int
}

// A cm holds a parsed cmap entry.
type cm struct {
	start, end, delta, offset uint32
}

type Font struct {
	NamePS  string // PostScript name
	NameOS  string // OS specific name
	NamePDF string // PDF name

	scale, xmin, xmax, ymin, ymax int

	ascent, descent, capHeight int
	license                    int

	cm          []cm
	hm          []HMetric
	cmapIndexes []byte
	nHMetric    int
	nGlyph      int
	nKern       int
	kernTable   []byte
	italic      float32

	cff []byte

	ttf []byte
}

func ParseFont(ttf []byte) (*Font, error) {
	const (
		SigOtto = 0x4f54544f
	)
	if len(ttf) < 12 {
		return nil, FontError("TTF data is too short")
	}
	offset := 0
	magic, offset := u32(ttf, offset), offset+4
	if magic != 0x00010000 && magic != SigOtto {
		return nil, FontError(fmt.Sprintf("bad TTF version %x", magic))
	}
	n, offset := int(u16(ttf, offset)), offset+2
	if len(ttf) < 16*n+12 {
		return nil, FontError("TTF data is too short")
	}

	f := &Font{ttf: ttf}

	var name, head, os2, cmap, hhea, hmtx, maxp, kern, post []byte
	var err error
	for i := 0; i < n; i++ {
		x := 16*i + 12
		switch string(ttf[x : x+4]) {
		case "cmap":
			cmap, err = readTable(ttf, ttf[x+8:x+16])
		case "name":
			name, err = readTable(ttf, ttf[x+8:x+16])
		case "head":
			head, err = readTable(ttf, ttf[x+8:x+16])
		case "OS/2":
			os2, err = readTable(ttf, ttf[x+8:x+16])
		case "hhea":
			hhea, err = readTable(ttf, ttf[x+8:x+16])
		case "hmtx":
			hmtx, err = readTable(ttf, ttf[x+8:x+16])
		case "maxp":
			maxp, err = readTable(ttf, ttf[x+8:x+16])
		case "kern":
			kern, err = readTable(ttf, ttf[x+8:x+16])
		case "CFF ":
			if magic == SigOtto {
				fmt.Println("cff parsed")
				f.cff, err = readTable(ttf, ttf[x+8:x+16])
			}
		case "post":
			post, err = readTable(ttf, ttf[x+8:x+16])
		}
		if err != nil {
			return nil, err
		}
	}

	if err := f.parseName(name); err != nil {
		return nil, err
	}
	if err := f.parseCmap(cmap); err != nil {
		return nil, err
	}
	if err := f.parseHead(head); err != nil {
		return nil, err
	}
	if err := f.parseOS2(os2); err != nil {
		return nil, err
	}
	if err := f.parseHhea(hhea); err != nil {
		return nil, err
	}
	if err := f.parseHmtx(hmtx); err != nil {
		return nil, err
	}
	if err := f.parseMaxp(maxp); err != nil {
		return nil, err
	}
	if err := f.parseKern(kern); err != nil {
		return nil, err
	}
	if err := f.parsePost(post); err != nil {
		return nil, err
	}
	return f, nil
}

// readTable returns a slice of the TTF data given by a table's directory entry.
func readTable(ttf []byte, offsetLength []byte) ([]byte, error) {
	offset := int(u32(offsetLength, 0))
	if offset < 0 {
		return nil, FontError(fmt.Sprintf("offset too large: %d", uint32(offset)))
	}
	length := int(u32(offsetLength, 4))
	if length < 0 {
		return nil, FontError(fmt.Sprintf("length too large: %d", uint32(length)))
	}
	end := offset + length
	if end < 0 || end > len(ttf) {
		return nil, FontError(fmt.Sprintf("offset + length too large: %d", uint32(offset)+uint32(length)))
	}
	return ttf[offset:end], nil
}

func (f *Font) parseName(ttf []byte) error {
	if len(ttf) < 6 {
		return FontError("TTF name block is too short")
	}
	n, start := int(u16(ttf, 2)), int(u16(ttf, 4))
	if 6+n*12 >= len(ttf) {
		return FontError("TTF name block is too short")
	}
	for i := 0; i < n; i++ {
		x := 6 + i*12
		platform := u16(ttf, x)
		lang := u16(ttf, x+4)
		typ := u16(ttf, x+6)
		length := int(u16(ttf, x+8))
		pos := int(u16(ttf, x+10)) + start
		if pos+length > len(ttf) {
			return FontError("invalid TTF name block entry")
		}
		runes := make([]uint16, length/2)
		for j := 0; j < len(runes); j++ {
			runes[j] = u16(ttf, pos+2*j)
		}
		text := string(utf16.Decode(runes))
		if platform == 3 && typ == 6 {
			f.NamePS = text
		}
		if platform == 3 && typ == 4 && lang == 0x409 {
			f.NameOS = text
		}
	}
	if len(f.NameOS) == 0 {
		return FontError("missing os specific TTF name entry")
	}
	f.NamePDF = f.NameOS
	if len(f.NamePS) > 0 {
		f.NamePDF = f.NamePS
	}
	f.NamePDF = strings.Replace(f.NamePDF, " ", "", -1)
	return nil
}

func (f *Font) parseCmap(cmap []byte) error {
	const (
		cmapFormat4         = 4
		languageIndependent = 0

		unicodeEncoding         = 0x00000003 // PID = 0 (Unicode), PSID = 3 (Unicode 2.0)
		microsoftSymbolEncoding = 0x00030000 // PID = 3 (Microsoft), PSID = 0 (Symbol)
		microsoftUCS2Encoding   = 0x00030001 // PID = 3 (Microsoft), PSID = 1 (UCS-2)
		microsoftUCS4Encoding   = 0x0003000a // PID = 3 (Microsoft), PSID = 10 (UCS-4)
	)
	if len(cmap) < 4 {
		return FontError("cmap too short")
	}
	nsubtab := int(u16(cmap, 2))
	if len(cmap) < 8*nsubtab+4 {
		return FontError("cmap too short")
	}
	offset, found, x := 0, false, 4
	for i := 0; i < nsubtab; i++ {
		// We read the 16-bit Platform ID and 16-bit Platform Specific ID as a single uint32.
		// All values are big-endian.
		pidPsid, o := u32(cmap, x), u32(cmap, x+4)
		x += 8
		// We prefer the Unicode cmap encoding. Failing to find that, we fall
		// back onto the Microsoft cmap encoding.
		if pidPsid == unicodeEncoding {
			offset, found = int(o), true
			break
		} else if pidPsid == microsoftSymbolEncoding ||
			pidPsid == microsoftUCS2Encoding ||
			pidPsid == microsoftUCS4Encoding {

			offset, found = int(o), true
			// We don't break out of the for loop, so that Unicode can override Microsoft.
		}
	}
	if !found {
		return FontError("unsupported cmap encoding")
	}
	if offset <= 0 || offset > len(cmap) {
		return FontError("bad cmap offset")
	}

	cmapFormat := u16(cmap, offset)
	switch cmapFormat {
	case cmapFormat4:
		language := u16(cmap, offset+4)
		if language != languageIndependent {
			return FontError(fmt.Sprintf("unsupported language: %d", language))
		}
		segCountX2 := int(u16(cmap, offset+6))
		if segCountX2%2 == 1 {
			return FontError(fmt.Sprintf("bad segCountX2: %d", segCountX2))
		}
		segCount := segCountX2 / 2
		offset += 14
		f.cm = make([]cm, segCount)
		for i := 0; i < segCount; i++ {
			f.cm[i].end = uint32(u16(cmap, offset))
			offset += 2
		}
		offset += 2
		for i := 0; i < segCount; i++ {
			f.cm[i].start = uint32(u16(cmap, offset))
			offset += 2
		}
		for i := 0; i < segCount; i++ {
			f.cm[i].delta = uint32(u16(cmap, offset))
			offset += 2
		}
		for i := 0; i < segCount; i++ {
			f.cm[i].offset = uint32(u16(cmap, offset))
			offset += 2
		}
		f.cmapIndexes = cmap[offset:]
		return nil
	}
	return FontError(fmt.Sprintf("unsupported cmap format: %d", cmapFormat))
}

func (f *Font) parseHhea(hhea []byte) error {
	if len(hhea) != 36 {
		return FontError("bad TTF hhea block length")
	}
	f.nHMetric = int(u16(hhea, 34))
	fmt.Println(f.nHMetric)

	/*if 4*f.nHMetric+2*(f.nGlyph-f.nHMetric) != len(f.hmtx) {
	        return FormatError(fmt.Sprintf("bad hmtx length: %d", len(f.hmtx)))
	}*/
	return nil
}

func (f *Font) parseHmtx(hmtx []byte) error {
	if len(hmtx) < 4*f.nHMetric {
		return FontError("bad TTF hmtx block length")
	}
	f.hm = make([]HMetric, f.nHMetric)
	for i := 0; i < f.nHMetric; i++ {
		f.hm[i].Width = int(u16(hmtx, 4*i))
		f.hm[i].Left = int(u16(hmtx, 4*i+2))
	}
	return nil
}

func (f *Font) parseMaxp(maxp []byte) error {
	if len(maxp) < 6 {
		return FontError("bad TTF maxp block length")
	}
	f.nGlyph = int(u16(maxp, 4))
	return nil
}

func (f *Font) parseKern(kern []byte) error {
	if len(kern) == 0 {
		return nil
	}
	if len(kern) < 18 {
		return FontError("TTF kern data too short")
	}
	version, offset := u16(kern, 0), 2
	if version != 0 {
		return FontError(fmt.Sprintf("unsupported TTF kern version: %d", version))
	}
	n, offset := u16(kern, offset), offset+2
	if n != 1 {
		return FontError(fmt.Sprintf("unsupported number of kern tables: %d", n))
	}
	offset += 2
	length, offset := int(u16(kern, offset)), offset+2
	coverage, offset := u16(kern, offset), offset+2
	if coverage != 0x0001 {
		// We only support horizontal kerning.
		return FontError(fmt.Sprintf("unsupported kern coverage: 0x%04x", coverage))
	}
	f.nKern, offset = int(u16(kern, offset)), offset+2
	if 6*f.nKern != length-14 {
		return FontError("bad kern table length")
	}
	f.kernTable = kern[14:]
	return nil
}

func (f *Font) parsePost(post []byte) error {
	if len(post) < 16 {
		return FontError("TTF post block is too short")
	}
	f.italic = float32(int16(u16(post, 4))) + float32(u16(post, 6))/math.MaxUint16
	return nil
}

func (f *Font) Scale(value, scale int) int {
	return (value * scale) / f.scale
}

// Kerning returns the kerning for the given glyph pair.
func (f *Font) Kerning(scale int, i0, i1 Index) int {
	if f.nKern == 0 {
		return 0
	}
	g := uint32(i0)<<16 | uint32(i1)
	lo, hi := 0, f.nKern
	for lo < hi {
		i := (lo + hi) / 2
		ig := u32(f.kernTable, 4+6*i)
		if ig < g {
			lo = i + 1
		} else if ig > g {
			hi = i
		} else {
			return f.Scale(scale, int(int16(u16(f.kernTable, 8+6*i))))
		}
	}
	return 0
}

func (f *Font) NumGlyphs() int {
	return f.nGlyph
}

func (f *Font) HMetric(i Index) HMetric {
	if i < 0 || len(f.hm) == 0 || int(i) >= f.nGlyph {
		return HMetric{}
	}
	if int(i) >= f.nHMetric {
		return f.hm[len(f.hm)-1]
	}
	return f.hm[i]
}

func (f *Font) parseHead(ttf []byte) error {
	if len(ttf) != 54 {
		return FontError("invalid TTF head block length")
	}
	f.scale = int(u16(ttf, 18))
	f.xmin = int(int16(u16(ttf, 36)))
	f.ymin = int(int16(u16(ttf, 38)))
	f.xmax = int(int16(u16(ttf, 40)))
	f.ymax = int(int16(u16(ttf, 42)))
	return nil
}

func (f *Font) parseOS2(ttf []byte) error {
	if len(ttf) < 72 {
		return FontError("TTF OS/2 block is too short")
	}
	version := u16(ttf, 0)
	f.license = int(int16(u16(ttf, 8)))
	f.ascent = int(int16(u16(ttf, 68)))
	f.descent = int(int16(u16(ttf, 70)))
	f.capHeight = f.ascent
	if version >= 2 && len(ttf) >= 90 {
		f.capHeight = int(int16(u16(ttf, 88)))
	}
	return nil
}

// Index returns a Font's index for the given rune.
func (f *Font) Index(x rune) Index {
	c := uint32(x)
	for i, j := 0, len(f.cm); i < j; {
		h := i + (j-i)/2
		cm := &f.cm[h]
		if c < cm.start {
			j = h
		} else if cm.end < c {
			i = h + 1
		} else if cm.offset == 0 {
			return Index(c + cm.delta)
		} else {
			offset := int(cm.offset) + 2*(h-len(f.cm)+int(c-cm.start))
			return Index(u16(f.cmapIndexes, offset))
		}
	}
	return 0
}

func (f *Font) Index2(x rune) Index {
	c := uint32(x)
	seg := -1
	for i := 0; i < len(f.cm); i++ {
		if f.cm[i].end >= c {
			seg = i
			break
		}
	}
	if seg < 0 || f.cm[seg].start > c {
		return 0
	}
	rval := rune(c)
	if f.cm[seg].offset != 0 {
		offset := int(f.cm[seg].offset) + 2*(seg+int(c-f.cm[seg].start))
		rval = rune(u16(f.cmapIndexes, offset))
	}
	if f.cm[seg].delta != 0 {
		rval = (rval + rune(f.cm[seg].delta)) % 0x10000
	}
	return Index(rval)
}

// u32 returns the big-endian uint32 at b[i:].
func u32(b []byte, i int) uint32 {
	return uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3])
}

// u16 returns the big-endian uint16 at b[i:].
func u16(b []byte, i int) uint16 {
	return uint16(b[i])<<8 | uint16(b[i+1])
}
