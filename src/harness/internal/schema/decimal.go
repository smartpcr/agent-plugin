package schema

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// decimal is an exact JSON number: digits × 10^exp, with digits free of leading and
// trailing zeros. Zero has no digits and is never negative. Comparing two decimals
// is exact; no number is rounded through float64 (9007199254740993 stays distinct
// from 9007199254740992, and 0.3 is a multiple of 0.1).
type decimal struct {
	neg    bool
	digits string
	exp    int64
}

// maxExp clamps exponents so a pathological input like 1e999999999999999999 costs
// nothing: numbers past it compare as the clamped value.
const maxExp = 1 << 40

// parseDecimal reads a JSON number. It reports false for anything else.
func parseDecimal(s string) (decimal, bool) {
	var d decimal
	if strings.HasPrefix(s, "-") {
		d.neg, s = true, s[1:]
	}
	mant, expPart, hasExp := strings.Cut(strings.ToLower(s), "e")
	intPart, frac, _ := strings.Cut(mant, ".")
	if intPart == "" || !allDigits(intPart) || !allDigits(frac) || (strings.Contains(mant, ".") && frac == "") {
		return decimal{}, false
	}
	if hasExp {
		e, ok := parseExp(expPart)
		if !ok {
			return decimal{}, false
		}
		d.exp = e
	}
	digits := intPart + frac
	d.exp -= int64(len(frac))
	trimmed := strings.TrimRight(digits, "0")
	d.exp += int64(len(digits) - len(trimmed))
	d.digits = strings.TrimLeft(trimmed, "0")
	if d.digits == "" {
		return decimal{}, true
	}
	d.exp = max(-maxExp, min(maxExp, d.exp))
	return d, true
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseExp reads an exponent, saturating at ±maxExp.
func parseExp(s string) (int64, bool) {
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = true, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if s == "" || !allDigits(s) {
		return 0, false
	}
	s = strings.TrimLeft(s, "0")
	e := int64(maxExp)
	if len(s) < 14 {
		e, _ = strconv.ParseInt("0"+s, 10, 64)
		e = min(e, maxExp)
	}
	if neg {
		e = -e
	}
	return e, true
}

// toDecimal converts a JSON number (a json.Number) or a Go number from a schema
// document. A float64 is read as its shortest decimal form, the literal it was
// written as. It reports false for any other value, NaN and ±Inf.
func toDecimal(v any) (decimal, bool) {
	switch x := v.(type) {
	case json.Number:
		return parseDecimal(string(x))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return decimal{}, false
		}
		return parseDecimal(strconv.FormatFloat(x, 'g', -1, 64))
	case float32:
		return toDecimal(float64(x))
	case int:
		return parseDecimal(strconv.FormatInt(int64(x), 10))
	case int64:
		return parseDecimal(strconv.FormatInt(x, 10))
	case int32:
		return parseDecimal(strconv.FormatInt(int64(x), 10))
	case uint64:
		return parseDecimal(strconv.FormatUint(x, 10))
	case uint:
		return parseDecimal(strconv.FormatUint(uint64(x), 10))
	}
	return decimal{}, false
}

func (d decimal) isZero() bool { return d.digits == "" }

// isInteger reports whether d has no fractional part.
func (d decimal) isInteger() bool { return d.isZero() || d.exp >= 0 }

// cmp returns -1, 0 or +1 as d is below, equal to or above e.
func (d decimal) cmp(e decimal) int {
	if d.neg != e.neg {
		if d.neg {
			return -1
		}
		return 1
	}
	c := d.cmpAbs(e)
	if d.neg {
		return -c
	}
	return c
}

func (d decimal) cmpAbs(e decimal) int {
	switch {
	case d.isZero() && e.isZero():
		return 0
	case d.isZero():
		return -1
	case e.isZero():
		return 1
	}
	// The magnitude is the position of the leading digit.
	if dm, em := int64(len(d.digits))+d.exp, int64(len(e.digits))+e.exp; dm != em {
		if dm < em {
			return -1
		}
		return 1
	}
	a, b := d.digits, e.digits
	n := max(len(a), len(b))
	a += strings.Repeat("0", n-len(a))
	b += strings.Repeat("0", n-len(b))
	return strings.Compare(a, b)
}

// multipleOf reports whether d is an integer multiple of the positive m.
func (d decimal) multipleOf(m decimal) bool {
	if d.isZero() {
		return true
	}
	if m.isZero() {
		return false
	}
	dn, _ := new(big.Int).SetString(d.digits, 10)
	mn, _ := new(big.Int).SetString(m.digits, 10)
	shift := d.exp - m.exp // d/m = dn/mn × 10^shift
	if shift >= 0 {
		// mn = 2^a·5^b·r: past max(a, b) more powers of ten change nothing, and
		// a, b ≤ log2(mn) < 4·len(digits).
		shift = min(shift, int64(4*len(m.digits)))
		dn.Mul(dn, new(big.Int).Exp(big.NewInt(10), big.NewInt(shift), nil))
	} else {
		// mn·10^-shift exceeds dn once -shift passes dn's digit count.
		if -shift > int64(len(d.digits)) {
			return false
		}
		mn.Mul(mn, new(big.Int).Exp(big.NewInt(10), big.NewInt(-shift), nil))
	}
	return new(big.Int).Mod(dn, mn).Sign() == 0
}
