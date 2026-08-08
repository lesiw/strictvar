package zero

import "time"

type T struct{ a int }

func use(...any) {}

var gx = 0 // want `zero value can be declared with var: var gx int`

// ge exercises the typed-nil form alone.
var ge error = nil // want `zero value can be declared without a value: var ge error`

func basics() {
	x := 0     // want `zero value can be declared with var: var x int`
	f := 0.0   // want `zero value can be declared with var: var f float64`
	s := ""    // want `zero value can be declared with var: var s string`
	b := false // want `zero value can be declared with var: var b bool`
	use(x, f, s, b, x, f, s, b)
}

func structs() {
	t := T{}  // want `zero value can be declared with var: var t T`
	p := &T{} // want `&T\{\} can be new\(T\)`
	use(t, p, t, p)
}

func conversions() {
	d := time.Duration(0) // want `zero value can be declared with var: var d time.Duration`
	bs := []byte(nil)     // want `zero value can be declared with var: var bs \[\]byte`
	e := error(nil)       // want `zero value can be declared with var: var e error`
	use(d, bs, e, d, bs, e)
}

func typed() {
	var i int = 0 // want `zero value can be declared without a value: var i int`
	// e exercises the typed-nil form alone.
	var e error = nil // want `zero value can be declared without a value: var e error`
	use(i, e, i, e)
}

func notZero() {
	m := map[string]int{}
	use(m, m)
	s := []int{}
	use(s, s)
	c := 1
	use(c, c)
	t := T{a: 1}
	use(t, t)
	var i any = 0
	use(i, i)
}

func carveouts() {
	for i := 0; i < 3; i++ {
		use(i)
	}
	if j := 0; j == 0 {
		use(j)
	}
}

func news() {
	use(&T{})                  // want `&T\{\} can be new\(T\)`
	if p := (&T{}); p != nil { // want `&T\{\} can be new\(T\)`
		use(p)
	}
	use((&T{}).a) // want `&T\{\} can be new\(T\)`
}

func newNeg() {
	use(&T{a: 1}, &map[string]int{})
}

var wrapped = func() int { return f2() }() // want `package-level IIFE can be var plus func init`

// complexInit needs more than one statement.
var complexInit = func() int { // want `package-level IIFE can be var plus func init`
	n := f2()
	return n * 2
}()

func f2() int { return 2 }

// dependent reads another package variable.
var dependent = func() int { // want `package-level IIFE can be var plus func init`
	n := f2()
	return n + 1
}()

// reader consumes dependent's value.
var reader = dependent + 1

func shadowedNew() {
	new := 1
	use(new, &T{})
}

func commentedLiteral() {
	t := T{ // holds the working set
	}
	use(t)
}

type state int

const stateIdle state = 0

func namedConst() {
	s := stateIdle
	use(s)
	var t state = stateIdle // want `non-zero value can be declared with :=: t := stateIdle`
	use(t)
}
