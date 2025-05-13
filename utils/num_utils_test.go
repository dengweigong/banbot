package utils

import (
	"fmt"
	"testing"
)

func TestArgSortDesc(t *testing.T) {
	arr := []float64{5, 3, 6, 8, 1, 9}
	res := ArgSortDesc(arr)
	fmt.Printf("%v", res)
}
