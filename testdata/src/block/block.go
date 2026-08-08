package block

import "bytes"

func use(...any) {}

func f() int { return 1 }

func valueFirst() {
	var (
		a = f()
		b int
		c string
	)
	use(a, b, c, a)
}

func typedValueFirst() {
	var (
		a       = f()
		b int32 = 2
		c int32 = 3
	)
	use(a, b, c)
}

func gap() {
	var (
		a int
		b string
		c = f()
	)
	use(a, b, c, c)
}

func good() {
	var (
		a int
		b string

		c = f()
	)
	use(a, b, c, c)
}

func dependent() {
	var (
		c string

		a       = f()
		b int32 = int32(a) // want `redundant type can be elided: b = int32\(a\)`
	)
	use(a, b, c)
}

func merge() {
	var a int // want `2 adjacent declarations can be grouped into a var block`
	var (
		b int
	)
	use(a, b)
}

func pairs() {
	var (
		x int // want `zero-value declarations of the same type can be combined: x, z int`
		y string
		z int
	)
	use(x, y, z)
}

func commentSpacing() {
	var (
		a int
		// b carries documentation.
		b string // want `commented declaration should be preceded by a blank line`
	)
	use(a, b)
}

func singletons() {
	var (
		z int

		w int32 = 2
		v       = f()
	)
	use(z, w, v)
}

func packTight() {
	var (
		a int

		b = f()
	)
	use(a, b)
}

func elide() {
	var (
		w    int32 = 2
		year int   = -1 // want `redundant type can be elided: year = -1`
	)
	use(w, year)
}

func typeDependent() {
	var (
		subtypes = [...]int{1, 2, 3}
		dat      [len(subtypes)][]byte
	)
	use(subtypes, dat)
}

func separated() {
	var (
		buf bytes.Buffer
	)
	buf.WriteString("x")
	var (
		text = buf.String() // want `var block outside the preamble can be dissolved into standalone declarations`
	)
	use(text)
}

func localType() {
	var (
		n = f()
	)
	type pair struct{ a, b int }
	var (
		p pair // want `var block outside the preamble can be dissolved into standalone declarations`
	)
	use(n, p)
}

func widened() {
	var (
		a     = f()
		b any = f()
	)
	use(a, b)
}

func ownedNew() {
	var (
		a int               // want `zero-value declarations of the same type can be combined: a, c int`
		b = &bytes.Buffer{} // want `&bytes.Buffer\{\} can be new\(bytes.Buffer\)`
		c int
	)
	use(a, b, c)
}

func shadowPairs() {
	n := f()
	if n > 0 {
		var (
			a int // want `zero-value declarations of the same type can be combined: a, n int`
			b = n
			n int
		)
		use(a, b, n)
	}
}

func emptyBlock() {
	var ()
}
