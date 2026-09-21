package dbmaintenance

import (
	"strings"
	"testing"
)

func TestRequireConfirmationExactMatch(t *testing.T) {
	if err := RequireConfirmation(strings.NewReader("RESTORE iot_edge\r\n"), "RESTORE iot_edge"); err != nil {
		t.Fatalf("RequireConfirmation() error = %v", err)
	}
	for _, input := range []string{"restore iot_edge\n", "RESTORE iot_edge \n", "RESTORE postgres\n"} {
		if err := RequireConfirmation(strings.NewReader(input), "RESTORE iot_edge"); err == nil {
			t.Fatalf("RequireConfirmation(%q) unexpectedly succeeded", input)
		}
	}
}

func TestRequireConfirmationBoundsInput(t *testing.T) {
	if err := RequireConfirmation(strings.NewReader(strings.Repeat("x", 600)), "x"); err == nil {
		t.Fatal("oversized confirmation unexpectedly succeeded")
	}
}
