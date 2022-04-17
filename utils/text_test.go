package utils

import (
	"fmt"
	"testing"
)

func TestMarkdownEscaper(t *testing.T) {
	testStrs := []string{
		"Hello! This_, string=funny*formatting #.",
		"1*3 = 2+1 {-.-}",
	}

	for _, testStr := range testStrs {
		ret := PrepareInputForMarkdown(testStr, "text")
		fmt.Printf("Parsed: %s\n\t--> %s\n\n", testStr, ret)
	}

	testLinks := []string{
		"https://shop.spacex.com/collections/accessories/products/occupy-mars-heat-sensitive-terraforming-mug-new",
		"https://imaginary.url.com/request&params=(x)?q=(y)",
	}

	for _, testLink := range testLinks {
		ret := PrepareInputForMarkdown(testLink, "link")
		fmt.Printf("Parsed: %s\n\t--> %s\n\n", testLink, ret)
	}

	testMonospaceStrs := []string{
		"Hello! This is a description that has lots of spaces.",
	}

	for _, testStr := range testMonospaceStrs {
		ret := Monospaced(testStr)
		fmt.Printf("Parsed: %s\n\t--> %s\n\n", testStr, ret)
	}
}
