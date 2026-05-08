package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, "cairo-registry: stub — full implementation in Phase 2.1")
	os.Exit(1)
}
