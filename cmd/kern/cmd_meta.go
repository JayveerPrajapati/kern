package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/mcp"
)

func runVersion(rest []string) {
	fmt.Printf("kern %s\n", version)

}

func runGuide(rest []string) {
	fmt.Println(mcp.Guide())

}
