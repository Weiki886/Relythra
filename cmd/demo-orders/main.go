package main

import (
	"fmt"
	"github.com/Weiki886/relythra/internal/demo"
	"os"
)

func main() {
	if err := demo.Run("orders"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
