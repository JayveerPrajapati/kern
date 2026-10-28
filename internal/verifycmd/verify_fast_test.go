package verifycmd

import (
	"reflect"
	"testing"
)

// TestFastModeHonorsExplicitTypes pins the 2026-10-26 audit finding:
// `kern verify build,security,architecture --fast` overwrote the explicit
// list with build+test and (with no changed packages) reported a green PASS
// having run only the build — security and architecture silently skipped.
func TestFastModeHonorsExplicitTypes(t *testing.T) {
	in := []string{"build", "security", "architecture"}
	if got := fastTypes(in, true); !reflect.DeepEqual(got, in) {
		t.Fatalf("fastTypes(explicit) = %v, want the requested list %v unchanged", got, in)
	}
	if got := fastTypes(nil, false); !reflect.DeepEqual(got, []string{"build", "test"}) {
		t.Fatalf("fastTypes(implicit) = %v, want [build test]", got)
	}
}
