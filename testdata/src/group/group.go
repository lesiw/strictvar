package group

func use(...any) {}

func f() int { return 1 }

func run3() {
	var a int // want `3 adjacent declarations can be grouped into a var block`
	var b string
	var c = f()
	use(a, b, c, c)
}

func pair() {
	var x int
	var y int // want `zero-value declarations of the same type can be combined: var x, y int`
	use(x, y)
}

func pairDifferentTypes() {
	var x int // want `2 adjacent declarations can be grouped into a var block`
	var y string
	use(x, y)
}

func consts() {
	const a = 1 // want `3 adjacent const declarations can be grouped into a const block`
	const b = 2
	const c = 3
	use(a, b, c)
}

func iotas() {
	const a = iota
	const b = iota
	use(a, b)
}

func interrupted() {
	var a int
	use(a)
	var b int
	use(b)
	var c int
	use(c)
}

func nonZero() {
	var x = 42 // want `non-zero value can be declared with :=: x := 42`
	use(x, x)
}

func nonZeroTyped() {
	var d int32 = 42
	use(d, d)
}

func preamble() {
	n := f() // want `3 adjacent declarations can be grouped into a var block`
	var s string
	var (
		t int
	)
	use(n, s, t)
}

func preambleTwoNoBlock() {
	n := f() // want `2 adjacent declarations can be grouped into a var block`
	m := f()
	use(n, m)
}

func preambleNames() {
	a := f() // want `2 adjacent declarations can be grouped into a var block`
	var x, y int
	use(a, x, y)
}

func midScope() {
	use(1)
	x := 42
	y := 69
	z := 420
	use(x, y, z)
}

func midScopeCall() {
	use(1)
	a := f()
	var s, t int
	use(a, s, t)
}

func midScopeBuiltin() {
	use(1)
	a := make([]int, 3)
	var s, t int
	use(a, s, t)
}

func midScopePair() {
	use(1)
	var x int
	var y int // want `zero-value declarations of the same type can be combined: var x, y int`
	use(x, y)
}

func midScopeTriple() {
	use(1)
	var i int
	var j int
	var k int // want `zero-value declarations of the same type can be combined: var i, j, k int`
	use(i, j, k)
}

func midScopeMixed() {
	use(1)
	var i int
	var s string
	use(i, s)
}

func midScopePairComment() {
	use(1)
	var i int // i is tracked.
	var j int // want `zero-value declarations of the same type can be combined: var i, j int`
	use(i, j)
}

func preambleTriple() {
	var i int
	var j int
	var k int // want `zero-value declarations of the same type can be combined: var i, j, k int`
	use(i, j, k)
}

func midScopeConsts() {
	use(1)
	const a = 1 // want `2 adjacent const declarations can be grouped into a const block`
	const b = 2
	use(a, b)
}

func midScopeConstBlock() {
	use(1)
	const (
		c = 3
	)
	use(c)
}

func fallible() (int, error) { return 1, nil }

func errStops() {
	x := f() // want `2 adjacent declarations can be grouped into a var block`
	var s, t int
	v, err := fallible()
	use(x, s, t, v, err)
}

func docComments() {
	// a is documented.
	var a int
	// b is documented.
	var b int
	// c is documented.
	var c int
	use(a, b, c)
}

func commentSeparated() {
	a := f()
	// b begins the second act.
	b := f()
	use(a, b)
}

func preambleHeaderComment() {
	n := f() // want `2 adjacent declarations can be grouped into a var block`
	var (    // pinned note
		s int
	)
	use(n, s)
}

func pairCommentSeparated() {
	var x int

	// y stands apart from x.

	var y int
	use(x, y)
}

const alpha = 1

// Floating commentary between the constants.

const beta = 2
