package script

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

var errInvalidTTF = errors.New("invalid ttf font")

// ttfFont 移植 Legado QueryTTF 的查询能力：cmap(format 0/4/6) 建立 Unicode→字形索引，
// glyf 轮廓数据作为字形指纹，用于 replaceFont 在两套字体间反查 Unicode。
type ttfFont struct {
	unicodeToGlyphID map[int]int
	unicodeToGlyph   map[int]string
	glyphToUnicode   map[string]int
	glyphs           []string
}

type ttfReader struct {
	data []byte
	err  error
}

func (r *ttfReader) u16(offset int) int {
	if r.err != nil || offset < 0 || offset+2 > len(r.data) {
		r.err = errInvalidTTF
		return 0
	}
	return int(binary.BigEndian.Uint16(r.data[offset:]))
}

func (r *ttfReader) i16(offset int) int {
	return int(int16(r.u16(offset)))
}

func (r *ttfReader) u32(offset int) int {
	if r.err != nil || offset < 0 || offset+4 > len(r.data) {
		r.err = errInvalidTTF
		return 0
	}
	return int(binary.BigEndian.Uint32(r.data[offset:]))
}

func parseTTF(data []byte) (*ttfFont, error) {
	r := &ttfReader{data: data}
	numTables := r.u16(4)
	tables := map[string][2]int{}
	for i := 0; i < numTables && r.err == nil; i++ {
		base := 12 + i*16
		if base+16 > len(data) {
			return nil, errInvalidTTF
		}
		tables[string(data[base:base+4])] = [2]int{r.u32(base + 8), r.u32(base + 12)}
	}
	for _, tag := range []string{"head", "maxp", "cmap", "loca", "glyf"} {
		table, ok := tables[tag]
		if !ok || table[0]+table[1] > len(data) {
			return nil, fmt.Errorf("%w: missing %s table", errInvalidTTF, tag)
		}
	}
	font := &ttfFont{unicodeToGlyphID: map[int]int{}, unicodeToGlyph: map[int]string{}, glyphToUnicode: map[string]int{}}
	font.readCmap(r, tables["cmap"][0])
	numGlyphs := r.u16(tables["maxp"][0] + 4)
	longLoca := r.i16(tables["head"][0]+50) != 0
	locaBase, glyfBase, glyfLen := tables["loca"][0], tables["glyf"][0], tables["glyf"][1]
	offsets := make([]int, numGlyphs+1)
	for i := range offsets {
		if longLoca {
			offsets[i] = r.u32(locaBase + i*4)
		} else {
			offsets[i] = r.u16(locaBase+i*2) * 2
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	font.glyphs = make([]string, numGlyphs)
	for i := 0; i < numGlyphs; i++ {
		start, end := offsets[i], offsets[i+1]
		if start >= end {
			continue
		}
		if end > glyfLen {
			return nil, fmt.Errorf("%w: glyph %d out of range", errInvalidTTF, i)
		}
		glyph, err := glyphFingerprint(data[glyfBase+start : glyfBase+end])
		if err != nil {
			return nil, err
		}
		font.glyphs[i] = glyph
	}
	unicodes := make([]int, 0, len(font.unicodeToGlyphID))
	for unicode := range font.unicodeToGlyphID {
		unicodes = append(unicodes, unicode)
	}
	sort.Ints(unicodes)
	for _, unicode := range unicodes {
		id := font.unicodeToGlyphID[unicode]
		if id >= numGlyphs {
			continue
		}
		glyph := font.glyphs[id]
		font.unicodeToGlyph[unicode] = glyph
		if glyph != "" {
			font.glyphToUnicode[glyph] = unicode
		}
	}
	return font, nil
}

func (f *ttfFont) readCmap(r *ttfReader, base int) {
	numTables := r.u16(base + 2)
	seen := map[int]bool{}
	for i := 0; i < numTables && r.err == nil; i++ {
		offset := base + r.u32(base+4+i*8+4)
		if seen[offset] {
			continue
		}
		seen[offset] = true
		switch r.u16(offset) {
		case 0:
			length := r.u16(offset + 2)
			for code := 0; code < length-6 && offset+6+code < len(r.data); code++ {
				if id := int(r.data[offset+6+code]); id != 0 {
					f.unicodeToGlyphID[code] = id
				}
			}
		case 4:
			length := r.u16(offset + 2)
			segCount := r.u16(offset+6) / 2
			endBase := offset + 14
			startBase := endBase + segCount*2 + 2
			deltaBase := startBase + segCount*2
			rangeBase := deltaBase + segCount*2
			glyphBase := rangeBase + segCount*2
			glyphCount := (length - 16 - segCount*8) / 2
			for seg := 0; seg < segCount && r.err == nil; seg++ {
				start, end := r.u16(startBase+seg*2), r.u16(endBase+seg*2)
				delta, rangeOffset := r.i16(deltaBase+seg*2), r.u16(rangeBase+seg*2)
				for code := start; code <= end && r.err == nil; code++ {
					id := 0
					if rangeOffset == 0 {
						id = (code + delta) & 0xFFFF
					} else if index := rangeOffset/2 + code - start + seg - segCount; index >= 0 && index < glyphCount {
						id = r.u16(glyphBase+index*2) + delta
					}
					if id != 0 {
						f.unicodeToGlyphID[code] = id
					}
				}
			}
		case 6:
			first, count := r.u16(offset+6), r.u16(offset+8)
			for i := 0; i < count && r.err == nil; i++ {
				f.unicodeToGlyphID[first+i] = r.u16(offset + 10 + i*2)
			}
		}
	}
}

// glyphFingerprint 生成字形指纹：简单字形为各点坐标增量 "x,y|x,y"（同 QueryTTF.getGlyfById），复合字形为组件记录的十六进制。
func glyphFingerprint(glyph []byte) (string, error) {
	r := &ttfReader{data: glyph}
	contours := r.i16(0)
	if r.err != nil {
		return "", r.err
	}
	if contours < 0 {
		if len(glyph) <= 10 {
			return "", errInvalidTTF
		}
		return "c:" + hex.EncodeToString(glyph[10:]), nil
	}
	points := 0
	if contours > 0 {
		points = r.u16(10+(contours-1)*2) + 1
	}
	pos := 10 + contours*2
	pos += 2 + r.u16(pos)
	flags := make([]byte, 0, points)
	for len(flags) < points {
		if pos >= len(glyph) {
			return "", errInvalidTTF
		}
		flag := glyph[pos]
		pos++
		flags = append(flags, flag)
		if flag&0x08 != 0 {
			if pos >= len(glyph) {
				return "", errInvalidTTF
			}
			for repeat := int(glyph[pos]); repeat > 0 && len(flags) < points; repeat-- {
				flags = append(flags, flag)
			}
			pos++
		}
	}
	readAxis := func(shortBit, sameBit byte) ([]int, error) {
		values := make([]int, points)
		for i, flag := range flags {
			switch {
			case flag&shortBit != 0:
				if pos >= len(glyph) {
					return nil, errInvalidTTF
				}
				value := int(glyph[pos])
				pos++
				if flag&sameBit == 0 {
					value = -value
				}
				values[i] = value
			case flag&sameBit != 0:
				values[i] = 0
			default:
				values[i] = r.i16(pos)
				pos += 2
			}
		}
		return values, r.err
	}
	xs, err := readAxis(0x02, 0x10)
	if err != nil {
		return "", err
	}
	ys, err := readAxis(0x04, 0x20)
	if err != nil {
		return "", err
	}
	parts := make([]string, points)
	for i := range parts {
		parts[i] = strconv.Itoa(xs[i]) + "," + strconv.Itoa(ys[i])
	}
	return strings.Join(parts, "|"), nil
}

func (f *ttfFont) GlyfIDByUnicode(unicode int) int {
	return f.unicodeToGlyphID[unicode]
}

func (f *ttfFont) GlyfByUnicode(unicode int) (string, bool) {
	glyph, ok := f.unicodeToGlyph[unicode]
	return glyph, ok && glyph != ""
}

func (f *ttfFont) UnicodeByGlyf(glyph string) int {
	return f.glyphToUnicode[glyph]
}

// isBlankUnicode 对齐 QueryTTF.isBlankUnicode。
func isBlankUnicode(unicode int) bool {
	switch unicode {
	case 0x0009, 0x0020, 0x00A0, 0x2002, 0x2003, 0x2007, 0x200A, 0x200B, 0x200C, 0x200D, 0x202F, 0x205F:
		return true
	}
	return false
}

// replaceFont 移植 JsExtensions.replaceFont：按错误字体的字形指纹在正确字体中反查真实字符。
func replaceFont(text string, errorFont *ttfFont, correctFont *ttfFont, filter bool) string {
	if errorFont == nil || correctFont == nil {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, char := range text {
		code := int(char)
		if isBlankUnicode(code) {
			b.WriteRune(char)
			continue
		}
		glyph, ok := errorFont.GlyfByUnicode(code)
		if errorFont.GlyfIDByUnicode(code) == 0 {
			ok = false
		}
		if filter && !ok {
			continue
		}
		if ok {
			if mapped := correctFont.UnicodeByGlyf(glyph); mapped != 0 {
				b.WriteRune(rune(mapped))
				continue
			}
		}
		b.WriteRune(char)
	}
	return b.String()
}

const ttfHandleProperty = "__ttf"

// queryTTFValue 对齐 JsExtensions.queryTTF：接受 URL、base64 或字节数组，返回字体查询对象；null 输入返回 null。
func (r *Runtime) queryTTFValue(runCtx evalContext, vm *goja.Runtime, value goja.Value) goja.Value {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return goja.Null()
	}
	var data []byte
	if text, ok := value.Export().(string); ok {
		if isAbsURL(text) {
			body, _, err := r.fetchBytes(runCtx, vm, text)
			if err != nil {
				throwJS(vm, err)
			}
			data = body
		} else {
			data = base64DecodeLenient(text)
		}
	} else {
		data = bytesFromJS(value)
	}
	if len(data) == 0 {
		return goja.Null()
	}
	font, err := parseTTF(data)
	if err != nil {
		throwJS(vm, err)
	}
	obj := vm.NewObject()
	_ = obj.DefineDataProperty(ttfHandleProperty, vm.ToValue(font), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	_ = obj.Set("getGlyfIdByUnicode", font.GlyfIDByUnicode)
	_ = obj.Set("getGlyfByUnicode", func(unicode int) goja.Value {
		if glyph, ok := font.GlyfByUnicode(unicode); ok {
			return vm.ToValue(glyph)
		}
		return goja.Null()
	})
	_ = obj.Set("getUnicodeByGlyf", font.UnicodeByGlyf)
	_ = obj.Set("getGlyfById", func(id int) goja.Value {
		if id < 0 || id >= len(font.glyphs) || font.glyphs[id] == "" {
			return goja.Null()
		}
		return vm.ToValue(font.glyphs[id])
	})
	_ = obj.Set("isBlankUnicode", isBlankUnicode)
	return obj
}

// exportedTTF 取回 queryTTF 返回对象中的字体；非字体对象返回 nil（replaceFont 原样返回文本）。
func exportedTTF(value goja.Value) *ttfFont {
	obj, ok := value.(*goja.Object)
	if !ok {
		return nil
	}
	handle := obj.Get(ttfHandleProperty)
	if handle == nil {
		return nil
	}
	font, _ := handle.Export().(*ttfFont)
	return font
}
