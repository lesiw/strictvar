# lesiw.io/strictvar

[![Go Reference](https://pkg.go.dev/badge/lesiw.io/strictvar.svg)](https://pkg.go.dev/lesiw.io/strictvar)
[![CI](https://github.com/lesiw/strictvar/actions/workflows/main.yml/badge.svg?branch=main)](https://github.com/lesiw/strictvar/actions/workflows/main.yml)
[![Release](https://img.shields.io/github/v/tag/lesiw/strictvar?sort=semver&label=release)](https://github.com/lesiw/strictvar/tags)
[![Go Version](https://img.shields.io/github/go-mod/go-version/lesiw/strictvar)](../go.mod)
[![Discord](https://img.shields.io/discord/1145827224516300971?logo=discord&logoColor=white&color=5865F2&label=discord)](https://lesiw.dev/discord)
[![License](https://img.shields.io/github/license/lesiw/strictvar)](../LICENSE)

An experimental analyzer for those who believe there are too many
ways to declare a variable in Go.

`strictvar` is a formatter with type information. Whether a
declaration carries a zero value is invisible to syntax alone, so
these rules cannot live in a formatter, but running the fixes is
meant to feel like running one.

Two ideas underlie every check. Zero values stay visually
distinct, with no right-hand side and `new(T)` in place of `&T{}`,
so declaring one reads as a deliberate act, and setting variables
up ahead of use becomes the natural style. And where Go permits
many spellings of one declaration, whether `var` or `:=`, typed or
inferred, standalone or grouped, a single spelling is chosen.

`strictvar` is deliberately opinionated and likely over-strict.
Whether it makes real code clearer can only be learned by running
it against real code, so it ships as an experiment, expected to
loosen as results come in.

## Checks

Each check shows the flagged form with its diagnostic, then the
form its fix produces. Comments mark structure as deliberate
throughout: a declaration with a doc comment is its own unit and
never joins a group, a comment on its own line between declarations
keeps them separate, and doc and trailing comments ride along with
their specs through every rewrite. A fix is withheld when it would
strand a comment attached to nothing.

### Zero values use var

```go
x := 0                // zero value can be declared with var: var x int
t := T{}              // zero value can be declared with var: var t T
d := time.Duration(0) // zero value can be declared with var: var d time.Duration
var e error = nil     // zero value can be declared without a value: var e error
```

```go
var x int
var t T
var d time.Duration
var e error
```

- `for`, `if`, and `switch` init positions are exempt: no `var`
  declaration can exist there.
- `map[K]V{}`, `[]T{}`, and named constants are not zero values.
- Declarations grouped into a var block keep their `var` form: the
  block takes precedence.

### Non-zero values use :=

```go
var x = 42              // non-zero value can be declared with :=: x := 42
var t state = stateIdle // non-zero value can be declared with :=: t := stateIdle
```

```go
x := 42
t := stateIdle
```

- A type that changes its value's type, as in `var w io.Writer =
  buf` or `var d int32 = 42`, is exempt: widening is work `:=`
  cannot express, so `var` is its one form.
- Package scope has no `:=`, so package declarations keep `var`.

### new(T), not &T{}

A pointer to a zero struct or array is `new(T)`, in any expression
position. `new(T)` is not a zero value, so it takes the `:=` form.

```go
p := &T{} // &T{} can be new(T)
```

```go
p := new(T)
```

- Maps and slices are exempt: `&map[K]V{}` is a pointer to a
  non-nil empty map, which `new` cannot express.
- A scope where `new` is shadowed is exempt.

### Same-type zeros combine

Anywhere in a function, independent of block grouping.

```go
var i int
var j int // zero-value declarations of the same type can be combined: var i, j int
```

```go
var i, j int
```

- The combine outranks forming a block in the preamble and also
  applies to specs inside a var block.
- A doc comment, or a comment on its own line between the
  declarations, marks them deliberately separate.

### Adjacent declarations share a block

The same holds for `const`. At function scope the `var` form of
this rule applies only in the preamble, described below.

```go
var gx = work() // 2 adjacent var declarations can be grouped into a var block
var gy = work()
```

```go
var (
    gx = work()
    gy = work()
)
```

- `const` declarations that use `iota` never group or merge away:
  regrouping would renumber them.
- A doc-commented declaration never joins a group. At package scope
  its comment renders as prose on pkgsite, where a block spec's
  comment renders as source text.

### Adjacent blocks merge

A block immediately following another block of its kind can merge
into it, its specs folding onto the end of the block above one spec
per line, doc comments riding with their specs.

```go
var (
    gOne = work()
)

var (
    gTwo = work() // var block can be merged into the var block on line 1
)
```

```go
var (
    gOne = work()
    gTwo = work()
)
```

- At package scope a block placed beyond intervening code is the
  author's island. Inside a function it dissolves instead.
- Spec order inside a block is the author's, and no check reports
  hand-added line breaks.
- A block of explicit values may still join a preceding `iota`
  block, which leaves every value unchanged.

### The preamble is one var block

A var block belongs in the preamble, the run of declarations
opening a scope, setting variables up ahead of use. `:=` statements
join the run, since `var name, offset, abs = t.locabs()` is
expressible and later specs may read earlier ones.

```go
name, offset, abs := t.locabs() // 3 adjacent declarations can be grouped into a var block
days := abs.days()
var (
    month         Month
    day, min, sec int
)
```

```go
var (
    name, offset, abs = t.locabs()
    days              = abs.days()
    month             Month
    day, min, sec     int
)
```

- A redeclaring `:=`, a zero-value `:=`, and a doc-commented
  declaration each end the run. So does a `:=` declaring an `error`
  variable: error handling follows, and the call stays beside its
  check.
- A run that starts past the first statement of its list takes no
  part. Mid-scope declarations stay standalone, though same-type
  zeros still combine.

### Var blocks outside the preamble dissolve

Anywhere else in a function, a var block makes its declarations
read differently from the surrounding code, so it dissolves into
standalone declarations.

```go
use(1)
var (
    b []byte // var block outside the preamble can be dissolved into standalone declarations
    n = f()
)
```

```go
use(1)
var b []byte
n := f()
```

- Zero values keep `var`, initialized values take `:=`, and a spec
  whose type does work `:=` cannot express keeps `var` with its
  type.
- A block immediately following the preamble still merges into it,
  and `const` blocks are untouched.

### Redundant block types elide

```go
var (
    day  int
    year int = -1 // redundant type can be elided: year = -1
)
```

```go
var (
    day  int
    year = -1
)
```

- Specs whose type does real work, such as `w io.Writer = buf` or
  `b int32 = 2`, keep it.

### A commented spec starts a group

A doc comment inside a block starts a visual group, so a blank line
precedes it.

```go
var (
    day, min, sec int
    // limit bounds the scan.
    limit = 100 // commented declaration should be preceded by a blank line
)
```

```go
var (
    day, min, sec int

    // limit bounds the scan.
    limit = 100
)
```

### Returned zero values become named returns

A zero-value declaration in a function's top-level statement list
whose type matches exactly one unnamed result, and which is
returned in that position, belongs in the signature.

```go
func f() error { // function-scoped zero value err can be a named return
    var err error
    if cond() {
        err = work()
    }
    return err
}
```

```go
func f() (err error) {
    if cond() {
        err = work()
    }
    return err
}
```

- The fix is offered only for a standalone, uncommented declaration
  in a single-result function. Anywhere else the diagnostic reports
  without a fix.
- Variables captured by function literals are exempt: naming the
  result changes what a deferred closure's write means.
- Variables whose first use overwrites them entirely are exempt:
  they never carry their zero value, so they belong to `:=`.

### var plus func init, not IIFE

The IIFE is a JavaScript idiom, and the init function is the Go
one.

```go
var table = func() map[string]int { // package-level IIFE can be var plus func init
    m := make(map[string]int)
    m["a"] = 1
    return m
}()
```

```go
var table map[string]int

func init() {
    m := make(map[string]int)
    m["a"] = 1
    table = m
}
```

- A literal whose body is a lone return unwraps in place:
  `var x = f()`.
- The fix is withheld when another package-level initializer reads
  the variable, since it would see the zero value after the move.

### String content lives at its reader

An unexported package variable holding a quoted string literal,
bare or through a conversion, that is read exactly once inside a
function can be declared where it is read. No fix is offered:
placement inside the function is a judgment.

```go
var nullLiteral = []byte("null") // package variable nullLiteral is read once and can be declared where it is read

func decodeNull(data []byte) bool {
    return bytes.Equal(data, nullLiteral)
}
```

- Only interpreted literals qualify. A raw string literal is an
  embed and stays hoisted, a call such as `errors.New("...")`
  constructs a value with its own identity, and any other
  initializer is configuration that earns a package-scope name.
- Reassigned or address-taken variables, exported names, multi-name
  specifications, and reads inside package-scope initializers never
  qualify.

### Declare where assigned

A zero-value declaration whose first use overwrites the whole
variable in the same statement list collapses into the assignment.

```go
var m map[string]int
m = make(map[string]int) // m can be declared where it is made

var err error
err = work() // err can be declared where it is assigned
return err
```

```go
m := make(map[string]int)

err := work()
return err
```

When the declaration lives in a preamble var block, or is about to
be grouped into one, and the assignment immediately follows the
block, the value folds into its spec instead.

```go
var (
    buf []byte
    m   map[string]int // m can be made in its declaration
)
m = make(map[string]int)
```

```go
var (
    buf []byte
    m   = make(map[string]int)
)
```

- The collapse only applies when `:=` reproduces the declared type
  exactly. `var x int32` followed by `x = 42` stays, because `x :=
  42` would infer `int`, and `var w io.Writer` assigned a concrete
  value stays, because the interface type is deliberate.
- A conditional assignment such as `if m == nil { m = make(...) }`
  is not reported, so `var err error` checked and conditionally set
  before a final `return err` survives untouched.

## Always allowed

A lone short variable declaration from a function call, `t, err :=
fn()`, is always expressible. At the start of a scope it is still
subject to grouping: a run of two or more declarations opening a
scope shares one var block.

## Fixes

Fixes are only suggested when there is exactly one obvious rewrite
and no information is lost. Applying fixes can expose further fixes
(a zero `:=` becomes a `var`, which then joins an adjacent block,
which then regroups), so the tool may need to run more than once
until all fixes apply cleanly.

## Usage

```sh
go get -tool lesiw.io/strictvar/cmd/strictvar
go tool strictvar ./...
```
