// Copyright (c) 2014 by Christoph Hack <christoph@tux21b.org>
// All rights reserved. Distributed under the Simplified BSD License.

package main

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type lengthUnit int

const (
	Fit          lengthUnit = 0
	Expand       lengthUnit = 1
	Exact        lengthUnit = 2
	Proportional lengthUnit = 3
)

type Length struct {
	Value    float32
	Unit     lengthUnit
	Computed float32
}

func ParseLength(s string) (Length, error) {
	var value float32

	s = strings.TrimSpace(s)
	split := 0
	for split < len(s) {
		r, n := utf8.DecodeRuneInString(s[split:])
		if r != '.' && !unicode.IsNumber(r) {
			break
		}
		split += n
	}
	if split > 0 {
		v, err := strconv.ParseFloat(s[:split], 32)
		if err != nil {
			return Length{}, err
		}
		value = float32(v)
	}
	unit := strings.TrimSpace(s[split:])
	switch unit {
	case "mm":
		return Length{value * 72.0 / 25.4, Exact, value * 72.0 / 25.4}, nil
	case "cm":
		return Length{value * 720 / 25.4, Exact, value * 720 / 25.4}, nil
	}
	return Length{}, errors.New("invalid length")
}

func MustParseLength(s string) Length {
	length, err := ParseLength(s)
	if err != nil {
		panic(err.Error())
	}
	return length
}

type Box struct {
	MarginTop, MarginRight, MarginBottom, MarginLeft     Length
	PaddingTop, PaddingRight, PaddingBottom, PaddingLeft Length
	Width, Height                                        Length
}

func (b *Box) TotalWidth() float32 {
	return b.MarginLeft.Computed + b.PaddingLeft.Computed + b.Width.Computed + b.PaddingRight.Computed + b.MarginRight.Computed
}

func (b *Box) TotalHeight() float32 {
	return b.MarginTop.Computed + b.PaddingTop.Computed + b.Height.Computed + b.PaddingBottom.Computed + b.MarginBottom.Computed
}
