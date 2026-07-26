package canonicalhash_test

import (
	"testing"

	"github.com/JakeFAU/stacks/core/internal/canonicalhash"
)

func TestCanonicalHashLengthPrefixesPreventBoundaryCollisions(t *testing.T) {
	left := canonicalhash.New("stacks.test.v1")
	left.String("ab")
	left.String("c")

	right := canonicalhash.New("stacks.test.v1")
	right.String("a")
	right.String("bc")

	if left.Sum() == right.Sum() {
		t.Fatal("canonical hash collided for distinct length boundaries")
	}
}
