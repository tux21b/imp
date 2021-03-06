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

/*

type Point struct {
	X int
	Y int
}

func P(x, y float32) Point {
	return Point{mm(x), mm(y)}
}

type Box struct {
	Size   Point
	Pos    Point
	Anchor Point
}

func (b *Box) Box() *Box {
	return b
}

type Object interface {
	Box() *Box
}

type Container interface {
	Object
	Children() []Object
}

type HBox struct {
	box  Box
	objs []Object
}

func NewHBox(objs ...Object) *HBox {
	h := &HBox{objs: objs}
	maxAsc, maxDesc := 0, 0
	for i := range h.objs {
		b := h.objs[i].Box()
		if b.Anchor.Y > maxDesc {
			maxDesc = b.Anchor.Y
		}
		if b.Size.Y-b.Anchor.Y > maxAsc {
			maxAsc = b.Size.Y - b.Anchor.Y
		}
	}
	x := 0
	for i := range h.objs {
		b := h.objs[i].Box()
		b.Pos.X = x
		b.Pos.Y = maxDesc - b.Anchor.Y
		x += b.Size.X
	}
	h.box.Size.X = x
	h.box.Size.Y = maxAsc + maxDesc
	h.box.Anchor.Y = maxDesc
	return h
}

func (h *HBox) Box() *Box {
	return &h.box
}

func (h *HBox) Children() []Object {
	return h.objs
}

type VBox struct {
	box  Box
	objs []Object
}

func NewVBox(objs ...Object) *VBox {
	v := &VBox{objs: objs}
	maxAsc, maxDesc := 0, 0
	for i := range v.objs {
		b := v.objs[i].Box()
		if b.Anchor.X > maxDesc {
			maxDesc = b.Anchor.X
		}
		if b.Size.X-b.Anchor.X > maxAsc {
			maxAsc = b.Size.X - b.Anchor.X
		}
	}
	y := 0
	for i := range v.objs {
		b := v.objs[i].Box()
		b.Pos.X = maxDesc - b.Anchor.X
		b.Pos.Y = y
		y += b.Size.Y
	}
	v.box.Size.X = maxAsc + maxDesc
	v.box.Size.Y = y
	v.box.Anchor.X = maxDesc
	return v
}

func (v *VBox) Box() *Box {
	return &v.box
}

func (v *VBox) Children() []Object {
	return v.objs
}

*/
