package backend

import (
	"testing"
)

func TestEnvFileQuotesAndSorts(t *testing.T) {
	rendered := EnvFile(map[string]string{
		"B_KEY": "plain",
		"A_KEY": "has 'quote' and $dollar",
	})
	want := "A_KEY='has '\\''quote'\\'' and $dollar'\nB_KEY='plain'\n"
	if rendered != want {
		t.Fatalf("EnvFile()\n got: %q\nwant: %q", rendered, want)
	}
}
