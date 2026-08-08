package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"lesiw.io/strictvar"
)

func main() { singlechecker.Main(strictvar.Analyzer) }
