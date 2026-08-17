package makedecl

func use(...any) {}

func work() error { return nil }

func assigned() error {
	var err error
	err = work() // want `err can be declared where it is assigned`
	return err
}

func constString() {
	var s string
	s = "hi" // want `s can be declared where it is assigned`
	use(s)
}

func constTyped() {
	var x int32
	x = 42
	use(x)
}

func maps() map[string]int {
	var m map[string]int
	m = make(map[string]int) // want `m can be declared where it is made`
	m["a"] = 1
	return m
}

func chans() {
	var ch chan int
	ch = make(chan int, 4) // want `ch can be declared where it is made`
	use(ch)
}

func slices() {
	var s []int
	s = make([]int, 3) // want `s can be declared where it is made`
	use(s)
}

func usedFirst() int {
	var m map[string]int
	if m == nil {
		m = make(map[string]int)
	}
	return len(m)
}

func appended() {
	var s []int
	s = append(s, 1)
	use(s)
}

func blockMake() {
	var (
		buf []byte
		m   map[string]int // want `m can be made in its declaration`
	)
	m = make(map[string]int)
	buf = append(buf, 1)
	use(m, buf)
}

func groupedMake() {
	var a int // want `3 adjacent declarations can be grouped into a var block`
	var b string
	var m map[string]int
	m = make(map[string]int)
	use(a, b, m)
}

func laterRead() {
	var (
		m map[string]int
		n int
	)
	m = make(map[string]int, n)
	use(m, n)
}
