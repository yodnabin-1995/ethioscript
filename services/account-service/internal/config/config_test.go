package config

import (
	"reflect"
	"testing"
)

func TestParseAllowedOriginsTrimsWhitespace(t *testing.T) {
	got := parseAllowedOrigins("http://localhost:3000, http://127.0.0.1:3000 ")
	want := []string{"http://localhost:3000", "http://127.0.0.1:3000"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseAllowedOrigins() = %#v, want %#v", got, want)
	}
}
