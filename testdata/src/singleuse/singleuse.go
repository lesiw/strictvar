package singleuse

import "errors"

func f() int { return 1 }

func use(...any) {}

var (
	greeting = "hello"        // want `package variable greeting is read once and can be declared where it is read`
	payload  = []byte("data") // want `package variable payload is read once and can be declared where it is read`
	num      = 12
	embed    = `large
content`
	rawConv    = []byte(`raw bytes`)
	twice      = "again"
	written    = "state"
	Exported   = "public"
	viaInit    = "seed"
	derived    = viaInit + "!"
	aliased    = "addr"
	errMade    = errors.New("constructed")
	one, other = "first", "second"
)

func reader() {
	use(greeting, payload, num, embed, rawConv, twice, derived)
	use(twice)
	written = "changed"
	use(written, Exported)
	use(&aliased)
	use(errMade, one, other)
}
