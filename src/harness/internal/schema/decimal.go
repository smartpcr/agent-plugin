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
//
// The exponent is kept exactly, as a canonical base-10 integer string ("" for
// zero), so 1e1099511627776 and 1e1099511627777 stay distinct however large the
// exponent; it is never expanded into a power of ten. The struct stays comparable,
// so equal decimals are == and reflect.DeepEqual.
type decimal struct {
	neg    bool
	digits string
	exp    string
}

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
	exp := new(big.Int)
	if hasExp {
		if !validExp(expPart) {
			return decimal{}, false
		}
		exp.SetString(strings.TrimPrefix(expPart, "+"), 10)
	}
	digits := intPart + frac
	trimmed := strings.TrimRight(digits, "0")
	exp.Add(exp, big.NewInt(int64(len(digits)-len(trimmed)-len(frac))))
	d.digits = strings.TrimLeft(trimmed, "0")
	if d.digits == "" {
		return decimal{}, true
	}
	d.exp = exp.String()
	return d, true
}

func (d decimal) expInt() *big.Int {
	e, _ := new(big.Int).SetString(d.exp, 10)
	if e == nil {
		return new(big.Int)
	}
	return e
}

// magnitude is the position of d's leading digit: len(digits) + exp.
func (d decimal) magnitude() *big.Int {
	return d.expInt().Add(d.expInt(), big.NewInt(int64(len(d.digits))))
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validExp reports whether s is a JSON exponent: an optional sign, then digits.
func validExp(s string) bool {
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	return s != "" && allDigits(s)
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
func (d decimal) isInteger() bool { return d.isZero() || !strings.HasPrefix(d.exp, "-") }

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
	if c := d.magnitude().Cmp(e.magnitude()); c != 0 {
		return c
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
	shift := d.expInt().Sub(d.expInt(), m.expInt()) // d/m = dn/mn × 10^shift
	if shift.Sign() >= 0 {
		// mn = 2^a·5^b·r: past max(a, b) more powers of ten change nothing, and
		// a, b ≤ log2(mn) < 4·len(digits).
		if limit := big.NewInt(int64(4 * len(m.digits))); shift.Cmp(limit) > 0 {
			shift = limit
		}
		dn.Mul(dn, new(big.Int).Exp(big.NewInt(10), shift, nil))
	} else {
		// mn·10^-shift exceeds dn once -shift passes dn's digit count.
		shift.Neg(shift)
		if shift.Cmp(big.NewInt(int64(len(d.digits)))) > 0 {
			return false
		}
		mn.Mul(mn, new(big.Int).Exp(big.NewInt(10), shift, nil))
	}
	return new(big.Int).Mod(dn, mn).Sign() == 0
}
