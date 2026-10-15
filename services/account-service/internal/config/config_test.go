package config

import (
	"reflect"
	"testing"
)

func TestParseAllowedOriginsTrimsWhitespace(t *testing.T) {
	got := parseAllowedOrigins("http://localhost:6005, http://127.0.0.1:6005 ")
	want := []string{"http://localhost:6005", "http://127.0.0.1:6005"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseAllowedOrigins() = %#v, want %#v", got, want)
	}
}
