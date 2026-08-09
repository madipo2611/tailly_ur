package billing

import "testing"

func TestValidStatus(t *testing.T) {
	if !ValidStatus("active") || ValidStatus("anything") {
		t.Fatal("unexpected status validation")
	}
}
