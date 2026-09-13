// Command keygen prints a new APP_KEY value.
package main

import (
	"fmt"

	"github.com/bmardale/stocat/internal/platform/crypt"
)

func main() {
	fmt.Println(crypt.GenerateKey())
}
