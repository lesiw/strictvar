package group

const (
	kA = 1
)

const (
	kB = 2 // want `const block can be merged into the const block on line 3`
)

const (
	kC = 3 // want `const block can be merged into the const block on line 7`
)

func sepA() {}

const (
	kD = iota
)

const (
	kE = iota
)

func sepB() {}

const (
	kF = 4
)

// Floating commentary between the blocks.

const (
	kG = 5
)

func sepC() {}

const (
	kH = 6
)

const ( // pinned header
	kI = 7 // want `const block can be merged into the const block on line 39`
)

func sepD() {}

const (
	kJ = iota
)

const (
	kK = 8 // want `const block can be merged into the const block on line 49`
)

func sepE() {}

const (
	kL = 9
)

const (
	// kM is documented.
	kM = 10 // want `const block can be merged into the const block on line 59`
)

var (
	gOne = work()
)

var (
	gTwo = work() // want `var block can be merged into the var block on line 68`
)

func sepF() {}

var gx = work() // want `2 adjacent var declarations can be grouped into a var block`
var gy = work()

func work() int { return 0 }
