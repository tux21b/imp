// Copyright (c) 2014 by Christoph Hack <christoph@tux21b.org>
// All rights reserved. Distributed under the Simplified BSD License.

// Package font implements a parser for TrueType and OpenType fonts.
// Those formats are documented at http://developer.apple.com/fonts/TTRefMan/
// and http://www.microsoft.com/typography/otspec/.
package font

import (
	"fmt"
	"io/ioutil"
	"math"
	"unicode/utf16"
)

type Font struct {
	FullName       string // full font name
	PostscriptName string // Postscript name

	UnitsPerEm             int     // scaling factor for (nearly) all values here
	XMin, XMax, YMin, YMax int     // bounding box
	Ascender               int     // typographic ascender
	Descender              int     // typographic descender
	CapHeight              int     // height of an uppercase letter (from baseline)
	ItalicAngle            float32 // italic angle

	cm          []cm
	hm          []HMetric
	cmapIndexes []byte
	nHMetric    int
	nGlyph      int
	nKern       int
	kernTable   []byte

	tables map[string][]byte

	liga      []Ligature
	kern      []Kerning
	classKern *classKerner

	smcpBefore, smcpAfter []Index

	// font tables
	full []byte // complete TTF / OTF file
	head []byte // font header
	name []byte // naming table
	cff  []byte // PostScript font programm (Compact Font Format, optional)
	os2  []byte // OS/2 and Windows specific metrics
	gpos []byte // glyph positioning data
}

// Open reads in a font file stored on the filesystem.
func Open(filename string) (*Font, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse parses the font file specified by data.
func Parse(data []byte) (*Font, error) {
	const (
		SigVer1 = 0x00010000
		SigOtto = 0x4f54544f
	)
	if len(data) < 12 {
		return nil, FontError("TTF data is too short")
	}
	offset := 0
	version, offset := u32(data, offset), offset+4
	if version != SigVer1 && version != SigOtto {
		return nil, FontError(fmt.Sprintf("bad version 0x%x", version))
	}
	n, offset := int(u16(data, offset)), offset+2
	if len(data) < 16*n+12 {
		return nil, FontError("TTF data is too short")
	}

	f := &Font{full: data, tables: make(map[string][]byte)}
	for i := 0; i < n; i++ {
		x := 16*i + 12
		name := string(data[x : x+4])
		table, err := readTable(data, data[x+8:x+16])
		if err != nil {
			return nil, err
		}
		f.tables[name] = table
	}

	f.head = f.tables["head"]
	f.name = f.tables["name"]
	f.gpos = f.tables["GPOS"]
	f.cff = f.tables["CFF "]
	f.os2 = f.tables["OS/2"]

	if err := f.parseHead(); err != nil {
		return nil, err
	}
	var err error
	if f.FullName, err = f.lookupName(4); err != nil {
		return nil, err
	}
	if f.PostscriptName, err = f.lookupName(6); err != nil {
		return nil, err
	}

	if err := f.parseCmap(f.tables["cmap"]); err != nil {
		return nil, err
	}
	if err := f.parseOS2(); err != nil {
		return nil, err
	}
	if err := f.parseHhea(f.tables["hhea"]); err != nil {
		return nil, err
	}
	if err := f.parseHmtx(f.tables["hmtx"]); err != nil {
		return nil, err
	}
	if err := f.parseMaxp(f.tables["maxp"]); err != nil {
		return nil, err
	}
	if err := f.parsePost(f.tables["post"]); err != nil {
		return nil, err
	}
	if err := f.parseGsub(); err != nil {
		return nil, err
	}
	if err := f.parseGsubSmcp(); err != nil {
		return nil, err
	}
	if err := f.parseGpos(); err != nil {
		return nil, err
	}
	return f, nil
}

// lookupName traverses the name table in order to find a specific name entry.
func (f *Font) lookupName(name uint16) (string, error) {
	const (
		Unicode        uint16 = 0
		UnicodeEnglish uint16 = 0
		Windows        uint16 = 3
		WindowsUCS2    uint16 = 1
		WindowsEnglish uint16 = 0x409
	)
	if len(f.name) < 6 {
		return "", errorf("name block is too short (%d bytes)", len(f.name))
	}
	if format := u16(f.name, 0); format != 0 && format != 1 {
		return "", errorf("invalid name block format %d", format)
	}
	count, strOffset := int(u16(f.name, 2)), int(u16(f.name, 4))
	if 6+count*12 > len(f.name) {
		return "", errorf("name block is too short (%d bytes)", len(f.name))
	}
	found := -1
	for i := 0; i < count; i++ {
		entry := f.name[6+i*12 : 20+i*12]
		var (
			platformID = u16(entry, 0)
			specificID = u16(entry, 2)
			languageID = u16(entry, 4)
			nameID     = u16(entry, 6)
		)
		if nameID == name &&
			((platformID == Unicode && languageID == UnicodeEnglish) ||
				platformID == Windows && specificID == WindowsUCS2 && languageID == WindowsEnglish) {
			// We only accept Unicode (any version) and Windows UCS2 entries in English
			found = i
			break
		}
	}
	if found < 0 {
		return "", nil
	}
	length := int(u16(f.name, 14+found*12))
	offset := int(u16(f.name, 16+found*12)) + strOffset
	if offset+length > len(f.name) || length&1 != 0 {
		return "", errorf("invalid name entry offset or length")
	}
	runes := make([]uint16, length/2)
	for i := 0; i < len(runes); i++ {
		runes[i] = u16(f.name, offset+2*i)
	}
	return string(utf16.Decode(runes)), nil
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

func (f *Font) CFF() []byte {
	return f.cff
}

func (f *Font) TTF() []byte {
	return f.full
}

func (f *Font) parseGsub() error {
	data := f.tables["GSUB"]
	if len(data) == 0 {
		return nil // GSUB block is optional
	}
	if len(data) < 10 {
		return errorf("GSUB block is too short (%d bytes)", len(data))
	}
	var (
		scriptTableOffset  = int(u16(data, 4))
		featureTableOffset = int(u16(data, 6))
		lookupTableOffset  = int(u16(data, 8))
	)

	featureIDs, err := f.parseScriptTable(data, scriptTableOffset, "", "")
	if err != nil {
		return err
	}

	lookupIDs, err := f.parseFeatureTable(data, featureTableOffset, featureIDs, "liga")
	if err != nil {
		return err
	}

	// parse lookup table
	if lookupTableOffset+2 > len(data) {
		return errorf("invalid GSUB lookup table at 0x%x", lookupTableOffset)
	}
	lookupCount := int(u16(data, lookupTableOffset))
	if lookupTableOffset+2+lookupCount*2 > len(data) {
		return errorf("unexpected end of GSUB lookup table with %d entries", lookupCount)
	}
	for _, i := range lookupIDs {
		offset := int(u16(data, lookupTableOffset+2+i*2)) + lookupTableOffset

		if offset+6 > len(data) {
			return errorf("unexpected end of GSUB lookup entry at 0x%x", offset)
		}
		kind := int(u16(data, offset))
		subblockCount := int(u16(data, offset+4))
		if offset+6+subblockCount*2 > len(data) {
			return errorf("unexpected end of GSUB lookup entry at 0x%x", offset)
		}
		if kind != 4 {
			return errorf("unsupported GSUB lookup type %d", kind)
		}
		for j := 0; j < subblockCount; j++ {
			subblockOffset := int(u16(data, offset+6+j*2)) + offset
			if subblockOffset+6 > len(data) {
				return errorf("unexpected end of GSUB subblock at 0x%x", subblockOffset)
			}
			if format := u16(data, subblockOffset); format != 1 {
				return errorf("unsupported GSUB lookup type 4 format %d", format)
			}
			coverageOffset := int(u16(data, subblockOffset+2)) + subblockOffset
			ligaSetCount := int(u16(data, subblockOffset+4))

			// parse coverage
			coverage, err := f.parseCoverage(data, coverageOffset)
			if err != nil {
				return err
			}
			if len(coverage) != ligaSetCount {
				return errorf("GSUB coverage length doesn't match ligature set length")
			}

			// parse ligature sets
			if subblockOffset+6+ligaSetCount*2 > len(data) {
				return errorf("unexpected end of GSUB liga subblock at 0x%x", subblockOffset)
			}
			for k := 0; k < ligaSetCount; k++ {
				ligaSetOffset := int(u16(data, subblockOffset+6+k*2)) + subblockOffset
				if ligaSetOffset+2 > len(data) {
					return errorf("unexpected end of GSUB ligature set at 0x%x", ligaSetOffset)
				}
				ligaCount := int(u16(data, ligaSetOffset))
				if ligaSetOffset+2+2*ligaCount > len(data) {
					return errorf("unexpected end of GSUB ligature set at 0x%x", ligaSetOffset)
				}
				// parse ligatures
				for l := 0; l < ligaCount; l++ {
					ligaOffset := int(u16(data, ligaSetOffset+2+2*l)) + ligaSetOffset
					if ligaOffset+4 > len(data) {
						return errorf("unexpected end of GSUB ligature entry at 0x%x", ligaOffset)
					}
					ligaGlyph := Index(int(u16(data, ligaOffset)))
					compCount := int(u16(data, ligaOffset+2))
					if ligaOffset+4+(compCount-1)*2 > len(data) {
						return errorf("unexpected end of GSUB ligature entry at 0x%x", ligaOffset)
					}
					component := make([]Index, compCount)
					component[0] = coverage[k]
					for m := 1; m < compCount; m++ {
						component[m] = Index(u16(data, ligaOffset+4+m*2-2))
					}
					f.liga = append(f.liga, Ligature{Old: component, New: ligaGlyph})
				}
			}
		}
	}
	return nil
}

func (f *Font) parseGsubSmcp() error {
	data := f.tables["GSUB"]
	if len(data) == 0 {
		return nil // GSUB block is optional
	}
	if len(data) < 10 {
		return errorf("GSUB block is too short (%d bytes)", len(data))
	}
	var (
		scriptTableOffset  = int(u16(data, 4))
		featureTableOffset = int(u16(data, 6))
		lookupTableOffset  = int(u16(data, 8))
	)

	featureIDs, err := f.parseScriptTable(data, scriptTableOffset, "", "")
	if err != nil {
		return err
	}

	lookupIDs, err := f.parseFeatureTable(data, featureTableOffset, featureIDs, "smcp")
	if err != nil {
		return nil
	}

	// parse lookup table
	if lookupTableOffset+2 > len(data) {
		return errorf("invalid GSUB lookup table at 0x%x", lookupTableOffset)
	}
	lookupCount := int(u16(data, lookupTableOffset))
	if lookupTableOffset+2+lookupCount*2 > len(data) {
		return errorf("unexpected end of GSUB lookup table with %d entries", lookupCount)
	}
	for _, i := range lookupIDs {
		offset := int(u16(data, lookupTableOffset+2+i*2)) + lookupTableOffset

		if offset+6 > len(data) {
			return errorf("unexpected end of GSUB lookup entry at 0x%x", offset)
		}
		kind := int(u16(data, offset))
		subblockCount := int(u16(data, offset+4))
		if offset+6+subblockCount*2 > len(data) {
			return errorf("unexpected end of GSUB lookup entry at 0x%x", offset)
		}
		if kind != 1 {
			return errorf("unsupported GSUB lookup type %d", kind)
		}
		for j := 0; j < subblockCount; j++ {
			subblockOffset := int(u16(data, offset+6+j*2)) + offset
			if subblockOffset+6 > len(data) {
				return errorf("unexpected end of GSUB subblock at 0x%x", subblockOffset)
			}
			if format := u16(data, subblockOffset); format != 2 {
				return errorf("unsupported GSUB lookup type 1 format %d", format)
			}
			coverageOffset := int(u16(data, subblockOffset+2)) + subblockOffset
			setCount := int(u16(data, subblockOffset+4))

			// parse coverage
			coverage, err := f.parseCoverage(data, coverageOffset)
			if err != nil {
				return err
			}
			if len(coverage) != setCount {
				return errorf("GSUB coverage length doesn't match set length")
			}
			if subblockOffset+6+setCount*2 > len(data) {
				return errorf("unexpected end of GSUB liga subblock at 0x%x", subblockOffset)
			}
			repl := make([]Index, setCount)
			for k := 0; k < len(repl); k++ {
				repl[k] = Index(u16(data, subblockOffset+6+2*k))
			}
			f.smcpBefore = coverage
			f.smcpAfter = repl
		}
	}
	return nil
}

func (f *Font) SmallCaps(glyphs []Index) []Index {
	if f.smcpBefore == nil {
		return glyphs
	}
	for i := range glyphs {
		for j := range f.smcpBefore {
			if f.smcpBefore[j] == glyphs[i] {
				glyphs[i] = f.smcpAfter[j]
			}
		}
	}
	return glyphs
}

func (f *Font) parseCoverage(data []byte, offset int) ([]Index, error) {
	if offset+4 > len(data) {
		return nil, errorf("unexpected end of coverage list at 0x%x", offset)
	}
	format := u16(data, offset)
	count := int(u16(data, offset+2))
	switch format {
	case 1:
		if offset+4+count*2 > len(data) {
			return nil, errorf("unexpected end of coverage list at 0x%x", offset)
		}
		glyphs := make([]Index, count)
		for i := range glyphs {
			glyphs[i] = Index(u16(data, offset+4+2*i))
		}
		return glyphs, nil
	case 2:
		if offset+4+count*6 > len(data) {
			return nil, errorf("unexpected end of coverage list at 0x%x", offset)
		}
		var glyphs []Index
		for i := 0; i < count; i++ {
			first := Index(u16(data, offset+4+6*i))
			last := Index(u16(data, offset+4+6*i+2))
			for g := first; g <= last; g++ {
				glyphs = append(glyphs, g)
			}
		}
		return glyphs, nil
	default:
		return nil, errorf("unsupported coverage format %d", format)
	}
}

func (f *Font) parseGpos() error {
	data := f.tables["GPOS"]
	if len(data) == 0 {
		return nil // GPOS block is optional
	}
	if len(data) < 10 {
		return errorf("GPOS block is too short (%d bytes)", len(data))
	}
	var (
		scriptTableOffset  = int(u16(data, 4))
		featureTableOffset = int(u16(data, 6))
		lookupTableOffset  = int(u16(data, 8))
	)

	featureIDs, err := f.parseScriptTable(data, scriptTableOffset, "", "")
	if err != nil {
		return err
	}

	lookupIDs, err := f.parseFeatureTable(data, featureTableOffset, featureIDs, "kern")
	if err != nil {
		return err
	}

	reverse := make([]rune, f.NumGlyphs())
	for i := 0; i < math.MaxUint16; i++ {
		reverse[f.Index(rune(i))] = rune(i)
	}

	for _, i := range lookupIDs {
		offset := int(u16(data, lookupTableOffset+2+i*2)) + lookupTableOffset

		if offset+6 > len(data) {
			return errorf("unexpected end of GPOS lookup entry at 0x%x", offset)
		}
		kind := int(u16(data, offset))
		subblockCount := int(u16(data, offset+4))
		if offset+6+subblockCount*2 > len(data) {
			return errorf("unexpected end of GPOS lookup entry at 0x%x", offset)
		}
		if kind != 2 {
			return errorf("unsupported GPOS lookup type %d", kind)
		}
		for j := 0; j < subblockCount; j++ {
			subblockOffset := int(u16(data, offset+6+j*2)) + offset
			if subblockOffset+2 > len(data) {
				return errorf("unexpected end of GPOS subblock at 0x%x", subblockOffset)
			}
			format := u16(data, subblockOffset)
			if format == 1 {
				if subblockOffset+10 > len(data) {
					return errorf("unexpected end of GPOS subblock at 0x%x", subblockOffset)
				}
				coverageOffset := int(u16(data, subblockOffset+2)) + subblockOffset
				format1 := u16(data, subblockOffset+4)
				format2 := u16(data, subblockOffset+6)
				pairSetCount := int(u16(data, subblockOffset+8))
				if format1 != 4 || format2 != 0 {
					return errorf("unsupported kern format %d %d", format1, format2)
				}
				// parse coverage
				if coverageOffset+4+2*pairSetCount > len(data) {
					return errorf("unexpected end of GPOS coverage at 0x%x", coverageOffset)
				}
				if format := u16(data, coverageOffset); format != 1 {
					return errorf("unsupported GPOS coverage format %d", format)
				}
				if count := int(u16(data, coverageOffset+2)); count != pairSetCount {
					return errorf("GPOS coverage length doesn't match pair set length")
				}
				coverage := make([]Index, pairSetCount)
				for k := 0; k < len(coverage); k++ {
					coverage[k] = Index(u16(data, coverageOffset+4+2*k))
				}
				// parse pair sets
				if subblockOffset+10+pairSetCount*2 > len(data) {
					return errorf("unexpected end of GPOS kern subblock at 0x%x", subblockOffset)
				}
				for k := 0; k < pairSetCount; k++ {
					pairSetOffset := int(u16(data, subblockOffset+10+k*2)) + subblockOffset
					if pairSetOffset+2 > len(data) {
						return errorf("unexpected end of GPOS pair set at 0x%x", pairSetOffset)
					}
					pairCount := int(u16(data, pairSetOffset))
					if pairSetOffset+2+4*pairCount > len(data) {
						return errorf("unexpected end of GPOS pair set at 0x%x", pairSetOffset)
					}
					// parse pairs
					for l := 0; l < pairCount; l++ {
						secondGlyph := Index(int(u16(data, pairSetOffset+2+4*l)))
						kern := int(int16(u16(data, pairSetOffset+2+4*l+2)))
						f.kern = append(f.kern, Kerning{coverage[k], secondGlyph, kern})
					}
				}
			} else if format == 2 {
				if subblockOffset+16 > len(data) {
					return errorf("unexpected end of GPOS subblock at 0x%x", subblockOffset)
				}
				format1 := u16(data, subblockOffset+4)
				format2 := u16(data, subblockOffset+6)
				classOffset1 := int(u16(data, subblockOffset+8)) + subblockOffset
				classOffset2 := int(u16(data, subblockOffset+10)) + subblockOffset
				classCount1 := u16(data, subblockOffset+12)
				classCount2 := u16(data, subblockOffset+14)
				if format1 != 4 || format2 != 0 {
					return errorf("unsupported kern format %d %d", format1, format2)
				}
				first, err := f.parseKernClassDef(data, classOffset1, classCount1)
				if err != nil {
					return err
				}
				second, err := f.parseKernClassDef(data, classOffset2, classCount2)
				if err != nil {
					return err
				}
				if subblockOffset+16+int(classCount1)*int(classCount2)*2 > len(data) {
					return errorf("unexpected end of GPOS subblock at 0x%x", subblockOffset)
				}
				table := make([]int, classCount1*classCount2)
				for k := 0; k < len(table); k++ {
					table[k] = int(int16(u16(data, subblockOffset+16+k*2)))
				}
				f.classKern = &classKerner{
					classA: first,
					classB: second,
					table:  table,
					countA: int(classCount1),
					countB: int(classCount2),
				}
			}
		}
	}
	return nil
}

func (f *Font) parseKernClassDef(data []byte, offset int, classCount uint16) ([]uint16, error) {
	if offset+4 > len(data) {
		return nil, errorf("unexpected end of class definition")
	}
	if format := u16(data, offset); format != 2 {
		return nil, errorf("unsupported class definition format %d", format)
	}
	count := int(u16(data, offset+2))
	if offset+4+count*6 > len(data) {
		return nil, errorf("unexpected end of class definition")
	}
	classes := make([]uint16, f.nGlyph)
	for k := 0; k < count; k++ {
		start := Index(u16(data, offset+4+k*6))
		end := Index(u16(data, offset+4+k*6+2))
		class := u16(data, offset+4+k*6+4)
		if end >= Index(len(classes)) {
			return nil, errorf("invalid glyph range %d %d", start, end)
		}
		if class >= classCount {
			return nil, errorf("invalid class number %d", class)
		}
		for l := start; l <= end; l++ {
			classes[l] = class
		}
	}
	return classes, nil
}

func (f *Font) parseScriptTable(data []byte, scriptTableOffset int, script string, lang string) ([]int, error) {
	// parse script list and locate the default script table
	if scriptTableOffset+2 > len(data) {
		return nil, errorf("unexpected end of GSUB script table")
	}
	scriptsCount := int(u16(data, scriptTableOffset))
	if scriptTableOffset+2+scriptsCount*6 > len(data) {
		return nil, errorf("unexpected end of GSUB script list with %d entries", scriptsCount)
	}
	scriptOffset := 0
	for i := 0; i < scriptsCount; i++ {
		x := scriptTableOffset + 2 + i*6
		if scriptTag := string(data[x : x+4]); scriptTag == "DFLT" {
			scriptOffset = int(u16(data, x+4))
		} else if scriptTag == script {
			scriptOffset = int(u16(data, x+4))
			break
		}
	}
	if scriptOffset <= 0 {
		return nil, errorf("no suitable script table not found")
	}
	scriptOffset += scriptTableOffset

	// parse script table and locate the default language/system table
	if scriptOffset+4 > len(data) {
		return nil, errorf("invalid script offset")
	}
	langSysOffset := int(u16(data, scriptOffset))
	langSysCount := int(u16(data, scriptOffset+2))
	if scriptOffset+4+6*langSysCount > len(data) {
		return nil, errorf("unexpected end of script table with %d entries", langSysCount)
	}
	for i := 0; i < langSysCount; i++ {
		x := scriptOffset + 4 + 6*i
		langID := string(data[x : x+4])
		if langID == lang {
			langSysOffset = int(u16(data, x+4))
			break
		} else if langID == "dflt" {
			langSysOffset = int(u16(data, x+4))
		}
	}
	if langSysOffset <= 0 {
		return nil, errorf("no suitable language/system table found")
	}
	langSysOffset += scriptOffset

	// parse language/system table to get all features
	var featureIDs []int
	if langSysOffset+6 > len(data) {
		return nil, errorf("invalid langSysOffset 0x%x", langSysOffset)
	}
	if required := u16(data, langSysOffset+2); required != math.MaxUint16 {
		featureIDs = append(featureIDs, int(required))
	}
	featureTableCount := int(u16(data, langSysOffset+4))
	if langSysOffset+6+featureTableCount*2 > len(data) {
		return nil, errorf("unexpected end of lang/sys table with %d entries", featureTableCount)
	}
	for i := 0; i < featureTableCount; i++ {
		featureIDs = append(featureIDs, int(u16(data, langSysOffset+6+i*2)))
	}

	return featureIDs, nil
}

func (f *Font) parseFeatureTable(data []byte, featureTableOffset int, featureIDs []int, feature string) ([]int, error) {
	// parse feature table
	if featureTableOffset+2 > len(data) {
		return nil, errorf("invalid feature table at 0x%x", featureTableOffset)
	}
	featureCount := int(u16(data, featureTableOffset))
	if featureTableOffset+2+featureCount*6 > len(data) {
		return nil, errorf("unexpected end of feature table with %d entries", featureCount)
	}
	lookupOffset := -1
	for _, id := range featureIDs {
		if id >= featureCount {
			return nil, errorf("can not find feature %d in GSUB feature table", id)
		}
		x := featureTableOffset + 2 + id*6
		if name := string(data[x : x+4]); name == feature {
			lookupOffset = int(u16(data, x+4))
		}
	}
	if lookupOffset < 0 {
		return nil, errorf("feature %q not found", feature)
	}
	lookupOffset += featureTableOffset

	// parse lookup list
	if lookupOffset+4 > len(data) {
		return nil, errorf("invalid lookup list at 0x%x", lookupOffset)
	}
	lookupListCount := int(u16(data, lookupOffset+2))
	if lookupOffset+4+2*lookupListCount > len(data) {
		return nil, errorf("unexpected end of lookup list at 0x%x", lookupOffset)
	}
	lookupIDs := make([]int, lookupListCount)
	for i := 0; i < len(lookupIDs); i++ {
		lookupIDs[i] = int(u16(data, lookupOffset+4+2*i))
	}
	return lookupIDs, nil
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

func (f *Font) parsePost(post []byte) error {
	if len(post) < 16 {
		return FontError("TTF post block is too short")
	}
	f.ItalicAngle = float32(int16(u16(post, 4))) + float32(u16(post, 6))/math.MaxUint16
	return nil
}

func (f *Font) Scale(value, scale int) int {
	return (value * scale) / f.UnitsPerEm
}

// Kerning returns the kerning for the given glyph pair.
func (f *Font) Kerning(scale int, a, b Index) int {
	kern := 0
	for i := 0; i < len(f.kern); i++ {
		if f.kern[i].First == a && f.kern[i].Second == b {
			kern += f.kern[i].Horiz
		}
	}
	if f.classKern != nil {
		kern += f.classKern.Kern(a, b)
	}
	return f.Scale(kern, scale)
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

func (f *Font) parseHead() error {
	const (
		tableVersion uint32 = 0x00010000
	)
	if version := u32(f.head, 0); version != tableVersion {
		return errorf("invalid head block version 0x%x", version)
	}
	if len(f.head) != 54 {
		return errorf("invalid head block length %d", len(f.head))
	}
	f.UnitsPerEm = int(u16(f.head, 18))
	f.XMin = int(int16(u16(f.head, 36)))
	f.YMin = int(int16(u16(f.head, 38)))
	f.XMax = int(int16(u16(f.head, 40)))
	f.YMax = int(int16(u16(f.head, 42)))
	return nil
}

func (f *Font) parseOS2() error {
	if len(f.os2) < 72 {
		return errorf("OS/2 block is too short")
	}
	version := u16(f.os2, 0)
	f.Ascender = int(int16(u16(f.os2, 68)))
	f.Descender = int(int16(u16(f.os2, 70)))
	if version >= 2 && len(f.os2) >= 90 {
		f.CapHeight = int(int16(u16(f.os2, 88)))
	} else {
		f.CapHeight = f.Ascender
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

func (f *Font) Ligatures(glyphs []Index) []Index {
	if len(f.liga) == 0 {
		return glyphs
	}
	for i := 0; i < len(glyphs); i++ {
		for _, liga := range f.liga {
			if i+len(liga.Old) > len(glyphs) {
				continue
			}
			found := true
			for k := 0; k < len(liga.Old); k++ {
				if liga.Old[k] != glyphs[i+k] {
					found = false
					break
				}
			}
			if !found {
				continue
			}
			glyphs[i] = liga.New
			glyphs = append(glyphs[:i+1], glyphs[i+len(liga.Old):]...)
			break
		}
	}
	return glyphs
}

func (f *Font) StringToGlyphs(text string) []Index {
	var glyphs []Index
	for _, r := range text {
		glyphs = append(glyphs, f.Index(r))
	}
	return glyphs
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

// An Index is a Font's index of a rune.
type Index uint16

// An HMetric holds the horizontal metrics of a single glyph.
type HMetric struct {
	Width, Left int
}

// A cm holds a parsed cmap entry.
type cm struct {
	start, end, delta, offset uint32
}

type Ligature struct {
	Old []Index
	New Index
}

type Kerner interface {
	Kern(a, b Index) int
}

type classKerner struct {
	classA []uint16
	classB []uint16
	table  []int
	countA int
	countB int
}

func (c *classKerner) Kern(a, b Index) int {
	if int(a) >= len(c.classA) || int(b) >= len(c.classB) {
		return 0
	}
	return c.table[int(c.classA[a])+int(c.classB[b])*c.countA]
}

type Kerning struct {
	First  Index
	Second Index
	Horiz  int
}

// FontError is used to report various errors about invalid TTF and OTF files.
type FontError string

// Error returns the detailed error message.
func (e FontError) Error() string {
	return string(e)
}

// errorf constructs a formatted FontError.
func errorf(format string, values ...interface{}) FontError {
	return FontError(fmt.Sprintf(format, values...))
}

// u32 returns the big-endian uint32 at b[i:].
func u32(b []byte, i int) uint32 {
	return uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3])
}

// u16 returns the big-endian uint16 at b[i:].
func u16(b []byte, i int) uint16 {
	return uint16(b[i])<<8 | uint16(b[i+1])
}
