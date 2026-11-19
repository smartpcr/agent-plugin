// Command helper is the separately built child of the coverage-child fixture.
package main

import (
	"fmt"

	"example.com/covchild/p"
)

func main() {
	fmt.Println(p.Helper(2))
}
